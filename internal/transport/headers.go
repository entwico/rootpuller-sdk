package transport

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
)

// Metadata header names understood by rootpuller-api.
const (
	DeploymentHeader = "rootpuller-deployment"
	BotHeader        = "rootpuller-bot"
)

type ctxKey int

const (
	deploymentCtxKey ctxKey = iota
	botCtxKey
)

// ContextWithDeployment returns a context that overrides the client-wide
// rootpuller-deployment header for calls made with it.
func ContextWithDeployment(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, deploymentCtxKey, name)
}

// ContextWithBot returns a context that overrides the client-wide
// rootpuller-bot header for calls made with it.
func ContextWithBot(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, botCtxKey, name)
}

// NewHeadersInterceptor injects the deployment/bot routing headers.
// Per-call context values win over the client-wide defaults; an empty
// resolved value sends no header (the server treats absent and empty
// alike, but absent keeps requests clean).
func NewHeadersInterceptor(defaultDeployment, defaultBot string) connect.Interceptor {
	return &headersInterceptor{deployment: defaultDeployment, bot: defaultBot}
}

type headersInterceptor struct {
	deployment string
	bot        string
}

func (i *headersInterceptor) apply(ctx context.Context, h http.Header) {
	deployment := i.deployment
	if v, ok := ctx.Value(deploymentCtxKey).(string); ok {
		deployment = v
	}
	if deployment != "" {
		h.Set(DeploymentHeader, deployment)
	}
	bot := i.bot
	if v, ok := ctx.Value(botCtxKey).(string); ok {
		bot = v
	}
	if bot != "" {
		h.Set(BotHeader, bot)
	}
}

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

func (i *headersInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
