package transport

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
)

// Metadata header names understood by rootpuller-api, in canonical MIME
// form ("rootpuller-deployment" / "rootpuller-bot" on the wire — gRPC
// metadata keys are case-insensitive).
const (
	DeploymentHeader = "Rootpuller-Deployment"
	BotHeader        = "Rootpuller-Bot"
)

type ctxKey int

const (
	deploymentCtxKey ctxKey = iota
	botCtxKey
)

// ContextWithDeployment returns a context that sets the
// rootpuller-deployment header for calls made with it, overriding any
// service-client default.
func ContextWithDeployment(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, deploymentCtxKey, name)
}

// ContextWithBot returns a context that sets the rootpuller-bot header
// for calls made with it, overriding any service-client default.
func ContextWithBot(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, botCtxKey, name)
}

// EnsureDeployment injects a service-client deployment default unless
// the context already carries an explicit value (which wins).
func EnsureDeployment(ctx context.Context, name string) context.Context {
	if name == "" {
		return ctx
	}

	if _, ok := ctx.Value(deploymentCtxKey).(string); ok {
		return ctx
	}

	return ContextWithDeployment(ctx, name)
}

// EnsureBot injects a service-client bot default unless the context
// already carries an explicit value (which wins).
func EnsureBot(ctx context.Context, name string) context.Context {
	if name == "" {
		return ctx
	}

	if _, ok := ctx.Value(botCtxKey).(string); ok {
		return ctx
	}

	return ContextWithBot(ctx, name)
}

// NewHeadersInterceptor turns the context values set by
// ContextWithDeployment/ContextWithBot (or the service clients' Ensure*
// defaults) into wire headers. An empty value sends no header (the
// server treats absent and empty alike, but absent keeps requests
// clean).
func NewHeadersInterceptor() connect.Interceptor {
	return &headersInterceptor{}
}

type headersInterceptor struct{}

func (i *headersInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		i.apply(ctx, req.Header())

		return next(ctx, req)
	}
}

func (i *headersInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		i.apply(ctx, conn.RequestHeader())

		return conn
	}
}

func (*headersInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

func (*headersInterceptor) apply(ctx context.Context, h http.Header) {
	if v, ok := ctx.Value(deploymentCtxKey).(string); ok && v != "" {
		h.Set(DeploymentHeader, v)
	}

	if v, ok := ctx.Value(botCtxKey).(string); ok && v != "" {
		h.Set(BotHeader, v)
	}
}
