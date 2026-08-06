package transport

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

// The five rootpuller-backed services advertise their per-deployment
// capacity in-band on every response:
//
//   - x-ratelimit-limit      (trailer)  max concurrent requests the server accepts now
//   - x-ratelimit-remaining  (trailer)  free slots right now (0 = full)
//   - retry-after            (trailer)  seconds to wait, on a shed
//   - RESOURCE_EXHAUSTED + google.rpc.RetryInfo (status) request shed
//
// BackpressureGate tracks the advertised limit as a dynamic ceiling and
// runs an AIMD effective-concurrency value just under it: shrink on shed /
// remaining==0, grow on success. If ops change the server limit, clients
// follow automatically — no redeploy.
const (
	defaultSeedLimit = 4
	minLimit         = 1
	decreaseFactor   = 0.5
	defaultCooldown  = time.Second
	// fallbackShedDelay is the base pause when a shed carries no retry
	// hint; it grows decorrelated-exponentially with consecutive sheds
	// (a cold-starting backend won't free up in 500ms). maxShedDelay
	// caps any pause.
	fallbackShedDelay = 500 * time.Millisecond
	maxShedDelay      = 30 * time.Second
	// shedDecorrelationCap bounds the exponent of the no-hint backoff.
	shedDecorrelationCap = 6
	// growHoldoff suppresses additive increase for this long after a
	// multiplicative decrease, so effective concurrency settles below
	// the shed ceiling instead of oscillating straight back into it.
	growHoldoff = 2 * time.Second
	// breakerTripSheds / breakerCooldown: after this many consecutive
	// sheds with no intervening success, slam effective to the floor and
	// hold all admissions for the cooldown — the deep-outage backstop.
	breakerTripSheds = 8
	breakerCooldown  = 10 * time.Second
)

// BackpressureGate is the shared admission gate behind the public
// rootpullersdk.Backpressure handle. Safe for concurrent use.
type BackpressureGate struct {
	mu          sync.Mutex
	notFull     *sync.Cond
	seed        int
	serverLimit int
	effective   float64
	inFlight    int
	success     int
	lastDrop    time.Time
	// shedUntil is a shared admission gate: while now < shedUntil every
	// acquire blocks, so a shed pauses ALL goroutines for the server's
	// requested delay instead of each one independently re-racing a
	// saturated backend.
	shedUntil        time.Time
	consecutiveSheds int
	nowFn            func() time.Time
	cooldown         time.Duration
}

// NewBackpressureGate seeds the concurrency ceiling with seed (used only
// until the first x-ratelimit-limit arrives). seed <= 0 falls back to the
// package default.
func NewBackpressureGate(seed int) *BackpressureGate {
	if seed < minLimit {
		seed = defaultSeedLimit
	}

	g := &BackpressureGate{
		seed:      seed,
		effective: float64(seed),
		nowFn:     time.Now,
		cooldown:  defaultCooldown,
	}
	g.notFull = sync.NewCond(&g.mu)

	return g
}

// NewBackpressureInterceptor gates every call through g and feeds the
// server's capacity signals back into it. It runs innermost (per retry
// attempt): each attempt re-acquires a slot and respects the shared shed
// pause, so it composes with the retry interceptor naturally.
func NewBackpressureInterceptor(g *BackpressureGate) connect.Interceptor {
	return &backpressureInterceptor{gate: g}
}

type backpressureInterceptor struct {
	gate *BackpressureGate
}

func (i *backpressureInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := i.gate.acquire(ctx); err != nil {
			return nil, err
		}

		resp, err := next(ctx, req)

		i.gate.release()

		if err == nil {
			i.gate.observe(parseRateLimitHeader(resp.Trailer().Get("X-Ratelimit-Limit"), resp.Trailer().Get("X-Ratelimit-Remaining")))
			i.gate.clearSheds()

			return resp, nil
		}

		i.gate.observeError(err)

		return nil, err
	}
}

