# rootpuller-sdk

Go client SDK for [rootpuller-api](https://github.com/entwico/rootpuller-api).
Wraps all 13 gRPC services behind an idiomatic, hand-written Go facade —
generated protobuf types appear in no exported signature, and all streaming
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

    rootpullersdk "github.com/entwico/rootpuller-sdk"
    "github.com/entwico/rootpuller-sdk/completion"
    "github.com/entwico/rootpuller-sdk/rerank"
)

// One globally configured SDK handle. OAuth via any standard
// oauth2.TokenSource:
oauth := &clientcredentials.Config{
    ClientID:     os.Getenv("CLIENT_ID"),
    ClientSecret: os.Getenv("CLIENT_SECRET"),
    TokenURL:     "https://auth.example.com/token",
}

sdk, err := rootpullersdk.New("http://rootpuller-api:8755",
    rootpullersdk.WithTokenSource(oauth.TokenSource(ctx)),
    rootpullersdk.WithOTel(),
    rootpullersdk.WithRetry(rootpullersdk.RetryOptions{}), // transient-aware retry
)
if err != nil { ... }

// Service clients are built from the handle, with construction-time
// defaults; call sites pass required inputs positionally and optional
// tuning in a nil-able Options struct:
reranker := rerank.NewService(sdk,
    rerank.WithDeployment("local"),
    rerank.WithDefaultModel("BAAI/bge-reranker-v2-m3"),
)
resp, err := reranker.Rerank(ctx, query, documents, nil)

llm := completion.NewService(sdk,
    completion.WithDefaultProvider(completion.ProviderGemini),
    completion.WithDefaultModel("gemini-2.5-pro"),
)
answer, err := llm.Complete(ctx,
    []completion.Message{completion.UserMessage("say hi")}, nil)

// Structured output: schema-enforced JSON straight into your type.
type Teaser struct{ Title, Text string }
teaser, _, err := completion.JSON[Teaser](ctx, llm,
    []completion.Message{completion.UserMessage(prompt)},
    &completion.Options{ResponseSchema: teaserSchema})
```

### Transport

The base URL scheme picks the transport:

- `http://host:8755` — cleartext HTTP/2 (h2c), matching the server's plain
  gRPC listener. This is the in-cluster default.
- `https://host` — TLS, for a terminating ingress. Use
  `rootpullersdk.WithInsecureTLS()` for self-signed certificates, or
  `rootpullersdk.WithTLSConfig(...)` for full control.

