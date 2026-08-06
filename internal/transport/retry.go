package transport

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/internal/apierr"
)

// RetryConfig bounds the transient-failure retry loop.
type RetryConfig struct {
	// MaxAttempts is the total number of tries including the first one.
	MaxAttempts int
	// MaxElapsed caps the whole retry budget including waits.
	MaxElapsed time.Duration
	// InitialInterval is the first backoff delay; it doubles per attempt
	// (with jitter) and is overridden by the server's RetryAfter hint
	// when present.
	InitialInterval time.Duration
}

// NewRetryInterceptor retries unary calls that fail transiently
// (apierr.IsTransient: Unavailable, DeadlineExceeded, ResourceExhausted,
// Aborted) with exponential backoff and jitter, honoring the server's
// google.rpc.RetryInfo hint. Streams are never retried.
func NewRetryInterceptor(cfg RetryConfig) connect.Interceptor {
	return &retryInterceptor{cfg: cfg}
}

type retryInterceptor struct {
	cfg RetryConfig
}

func (i *retryInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		deadline := time.Now().Add(i.cfg.MaxElapsed)
		interval := i.cfg.InitialInterval

		var lastErr error

		for attempt := range i.cfg.MaxAttempts {
			if attempt > 0 {
				wait := interval
				// The server's hint (e.g. backpressure shedding) wins
				// over our schedule.
				var ae *apierr.Error
				if errors.As(lastErr, &ae) && ae.RetryAfter > 0 {
					wait = ae.RetryAfter
				}
				// Full jitter avoids thundering herds on recovery.
				wait = time.Duration(rand.Int64N(int64(wait)) + int64(wait)/2) //nolint:gosec // jitter, not crypto

				if time.Now().Add(wait).After(deadline) {
					break
				}

				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()

					return nil, WrapError(ctx.Err(), req.Spec().Procedure)
				case <-timer.C:
				}

				interval *= 2
			}

			resp, err := next(ctx, req)
			if err == nil {
				return resp, nil
			}

			lastErr = WrapError(err, req.Spec().Procedure)
			if !apierr.IsTransient(lastErr) {
				return nil, lastErr
			}
		}

		return nil, lastErr
	}
}

func (*retryInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next // streams carry state; retrying them is the caller's call
}

func (*retryInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