func (i *backpressureInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		// Streams hold a slot for their whole lifetime; the facade
		// helpers always CloseResponse, which releases it.
		if err := i.gate.acquire(ctx); err != nil {
			return &failedStreamingClientConn{StreamingClientConn: next(ctx, spec), err: connect.NewError(connect.CodeOf(err), err)}
		}

		return &gatedStreamingClientConn{StreamingClientConn: next(ctx, spec), gate: i.gate}
	}
}

func (*backpressureInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// gatedStreamingClientConn releases the gate slot when the stream ends
// and feeds terminal capacity signals back. The facade drives streams
// from one goroutine and always calls CloseResponse after the receive
// loop ends, which this relies on.
type gatedStreamingClientConn struct {
	connect.StreamingClientConn

	gate    *BackpressureGate
	once    sync.Once
	sawShed bool
}

func (c *gatedStreamingClientConn) Receive(msg any) error {
	err := c.StreamingClientConn.Receive(msg)
	if err != nil && !errors.Is(err, io.EOF) && connect.CodeOf(err) == connect.CodeResourceExhausted {
		c.sawShed = true
	}

	return err
}

func (c *gatedStreamingClientConn) CloseResponse() error {
	err := c.StreamingClientConn.CloseResponse()
	c.once.Do(func() {
		c.gate.release()

		trailer := c.ResponseTrailer()

		if c.sawShed {
			c.gate.shed(0)
		} else {
			c.gate.observe(parseRateLimitHeader(trailer.Get("X-Ratelimit-Limit"), trailer.Get("X-Ratelimit-Remaining")))
			c.gate.clearSheds()
		}
	})

	return err
}

// observeError folds a failed unary attempt into the gate: an explicit
// shed shrinks the AIMD value and opens the shared pause using the
// server's hint (RetryInfo detail or retry-after seconds), or a
// decorrelated backoff when no hint rode along.
func (g *BackpressureGate) observeError(err error) {
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		return
	}

	g.shed(shedHint(err))
}

// shed applies the multiplicative decrease and opens the shared pause.
// hint 0 means "no server hint": use the decorrelated backoff.
func (g *BackpressureGate) shed(hint time.Duration) {
	g.mu.Lock()
	g.consecutiveSheds++
	g.decreaseLocked()
	g.mu.Unlock()

	wait := hint
	if wait <= 0 {
		wait = g.decorrelatedShedDelay()
	}

	if wait > maxShedDelay {
		wait = maxShedDelay
	}

	wait += jitter(wait)

	g.pauseUntil(wait)
}

// shedHint extracts the server's requested retry delay from the
// google.rpc.RetryInfo error detail or the retry-after trailer of a
// connect error. Zero when absent.
func shedHint(err error) time.Duration {
	var ce *connect.Error
	if !errors.As(err, &ce) {
		return 0
	}

	for _, detail := range ce.Details() {
		value, verr := detail.Value()
		if verr != nil {
			continue
		}

		if info, ok := value.(*errdetails.RetryInfo); ok && info.GetRetryDelay() != nil {
			return info.GetRetryDelay().AsDuration()
		}
	}

	if v := ce.Meta().Get("Retry-After"); v != "" {
		if secs, perr := strconv.Atoi(strings.TrimSpace(v)); perr == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
	}

	return 0
}