Under the hood the SDK is a [connect-go](https://connectrpc.com) client
speaking the gRPC protocol, so it talks to the unmodified server. A binary
links only the service packages it imports.

### Global options (`rootpullersdk.New`)

| Option | Purpose |
|---|---|
| `WithTokenSource(ts)` | OAuth bearer auth from any `oauth2.TokenSource` (cached via `ReuseTokenSource`) |
| `WithToken(s)` | Static bearer token |
| `WithRetry(RetryOptions{...})` | Retry transiently failing unary calls (exponential backoff + jitter, honors the server's RetryAfter hint); streams are never auto-retried |
| `WithTLSConfig` / `WithInsecureTLS` | TLS settings for `https` URLs |
| `WithOTel(...)` | OpenTelemetry tracing/metrics via otelconnect |
| `WithReadMaxBytes(n)` | Receive size cap (default 64 MiB) |
| `WithInterceptors(...)` | Custom connect interceptors (escape hatch) |
| `WithHTTPClient(hc)` | Full HTTP client override |

### Services

| Package | Service | Highlights |
|---|---|---|
| `chunker` | TextChunkerService | 8 chunking strategies; `ChunkToken(ctx, texts, opts)` |
| `embedding` | VectorEmbeddingService | `Embed`, `EmbedStream` (Go iterator), `ListModels` |
| `rerank` | RerankService | `Rerank(ctx, query, documents, opts)` |
| `completion` | CompletionService | `Complete`, `CompleteWithAttachments`, generic `JSON[T]` |
| `search` | SearchService | Brave/Serper web/news/image/video search |
| `chef` | DocumentProcessingService | text/table/markdown processing |
| `vectorops` | VectorOpsService | HDBSCAN + UMAP over streamed matrices |
| `webcontent` | WebContentService + ScrapeService | `Fetch(ctx, url, opts)`, `Crawl`, `MapURLs`, `OpenSession` (full-duplex) |
| `unshakaler` | UnshakalerService | `UpscaleImage(ctx, image, opts)` |
| `facefixer` | FaceFixerService | restore/colorize/inpaint faces |
| `bgremover` | BackgroundRemoverService | background removal |
| `painter` | ImagePainterService | Imagen 3 / gpt-image-1 generate/edit/outpaint |
| `assetia` | MediaProcessingService | image crop/resize/convert/watermark, video transcode (H.265/AV1), previews, `ProbeMedia` |

Method shape everywhere: **required inputs positional, optional tuning in
one trailing `*Options` (nil = all defaults)**. Construction-time
defaults (`WithDefaultModel`, `WithDefaultProvider`, ...) fill empty
Options fields; per-call values win.

Image services take `rootpullersdk.Upload` (streaming) and return
`rootpullersdk.File`; the SDK chunks uploads at 2 MiB and handles the
half-close-before-response protocol the server requires.

### Backpressure

The five rootpuller-backed services (chunker, embedding, rerank,
vectorops, chef) advertise their per-deployment capacity in-band
(rate-limit trailers, shed statuses with retry hints). A `Backpressure`
gate follows those signals — AIMD concurrency control, a shared shed
pause, a deep-outage circuit breaker — so batch workloads pace themselves
to the server. Create **one gate per deployment** and share it across the
routable services targeting it:

```go
bp := rootpullersdk.NewBackpressure(rootpullersdk.BackpressureOptions{SeedConcurrency: 6})
chk := chunker.NewService(sdk, chunker.WithDeployment("local"), chunker.WithBackpressure(bp))
emb := embedding.NewService(sdk, embedding.WithDeployment("local"), embedding.WithBackpressure(bp))
```

`WithBackpressure` exists only on the five services that emit the
signals. It composes with `WithRetry`: every retry attempt re-acquires a
slot and waits out the shared shed pause. Streams hold one slot for
their lifetime.

### Routing headers

Scoped to the services that understand them, as construction options:

- `rootpuller-deployment` (chunker, embedding, rerank, vectorops, chef):
  `chunker.WithDeployment("cloudrun")` etc.
- `rootpuller-bot` (webcontent, scrape): `webcontent.WithBot("crawler-a")`
  — one option accepted by both `NewService` and `NewScrapeService`.

Per-call overrides win: `rootpullersdk.ContextWithDeployment(ctx, "local")`,
`rootpullersdk.ContextWithBot(ctx, "crawler-b")`.

### Errors

Every RPC failure is a `*rootpullersdk.Error`. Match classes with
sentinels, or use the transient classifier:

```go
if rootpullersdk.IsTransient(err) { // Unavailable | DeadlineExceeded | ResourceExhausted | Aborted
    var ae *rootpullersdk.Error
    errors.As(err, &ae)
    time.Sleep(ae.RetryAfter) // server's google.rpc.RetryInfo hint
}
```

(Or let `WithRetry` do exactly this for you.) The webcontent/scrape
services additionally surface rich domain errors as
`*webcontent.ContentError` (`PAYWALL`, `BLOCKED_CLOUDFLARE`,
`FETCH_TIMEOUT`, … with `Retryable`/`RetryAfter`).

### Testing your code

`rootpullertest` runs an in-process fake rootpuller-api over the real wire
path (h2c + gRPC protocol) with facade-typed hooks:

```go
srv := rootpullertest.NewServer(t, &rootpullertest.Rerank{
    RerankFunc: func(query string, documents []string) ([]rerank.Result, error) { ... },
})
sdk, _ := rootpullersdk.New(srv.URL)
svc := rerank.NewService(sdk)
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

The generated stubs live in `internal/gen` and are committed. Run
`task check-drift` to regenerate from proto `main` and fail on any diff —
a facade compile failure after regenerating is the semantic drift alarm;
fix by updating the affected facade converters and committing. (There is
no CI at the moment, so run this before releasing.)

Integration smoke test against a live server:

```bash
ROOTPULLER_ADDR=http://localhost:8755 go test -tags integration ./integration
```
