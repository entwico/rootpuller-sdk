package rootpullersdk

import (
	"crypto/tls"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"golang.org/x/oauth2"

	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// DefaultReadMaxBytes caps the size of a single received message. The
// server chunks large payloads at 2 MiB, so this is headroom for big unary
// responses, not a streaming requirement.
const DefaultReadMaxBytes = 64 * 1024 * 1024

type config struct {
	tokenSource      oauth2.TokenSource
	httpClient       *http.Client
	tlsConfig        *tls.Config
	otelEnabled      bool
	otelOptions      []otelconnect.Option
	readMaxBytes     int
	retry            *transport.RetryConfig
	userInterceptors []connect.Interceptor
}

// Option configures the Client returned by New.
type Option func(*config)

// WithTokenSource authenticates every call with OAuth bearer tokens from
// ts. The source is wrapped in oauth2.ReuseTokenSource, so tokens are
// cached and refreshed only on expiry; any standard-library-compatible
// source (client-credentials config, google.DefaultTokenSource, ...) works.
func WithTokenSource(ts oauth2.TokenSource) Option {
	return func(c *config) { c.tokenSource = oauth2.ReuseTokenSource(nil, ts) }
}

// WithToken authenticates every call with a fixed bearer token.
func WithToken(token string) Option {
	return func(c *config) {
		c.tokenSource = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	}
}

// WithHTTPClient replaces the scheme-derived HTTP client entirely. The
// caller is then responsible for HTTP/2 support (h2c for http:// URLs).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *config) { c.httpClient = hc }
}

// WithTLSConfig sets the TLS configuration used for https base URLs.
func WithTLSConfig(cfg *tls.Config) Option {
	return func(c *config) { c.tlsConfig = cfg }
}

// WithInsecureTLS accepts any server certificate on https base URLs —
// for self-signed in-cluster endpoints. Do not use across trust
// boundaries.
func WithInsecureTLS() Option {
	return func(c *config) { c.tlsConfig = &tls.Config{InsecureSkipVerify: true} } //nolint:gosec // opt-in by name
}

// WithOTel instruments every call with OpenTelemetry traces and metrics
// via otelconnect.
func WithOTel(opts ...otelconnect.Option) Option {
	return func(c *config) {
		c.otelEnabled = true
		c.otelOptions = opts
	}
}

// WithReadMaxBytes overrides DefaultReadMaxBytes for received messages.
func WithReadMaxBytes(n int) Option {
	return func(c *config) { c.readMaxBytes = n }
}

// RetryOptions bounds the retry loop enabled by WithRetry. Zero fields
// take the defaults noted per field.
type RetryOptions struct {
	// MaxAttempts is the total number of tries including the first
	// (default 4).
	MaxAttempts int
	// MaxElapsed caps the whole retry budget including waits (default
	// 20s).
	MaxElapsed time.Duration
	// InitialInterval is the first backoff delay, doubling per attempt
	// with jitter (default 500ms). The server's RetryAfter hint
	// overrides the schedule when present.
	InitialInterval time.Duration
}

// WithRetry retries transiently failing unary calls (IsTransient:
// Unavailable, DeadlineExceeded, ResourceExhausted, Aborted) with
// exponential backoff, honoring the server's retry hints. Streaming
// calls are never retried automatically.
func WithRetry(opts RetryOptions) Option {
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 4
	}

	if opts.MaxElapsed <= 0 {
		opts.MaxElapsed = 20 * time.Second
	}

	if opts.InitialInterval <= 0 {
		opts.InitialInterval = 500 * time.Millisecond
	}

	return func(c *config) {
		c.retry = &transport.RetryConfig{
			MaxAttempts:     opts.MaxAttempts,
			MaxElapsed:      opts.MaxElapsed,
			InitialInterval: opts.InitialInterval,
		}
	}
}

// WithInterceptors appends custom connect interceptors after the SDK's
// own (headers, auth, otel). An escape hatch for logging, retries, or
// custom metadata.
func WithInterceptors(interceptors ...connect.Interceptor) Option {
	return func(c *config) { c.userInterceptors = append(c.userInterceptors, interceptors...) }
}
