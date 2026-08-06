package rootpullersdk_test

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/types/known/durationpb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/chunker"
	chunkerpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chunker"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chunker/chunkerconnect"
	rerankpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/rerank"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/rerank/rerankconnect"
	"github.com/entwico/rootpuller-sdk/rerank"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

// capacityServer mimics the rootpuller-backed services' capacity
// protocol: a shared in-flight counter across services, x-ratelimit
// trailers, and optional sheds.
type capacityServer struct {
	limit int32

	inFlight    atomic.Int32
	maxInFlight atomic.Int32
	calls       atomic.Int64
	shedsLeft   atomic.Int64
	shedHint    time.Duration

	mu        sync.Mutex
	callTimes []time.Time
}

var errAtCapacity = errors.New("at capacity")

func (s *capacityServer) enter() error {
	s.calls.Add(1)

	s.mu.Lock()
	s.callTimes = append(s.callTimes, time.Now())
	s.mu.Unlock()

	if s.shedsLeft.Add(-1) >= 0 {
		cerr := connect.NewError(connect.CodeResourceExhausted, errAtCapacity)

		if s.shedHint > 0 {
			if detail, derr := connect.NewErrorDetail(&errdetails.RetryInfo{RetryDelay: durationpb.New(s.shedHint)}); derr == nil {
				cerr.AddDetail(detail)
			}
		}

		return cerr
	}

	cur := s.inFlight.Add(1)
	for {
		observed := s.maxInFlight.Load()
		if cur <= observed || s.maxInFlight.CompareAndSwap(observed, cur) {
			break
		}
	}

	// Hold the slot long enough for overlap to be observable.
	time.Sleep(30 * time.Millisecond)

	return nil
}

func (s *capacityServer) exitTrailers(t http.Header) {
	in := s.inFlight.Add(-1)
	t.Set("X-Ratelimit-Limit", strconv.Itoa(int(s.limit)))
	t.Set("X-Ratelimit-Remaining", strconv.Itoa(max(int(s.limit)-int(in), 1)))
}

type capacityRerank struct {
	rerankconnect.UnimplementedRerankServiceHandler

	srv *capacityServer
}

func (h *capacityRerank) Rerank(_ context.Context, _ *connect.Request[rerankpb.RerankRequest]) (*connect.Response[rerankpb.RerankResponse], error) {
	if err := h.srv.enter(); err != nil {
		return nil, err
	}

	resp := connect.NewResponse(&rerankpb.RerankResponse{})
	h.srv.exitTrailers(resp.Trailer())

	return resp, nil
}

type capacityChunker struct {
	chunkerconnect.UnimplementedTextChunkerServiceHandler

	srv *capacityServer
}

func (h *capacityChunker) ChunkToken(_ context.Context, _ *connect.Request[chunkerpb.ChunkTokenRequest]) (*connect.Response[chunkerpb.TextChunkResponse], error) {
	if err := h.srv.enter(); err != nil {
		return nil, err
	}

	resp := connect.NewResponse(&chunkerpb.TextChunkResponse{})
	h.srv.exitTrailers(resp.Trailer())

	return resp, nil
}

func newCapacityStack(t *testing.T, srv *capacityServer, sdkOpts ...rootpullersdk.Option) (*rerank.Service, *chunker.Service) {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle(rerankconnect.NewRerankServiceHandler(&capacityRerank{srv: srv}))
	mux.Handle(chunkerconnect.NewTextChunkerServiceHandler(&capacityChunker{srv: srv}))
	ts := rootpullertest.NewServerWithMux(t, mux)

	sdk, err := rootpullersdk.New(ts.URL, sdkOpts...)
	if err != nil {
		t.Fatal(err)
	}

	bp := rootpullersdk.NewBackpressure(rootpullersdk.BackpressureOptions{SeedConcurrency: 8})

	return rerank.NewService(sdk, rerank.WithBackpressure(bp)),
		chunker.NewService(sdk, chunker.WithBackpressure(bp))
}

func TestBackpressureAdoptsAdvertisedLimit(t *testing.T) {
	t.Parallel()

	srv := &capacityServer{limit: 1}
	reranker, _ := newCapacityStack(t, srv)

	// Warm-up call teaches the gate the advertised limit of 1.
	if _, err := reranker.Rerank(t.Context(), "q", []string{"d"}, nil); err != nil {
		t.Fatal(err)
	}

	srv.maxInFlight.Store(0)

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, err := reranker.Rerank(t.Context(), "q", []string{"d"}, nil); err != nil {
				t.Error(err)
			}
		})
	}

	wg.Wait()

	if got := srv.maxInFlight.Load(); got != 1 {
		t.Errorf("server observed %d concurrent calls, want 1 (adopted limit)", got)
	}
}

func TestBackpressureSharedAcrossServices(t *testing.T) {
	t.Parallel()

	srv := &capacityServer{limit: 1}
	reranker, chunk := newCapacityStack(t, srv)

	if _, err := reranker.Rerank(t.Context(), "q", []string{"d"}, nil); err != nil {
		t.Fatal(err)
	}

	srv.maxInFlight.Store(0)

	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if _, err := reranker.Rerank(t.Context(), "q", []string{"d"}, nil); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if _, err := chunk.ChunkToken(t.Context(), []string{"x"}, nil); err != nil {
				t.Error(err)
			}
		})
	}

	wg.Wait()

	// One shared gate bounds BOTH services' calls together.
	if got := srv.maxInFlight.Load(); got != 1 {
		t.Errorf("server observed %d concurrent calls across services, want 1", got)
	}
}

func TestBackpressureShedPausesAndComposesWithRetry(t *testing.T) {
	t.Parallel()

	const hint = 300 * time.Millisecond

	srv := &capacityServer{limit: 4, shedHint: hint}
	srv.shedsLeft.Store(1)

	reranker, _ := newCapacityStack(t, srv,
		rootpullersdk.WithRetry(rootpullersdk.RetryOptions{InitialInterval: time.Millisecond}))

	start := time.Now()

	if _, err := reranker.Rerank(t.Context(), "q", []string{"d"}, nil); err != nil {
		t.Fatalf("want success after shed retry, got %v", err)
	}

	if got := srv.calls.Load(); got != 2 {
		t.Fatalf("server saw %d calls, want 2 (shed + success)", got)
	}

	// The retry had to wait out the gate's shed pause (server hint).
	if elapsed := time.Since(start); elapsed < hint {
		t.Errorf("retry completed after %v, want >= %v (shed pause)", elapsed, hint)
	}
}

func TestBackpressureShedPauseRespectsContext(t *testing.T) {
	t.Parallel()

	srv := &capacityServer{limit: 4, shedHint: 5 * time.Second}
	srv.shedsLeft.Store(1)

	reranker, _ := newCapacityStack(t, srv)

	// First call sheds and opens a long pause.
	_, err := reranker.Rerank(t.Context(), "q", []string{"d"}, nil)
	if !errors.Is(err, rootpullersdk.ErrResourceExhausted) {
		t.Fatalf("err = %v, want ErrResourceExhausted", err)
	}

	// A second call must not hang for the full pause when its context
	// expires first.
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()

	_, err = reranker.Rerank(ctx, "q", []string{"d"}, nil)
	if err == nil {
		t.Fatal("want context error while gate is paused")
	}

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("call blocked %v despite 100ms context deadline", elapsed)
	}
}
