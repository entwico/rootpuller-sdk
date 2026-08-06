package transport

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"golang.org/x/oauth2"
)

// NewAuthInterceptor injects "Authorization: Bearer <token>" on every
// unary call and client stream, fetching the token from ts per call
// (callers wrap ts in oauth2.ReuseTokenSource, so this is a cheap cached
// read that refreshes only on expiry). A token-source failure surfaces as
// CodeUnauthenticated wrapping the original error.
func NewAuthInterceptor(ts oauth2.TokenSource) connect.Interceptor {
	return &authInterceptor{ts: ts}
}

type authInterceptor struct {
	ts oauth2.TokenSource
}

func (i *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		bearer, cerr := i.bearer()
		if cerr != nil {
			return nil, cerr
		}

		req.Header().Set("Authorization", bearer)

		return next(ctx, req)
	}
}

func (i *authInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)

		bearer, cerr := i.bearer()
		if cerr != nil {
			return &failedStreamingClientConn{StreamingClientConn: conn, err: cerr}
		}

		conn.RequestHeader().Set("Authorization", bearer)

		return conn
	}
}

func (*authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next // client-side SDK; handlers untouched
}

func (i *authInterceptor) bearer() (string, *connect.Error) {
	tok, err := i.ts.Token()
	if err != nil {
		return "", connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("rootpuller: fetching oauth2 token: %w", err))
	}

	return "Bearer " + tok.AccessToken, nil
}

// failedStreamingClientConn fails every stream operation with the token
// error, because WrapStreamingClient cannot itself return an error.
type failedStreamingClientConn struct {
	connect.StreamingClientConn

	err *connect.Error
}

func (c *failedStreamingClientConn) Send(any) error    { return c.err }
func (c *failedStreamingClientConn) Receive(any) error { return c.err }
