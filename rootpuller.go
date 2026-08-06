// Package rootpuller is the entry point of the rootpuller-api Go SDK.
// New builds a Client from a base URL; per-service clients hang off it:
//
//	c, err := rootpuller.New("http://rootpuller-api:8755",
//	    rootpuller.WithTokenSource(tokenSource),
//	    rootpuller.WithDeployment("cloudrun"))
//	if err != nil { ... }
//	chunks, err := c.Chunker().ChunkToken(ctx, &chunker.TokenRequest{Texts: texts})
package rootpuller

import (
	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"

	"github.com/entwico/rootpuller-sdk/bgremover"
	"github.com/entwico/rootpuller-sdk/chef"
	"github.com/entwico/rootpuller-sdk/chunker"
	"github.com/entwico/rootpuller-sdk/completion"
	"github.com/entwico/rootpuller-sdk/embedding"
	"github.com/entwico/rootpuller-sdk/facefixer"
	"github.com/entwico/rootpuller-sdk/internal/transport"
	"github.com/entwico/rootpuller-sdk/painter"
	"github.com/entwico/rootpuller-sdk/rerank"
	"github.com/entwico/rootpuller-sdk/search"
	"github.com/entwico/rootpuller-sdk/unshakaler"
	"github.com/entwico/rootpuller-sdk/vectorops"
	"github.com/entwico/rootpuller-sdk/webcontent"
)

// Client aggregates one configured connection to rootpuller-api and the
// per-service clients built on it. It is safe for concurrent use.
type Client struct {
	core *transport.Core

	bgremover  *bgremover.Client
	chef       *chef.Client
	chunker    *chunker.Client
	facefixer  *facefixer.Client
	completion *completion.Client
	embedding  *embedding.Client
	painter    *painter.Client
	rerank     *rerank.Client
	scrape     *webcontent.ScrapeClient
	search     *search.Client
	unshakaler *unshakaler.Client
	vectorops  *vectorops.Client
	webcontent *webcontent.Client
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

	return &Client{
		core:       core,
		bgremover:  bgremover.NewFromCore(core),
		chef:       chef.NewFromCore(core),
		chunker:    chunker.NewFromCore(core),
		facefixer:  facefixer.NewFromCore(core),
		painter:    painter.NewFromCore(core),
		scrape:     webcontent.NewScrapeFromCore(core),
		completion: completion.NewFromCore(core),
		embedding:  embedding.NewFromCore(core),
		rerank:     rerank.NewFromCore(core),
		search:     search.NewFromCore(core),
		unshakaler: unshakaler.NewFromCore(core),
		vectorops:  vectorops.NewFromCore(core),
		webcontent: webcontent.NewFromCore(core),
	}, nil
}

// BgRemover returns the BackgroundRemoverService client.
func (c *Client) BgRemover() *bgremover.Client { return c.bgremover }

// Chef returns the DocumentProcessingService client.
func (c *Client) Chef() *chef.Client { return c.chef }

// FaceFixer returns the FaceFixerService client.
func (c *Client) FaceFixer() *facefixer.Client { return c.facefixer }

// Chunker returns the TextChunkerService client.
func (c *Client) Chunker() *chunker.Client { return c.chunker }

// Completion returns the CompletionService client.
func (c *Client) Completion() *completion.Client { return c.completion }

// Embedding returns the VectorEmbeddingService client.
func (c *Client) Embedding() *embedding.Client { return c.embedding }

// Painter returns the ImagePainterService client.
func (c *Client) Painter() *painter.Client { return c.painter }

// Rerank returns the RerankService client.
func (c *Client) Rerank() *rerank.Client { return c.rerank }

// Scrape returns the ScrapeService client.
func (c *Client) Scrape() *webcontent.ScrapeClient { return c.scrape }

// Search returns the SearchService client.
func (c *Client) Search() *search.Client { return c.search }

// Unshakaler returns the UnshakalerService client.
func (c *Client) Unshakaler() *unshakaler.Client { return c.unshakaler }

// VectorOps returns the VectorOpsService client.
func (c *Client) VectorOps() *vectorops.Client { return c.vectorops }

// WebContent returns the WebContentService client.
func (c *Client) WebContent() *webcontent.Client { return c.webcontent }
