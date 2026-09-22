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
| `decision` | DecisionService | `Decide(ctx, state, questions, opts)` typed choice/score/noul answers with calibrated probabilities, Laya (local) or Jev (hosted); `ListModels` |
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
| `escriba` | TranscriptionService | `OpenLive` (full-duplex speech to text), `Transcribe` (short clips), `TranscribeRecording` (any length, optional speaker labels, progress), `GetCapabilities`; deployment-routable |

### Decisions

`decision` asks typed questions about a state and returns calibrated
probabilities in one forward pass, no generated text. The gateway answers
with Laya (open weights, local worker) by default; pick Jev (TypeSafe,
hosted) per client or per call:

```go
decider := decision.NewService(sdk, decision.WithDeployment("local"))
resp, err := decider.Decide(ctx,
    ticket, // a string, or anything that marshals to a JSON object/array
    map[string]decision.Question{
        "department": decision.Choice("Which team should handle this?", map[string]string{
            "billing":   "payments, invoices, refunds",
            "technical": "bugs, outages, errors",
        }),
        "severity": decision.Score("How severe is it?", "minor", "moderate", "critical"),
        "urgent":   decision.Noul("Does the message convey urgency?"),
    },
    &decision.Options{Model: decision.ModelRef{Provider: decision.ProviderJev}},
)
dep := resp.Answers["department"].Choice // .Choice, .Probabilities, .Confidence
```

### Live transcription

`escriba.OpenLive` returns a session you feed audio to while reading results:

```go
session, err := svc.OpenLive(ctx, escriba.LiveConfig{SampleRate: 16000, Language: "de"})
defer session.Close()

go func() {
    for chunk := range microphone {   // ~100-250 ms of s16le mono PCM each
        session.Send(chunk)
    }
    session.CloseSend()               // the speaker has finished
}()

for event, err := range session.Events(ctx) {
    switch event.Kind {
    case escriba.EventKindPartial:    // provisional — overwrite, never append
    case escriba.EventKindCommitted:  // settled — append, grouped by Utterance
    case escriba.EventKindRevision:   // replaces that utterance entirely
    case escriba.EventKindComplete:   // authoritative transcript
    }
}
```

Two events carry different guarantees. `Partial` is the model's current guess
at the tail and must be replaced wholesale; `Committed` is settled and never
retracted. After an utterance closes, a `Revision` may supersede it with a
higher-quality re-decode, so keep committed text grouped by utterance index
rather than as one flat string. Revisions arrive out of band and are
best-effort: a consumer that ignores them still shows correct, if slightly
worse, text. `CloseSend` does not end the session — keep reading until
`Complete`, which is the transcript worth persisting.

`escriba.TranscribeRecording` takes a complete recording of any length — video
containers included — and returns when it is done. The call is the job: there is
nothing to poll, and cancelling `ctx` cancels the work on the server.

```go
recording, err := svc.TranscribeRecording(ctx, rootpullersdk.Upload{Name: "call.m4a", Content: file},
    &escriba.RecordingOptions{
        Speakers:   &escriba.SpeakerOptions{Count: 2}, // nil for a plain transcript
        OnProgress: func(p escriba.RecordingProgress) { log.Printf("%s %.0f%%", p.Stage, p.Percentage) },
    })
if err != nil { ... }

for _, turn := range recording.Turns() { // consecutive segments of one speaker, merged
    fmt.Printf("[%s] Speaker %d: %s\n", turn.Start, *turn.Speaker+1, turn.Text)
}
```

The upload streams, so memory stays flat however large the file. A recording can
take minutes and may first queue behind another (`RecordingStageQueued`): the
server gives live sessions priority. Speakers are told apart, not identified —
an index numbered by first appearance. `SpeakerMethodChannel` is exact and free
for dual-channel call recordings; `SpeakerMethodDiarization` works on any audio
but only on deployments that list it in `Capabilities.SpeakerMethods`
(`CodeUnimplemented` otherwise). `Recording.Segments` is the subtitle-shaped
view, `Recording.Turns()` the dialogue-shaped one. A full queue is
`CodeResourceExhausted` with a retry hint; `CodeUnavailable` means the server
lost the recording mid-flight and it has to be uploaded again.

Method shape everywhere: **required inputs positional, optional tuning in
one trailing `*Options` (nil = all defaults)**. Construction-time
defaults (`WithDefaultModel`, `WithDefaultProvider`, ...) fill empty
Options fields; per-call values win.

Image services take `rootpullersdk.Upload` (streaming) and return
`rootpullersdk.File`; the SDK chunks uploads at 2 MiB and handles the
half-close-before-response protocol the server requires.

### Backpressure

The six rootpuller-backed services (chunker, embedding, rerank,
decision, vectorops, chef) advertise their per-deployment capacity in-band
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

`WithBackpressure` exists only on the six services that emit the
signals. It composes with `WithRetry`: every retry attempt re-acquires a
slot and waits out the shared shed pause. Streams hold one slot for
their lifetime.

### Routing headers

Scoped to the services that understand them, as construction options:

- `rootpuller-deployment` (chunker, embedding, rerank, decision, vectorops,
  chef; for decision only the local Laya provider is routed):
  `chunker.WithDeployment("cloudrun")` etc.
- `rootpuller-bot` (webcontent, scrape): `webcontent.WithBot("crawler-a")`
  — one option accepted by both `NewService` and `NewScrapeService`. This
  selects a server-side crawler identity, not just routing — see the next
  section.

Per-call overrides win: `rootpullersdk.ContextWithDeployment(ctx, "local")`,
`rootpullersdk.ContextWithBot(ctx, "crawler-b")`.

### Crawler identity (Web Bot Auth)

The webcontent/scrape services are designed to fetch as a **named,
verifiable crawler**, not anonymously. `webcontent.WithBot("crawler-a")`
sends only the identity's name; everything else is resolved server-side
in rootpuller-api, where the identity lives:

- a per-bot **User-Agent** identifying the crawler on every request;
- **Web Bot Auth request signing** — [RFC 9421](https://www.rfc-editor.org/rfc/rfc9421)
  HTTP Message Signatures (Ed25519) on interactive fetch sessions, with
  the public key published as a JWKS under the bot's
  `/.well-known/http-message-signatures-directory` so origins and CDNs
  can cryptographically verify who is fetching;
- optional server-enforced **robots.txt compliance**: a bot configured
  with `obeyRobots` has it stamped onto session fetches server-side.

The SDK never carries keys or User-Agent strings, and a call without a
bot selected is a plain unsigned fetch. Note that on the `Fetch`/`Crawl`
paths robots.txt handling is client-controlled: the server default is
**off**, so set `FetcherOptions.ObeyRobotsTxt` /
`CrawlRules.ObeyRobotsTxt` explicitly when a workload must honor
robots.txt (for AI-ingestion pipelines, honoring machine-readable
opt-outs is what keeps you inside the EU text-and-data-mining exception).

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
