// Package rootpullersdk is the entry point of the rootpuller-api Go SDK.
// New builds the globally configured SDK handle; each service package
// constructs its client from it:
//
//	sdk, err := rootpullersdk.New("http://rootpuller-api:8755",
//	    rootpullersdk.WithTokenSource(tokenSource))
//	if err != nil { ... }
//	svc := rerank.NewService(sdk, rerank.WithDeployment("local"))
//	results, err := svc.Rerank(ctx, query, documents, nil)
//
// The root package also holds the types shared across services (File,
// Upload, Usage, TextChunk, ...) and the error model (Error, the Err*
// sentinels, IsTransient).
package rootpullersdk

import (
	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"

	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Client is the configured connection to one rootpuller-api instance:
// transport, auth, and global call options. Pass it to the service
// constructors (rerank.NewService, chunker.NewService, ...). It is safe
// for concurrent use.
type Client struct {
	core *transport.Core
}

// New builds a Client for the rootpuller-api instance at baseURL.
//
// The URL scheme selects the transport: "http://host:8755" speaks
// cleartext HTTP/2 (h2c) — the in-cluster default, matching the server's
// plaintext gRPC listener — while "https://host" negotiates TLS (see
// WithTLSConfig and WithInsecureTLS for self-signed certificates).
func New(baseURL string, opts ...Option) (*Client, error) {
	cfg := &config{readMaxBytes: DefaultReadMaxBytes}
	for _, opt := range opts {
		opt(cfg)
	}

	httpClient := cfg.httpClient
	if httpClient == nil {
		var err error

		httpClient, err = transport.NewHTTPClient(baseURL, cfg.tlsConfig)
		if err != nil {
			return nil, err
		}
	}

	interceptors := []connect.Interceptor{transport.NewHeadersInterceptor()}
	if cfg.tokenSource != nil {
		interceptors = append(interceptors, transport.NewAuthInterceptor(cfg.tokenSource))
	}

	if cfg.retry != nil {
		interceptors = append(interceptors, transport.NewRetryInterceptor(*cfg.retry))
	}

	if cfg.otelEnabled {
		otelInterceptor, err := otelconnect.NewInterceptor(cfg.otelOptions...)
		if err != nil {
			return nil, err
		}

		interceptors = append(interceptors, otelInterceptor)
	}

	interceptors = append(interceptors, cfg.userInterceptors...)

	core := &transport.Core{
		HTTPClient: httpClient,
		BaseURL:    baseURL,
		ClientOpts: []connect.ClientOption{
			connect.WithGRPC(),
			connect.WithReadMaxBytes(cfg.readMaxBytes),
			connect.WithInterceptors(interceptors...),
		},
	}

	return &Client{core: core}, nil
}

// TransportCore exposes the connection internals to the SDK's own
// service packages. The returned type lives in an internal package, so
// code outside this module cannot do anything with it — treat this
// method as SDK-internal.
func (c *Client) TransportCore() *transport.Core { return c.core }
