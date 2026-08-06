package transport

import (
	"context"
	"net/http"
	"sync"

	"connectrpc.com/connect"
)

// Trailers receives the response trailers of a unary call when the caller
// opted in via CaptureTrailers. Safe for concurrent use.
type Trailers struct {
	mu sync.Mutex
	h  http.Header
}

// Get returns the first value of the (case-insensitive) trailer key, or
// "" when absent.
func (t *Trailers) Get(key string) string {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.h == nil {
		return ""
	}

	return t.h.Get(key)
}

func (t *Trailers) set(h http.Header) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.h = h.Clone()
}

type trailersCtxKey struct{}

// CaptureTrailers returns a context that makes the next unary call on it
// record its response trailers into the returned Trailers.
func CaptureTrailers(ctx context.Context) (context.Context, *Trailers) {
	t := &Trailers{}

	return context.WithValue(ctx, trailersCtxKey{}, t), t
}

// NewTrailerInterceptor copies unary response trailers into the
// context's Trailers holder. It must run outermost so a retried call
// reports the final attempt's trailers.
func NewTrailerInterceptor() connect.Interceptor {
	return &trailerInterceptor{}
}

type trailerInterceptor struct{}

func (*trailerInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		resp, err := next(ctx, req)

		if holder, ok := ctx.Value(trailersCtxKey{}).(*Trailers); ok && resp != nil {
			holder.set(resp.Trailer())
		}

		return resp, err
	}
}

func (*trailerInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (*trailerInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
