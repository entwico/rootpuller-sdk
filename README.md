# rootpuller-sdk

Go client SDK for [rootpuller-api](https://github.com/entwico/rootpuller-api).
Wraps all 13 gRPC services behind an idiomatic, hand-written Go facade —
generated protobuf types never appear in the public API, and all streaming
wire protocols (chunked uploads, event streams, full-duplex sessions) are
encapsulated.

## Install

```bash
go get github.com/entwico/rootpuller-sdk
```

## Quick start

```go
import (
    "golang.org/x/oauth2/clientcredentials"

    rootpuller "github.com/entwico/rootpuller-sdk"
    "github.com/entwico/rootpuller-sdk/chunker"
    "github.com/entwico/rootpuller-sdk/embedding"
)

// OAuth via any standard oauth2.TokenSource:
oauth := &clientcredentials.Config{
    ClientID:     os.Getenv("CLIENT_ID"),
    ClientSecret: os.Getenv("CLIENT_SECRET"),
    TokenURL:     "https://auth.example.com/token",
}

c, err := rootpuller.New("http://rootpuller-api:8755",
    rootpuller.WithTokenSource(oauth.TokenSource(ctx)),
)
if err != nil { ... }

chunks, err := c.Chunker().ChunkToken(ctx, &chunker.TokenRequest{
    Texts:     []string{text},
    Tokenizer: chunker.TokenizerGPT2,
    ChunkSize: rootpuller.Ptr(512),
})

resp, err := c.Embedding().Embed(ctx, &embedding.Request{
    Inputs: embedding.Text("hello world"),
    Model:  embedding.ModelRef{Backend: embedding.BackendLocal, ModelID: "bge-m3"},
})
```

### Transport

The base URL scheme picks the transport:

- `http://host:8755` — cleartext HTTP/2 (h2c), matching the server's plain
  gRPC listener. This is the in-cluster default.
- `https://host` — TLS, for a terminating ingress. Use
  `rootpuller.WithInsecureTLS()` for self-signed certificates, or
  `rootpuller.WithTLSConfig(...)` for full control.

Under the hood the SDK is a [connect-go](https://connectrpc.com) client
speaking the gRPC protocol, so it talks to the unmodified server.

### Options

| Option | Purpose |
|---|---|
| `WithTokenSource(ts)` | OAuth bearer auth from any `oauth2.TokenSource` (cached via `ReuseTokenSource`) |
| `WithToken(s)` | Static bearer token |
| `WithTLSConfig` / `WithInsecureTLS` | TLS settings for `https` URLs |
| `WithOTel(...)` | OpenTelemetry tracing/metrics via otelconnect |
| `WithReadMaxBytes(n)` | Receive size cap (default 64 MiB) |
| `WithInterceptors(...)` | Custom connect interceptors (escape hatch) |
| `WithHTTPClient(hc)` | Full HTTP client override |

### Routing headers

The two routing headers are scoped to the services that understand them,
so they live on the service clients rather than on `rootpuller.New`:

- `rootpuller-deployment` (chunker, embedding, vectorops — selects the
  backend deployment, e.g. `"local"` / `"cloudrun"`):
  `c.Chunker().WithDeployment("cloudrun")`, likewise on `Embedding()`
  and `VectorOps()`.
- `rootpuller-bot` (webcontent, scrape — selects the bot identity):
  `c.WebContent().WithBot("crawler-a")`, `c.Scrape().WithBot(...)`.

`WithDeployment`/`WithBot` return cheap derived clients, so a default can
be baked once and reused. Per-call overrides win over the client default:
`rootpuller.ContextWithDeployment(ctx, "local")`,
`rootpuller.ContextWithBot(ctx, "crawler-b")`.

### Services

| Accessor | Service | Highlights |
|---|---|---|
| `c.Chunker()` | TextChunkerService | 8 chunking strategies, `[][]common.TextChunk` |
| `c.Embedding()` | VectorEmbeddingService | `Embed`, `EmbedStream` (Go 1.23 iterator), `ListModels` |
| `c.Rerank()` | RerankService | `Rerank`, `ListModels` |
| `c.Completion()` | CompletionService | `Complete`, `CompleteWithAttachments` (streamed uploads ≤64 MiB) |
| `c.Search()` | SearchService | Brave/Serper web/news/image/video search |
| `c.Chef()` | DocumentProcessingService | text/table/markdown processing, tokenizer discovery |
| `c.VectorOps()` | VectorOpsService | HDBSCAN clustering, UMAP projection over streamed matrices |
| `c.WebContent()` | WebContentService | `Fetch`/`FetchURL` event streams, `ExtractContent` |
| `c.Scrape()` | ScrapeService | `Crawl`/`CrawlPages`, `MapURLs`, `OpenSession` (full-duplex) |
| `c.Unshakaler()` | UnshakalerService | image upscaling |
| `c.FaceFixer()` | FaceFixerService | restore/colorize/inpaint faces |
| `c.BgRemover()` | BackgroundRemoverService | background removal |
| `c.Painter()` | ImagePainterService | Imagen 3 / gpt-image-1 generate/edit/outpaint |

All image services take `common.Upload` (streaming) and return
`common.File`; the SDK chunks uploads at 2 MiB and handles the
half-close-before-response protocol the server requires.

### Errors

Every RPC failure is an `*apierror.Error`. Match classes with sentinels:

```go
if errors.Is(err, apierror.ErrResourceExhausted) {
    var ae *apierror.Error
    errors.As(err, &ae)
    time.Sleep(ae.RetryAfter) // server's google.rpc.RetryInfo hint
}
```

The webcontent/scrape services additionally surface rich domain errors as
`*webcontent.ContentError` (`PAYWALL`, `BLOCKED_CLOUDFLARE`,
`FETCH_TIMEOUT`, … with `Retryable`/`RetryAfter`).

### Testing your code

`rootpullertest` runs an in-process fake rootpuller-api over the real wire
path (h2c + gRPC protocol) with facade-typed hooks:

```go
srv := rootpullertest.NewServer(t, &rootpullertest.Chunker{
    ChunkFunc: func(method string, texts []string) ([][]common.TextChunk, error) { ... },
})
c, _ := rootpuller.New(srv.URL)
```

## Development

Requires Go 1.26+, [Task](https://taskfile.dev/), golangci-lint, and SSH
read access to `entwico/rootpuller-proto` (codegen only).

```bash
task generate     # regenerate stubs from rootpuller-proto@main
task build
task test         # -race + coverage
task lint         # golangci-lint + facade no-gen-leak check
task check-drift  # regenerate and fail on diff (CI)
```

The generated stubs live in `internal/gen` and are committed; CI
regenerates nightly from proto `main` and fails on drift — a facade
compile failure is the semantic drift alarm. Fix by running
`task generate`, updating the affected facade converters, and committing.

Integration smoke test against a live server:

```bash
ROOTPULLER_ADDR=http://localhost:8755 go test -tags integration ./integration
```