func (g *BackpressureGate) acquire(ctx context.Context) error {
	// Shared shed-pause gate: hold new admissions until the server's
	// requested cooldown passes. Re-checked each pass since a concurrent
	// shed can extend it.
	for {
		g.mu.Lock()
		d := g.shedUntil.Sub(g.nowFn())
		g.mu.Unlock()

		if d <= 0 {
			break
		}

		if err := sleepCtx(ctx, d); err != nil {
			return err
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	for g.inFlight >= int(g.effective) {
		if err := ctx.Err(); err != nil {
			return err
		}

		stop := context.AfterFunc(ctx, func() {
			g.mu.Lock()
			g.notFull.Broadcast()
			g.mu.Unlock()
		})

		g.notFull.Wait()
		stop()

		if err := ctx.Err(); err != nil {
			return err
		}
	}

	g.inFlight++

	return nil
}

func (g *BackpressureGate) release() {
	g.mu.Lock()
	if g.inFlight > 0 {
		g.inFlight--
	}

	g.notFull.Broadcast()
	g.mu.Unlock()
}

// rateLimit is one response's parsed capacity advertisement. ok is false
// when the response carried no x-ratelimit trailers (non-routable
// backends, older servers).
type rateLimit struct {
	limit     int
	remaining int
	ok        bool
}

// observe folds a successful response's capacity signals into the gate:
// it adopts the advertised ceiling, and grows the effective concurrency
// on free slots or shrinks it when the server reports it is full
// (remaining == 0).
func (g *BackpressureGate) observe(rl rateLimit) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if rl.ok && rl.limit > 0 {
		g.serverLimit = rl.limit
	}

	ceil := g.ceilingLocked()
	if g.effective > float64(ceil) {
		g.effective = float64(ceil)
		g.notFull.Broadcast()
	}

	// remaining == 0 means the server is at capacity right now — ease off.
	if rl.ok && rl.remaining == 0 {
		g.decreaseLocked()

		return
	}

	g.increaseLocked(ceil)
}

func (g *BackpressureGate) clearSheds() {
	g.mu.Lock()
	g.consecutiveSheds = 0
	g.mu.Unlock()
}

func (g *BackpressureGate) decorrelatedShedDelay() time.Duration {
	g.mu.Lock()
	n := g.consecutiveSheds
	g.mu.Unlock()

	if n < 1 {
		n = 1
	}

	d := min(fallbackShedDelay<<min(n-1, shedDecorrelationCap), maxShedDelay)

	return d
}

// pauseUntil opens the shared admission gate for d (extending, never
// shortening, any existing pause). On a sustained shed streak it trips
// the circuit breaker: effective drops to the floor and the pause widens
// to the breaker cooldown.
func (g *BackpressureGate) pauseUntil(d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.consecutiveSheds >= breakerTripSheds {
		g.effective = minLimit
		g.success = 0

		if d < breakerCooldown {
			d = breakerCooldown
		}
	}

	if until := g.nowFn().Add(d); until.After(g.shedUntil) {
		g.shedUntil = until
	}
}

func (g *BackpressureGate) ceilingLocked() int {
	if g.serverLimit > 0 {
		return g.serverLimit
	}

	return g.seed
}

func (g *BackpressureGate) decreaseLocked() {
	// Collapse-guard: many in-flight calls shed near-simultaneously;
	// only the first within a cooldown window shrinks so it doesn't
	// crater to the floor.
	now := g.nowFn()
	if !g.lastDrop.IsZero() && now.Sub(g.lastDrop) < g.cooldown {
		return
	}

	g.lastDrop = now

	g.effective *= decreaseFactor
	if g.effective < minLimit {
		g.effective = minLimit
	}

	g.success = 0
}

func (g *BackpressureGate) increaseLocked(ceil int) {
	if g.effective >= float64(ceil) {
		return
	}

	// Hold off growth right after a multiplicative decrease so effective
	// settles below the shed ceiling instead of oscillating back into it.
	if !g.lastDrop.IsZero() && g.nowFn().Sub(g.lastDrop) < growHoldoff {
		return
	}

	// Additive increase: +1 slot per ~full window of successes.
	g.success++
	if g.success < int(g.effective)+1 {
		return
	}

	g.success = 0

	g.effective++
	if g.effective > float64(ceil) {
		g.effective = float64(ceil)
	}

	g.notFull.Broadcast()
}

func parseRateLimitHeader(limitVal, remainingVal string) rateLimit {
	if limitVal == "" {
		return rateLimit{}
	}

	l, err := strconv.Atoi(strings.TrimSpace(limitVal))
	if err != nil {
		return rateLimit{}
	}

	// -1 = unknown; observe treats anything != 0 as "has free slots".
	rem := -1

	if remainingVal != "" {
		if r, rerr := strconv.Atoi(strings.TrimSpace(remainingVal)); rerr == nil {
			rem = r
		}
	}

	return rateLimit{limit: l, remaining: rem, ok: true}
}

func jitter(d time.Duration) time.Duration {
	span := int64(d) / 5
	if span <= 0 {
		return 0
	}

	return time.Duration(rand.Int64N(span)) //nolint:gosec // decorrelation jitter, not crypto
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
