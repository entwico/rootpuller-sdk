package rootpullersdk

import (
	"context"

	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// ContextWithDeployment sets the rootpuller-deployment routing header
// for calls made with the returned context, overriding any
// WithDeployment default on the service client. The header selects
// between configured backend deployments (e.g. "local", "cloudrun") and
// is only meaningful for the chunker, embedding, and vectorops services
// (see their WithDeployment methods for client-scoped defaults).
func ContextWithDeployment(ctx context.Context, name string) context.Context {
	return transport.ContextWithDeployment(ctx, name)
}

// ContextWithBot sets the rootpuller-bot header for calls made with the
// returned context, overriding any WithBot default on the service
// client. The header selects the bot identity and is only meaningful for
// the webcontent and scrape services (see their WithBot methods for
// client-scoped defaults).
func ContextWithBot(ctx context.Context, name string) context.Context {
	return transport.ContextWithBot(ctx, name)
}

// Ptr returns a pointer to v — a shorthand for filling the optional
// (pointer-typed) fields of request structs: rootpullersdk.Ptr(512).
func Ptr[T any](v T) *T { return new(v) }

// Trailers receives the response trailers of a unary call when the
// caller opted in via CaptureTrailers. The server reports adaptive
// rate-limit capacity in the "x-ratelimit-limit" and
// "x-ratelimit-remaining" trailers.
type Trailers = transport.Trailers

// CaptureTrailers returns a context that makes the next unary SDK call
// on it record its response trailers into the returned Trailers — the
// hook for client-side adaptive concurrency control. With WithRetry
// enabled, the final attempt's trailers are reported.
func CaptureTrailers(ctx context.Context) (context.Context, *Trailers) {
	return transport.CaptureTrailers(ctx)
}
