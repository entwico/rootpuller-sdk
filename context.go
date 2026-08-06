package rootpuller

import (
	"context"

	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// ContextWithDeployment overrides the client-wide rootpuller-deployment
// routing header for calls made with the returned context. The header
// selects between configured backend deployments (e.g. "local",
// "cloudrun") for the chunker, embedding, and vectorops services.
func ContextWithDeployment(ctx context.Context, name string) context.Context {
	return transport.ContextWithDeployment(ctx, name)
}

// ContextWithBot overrides the client-wide rootpuller-bot header for
// calls made with the returned context. The header selects the bot
// identity used by the scrape and webcontent services.
func ContextWithBot(ctx context.Context, name string) context.Context {
	return transport.ContextWithBot(ctx, name)
}

// Ptr returns a pointer to v — a shorthand for filling the optional
// (pointer-typed) fields of request structs: rootpuller.Ptr(512).
func Ptr[T any](v T) *T { return &v }
