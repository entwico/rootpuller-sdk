# rootpuller-sdk review profile

## What this software is

The Go client SDK for rootpuller-api: one hand-written facade package per gRPC
service (`chunker`, `painter`, `video`, `voice`, …), generated connect-go code
under `internal/gen/`, shared plumbing in `internal/{transport,streamio,apierr,
protoconv}`, and `rootpullertest/` — in-process fakes consumers test against.
Each package is `client.go` (Service, NewService, methods), `types.go` (options,
enums, results, proto conversion) and `client_test.go` against the fake.

## What a real failure looks like here

- A generated proto type in any exported signature (the facade contract;
  `task lint-no-gen-leak` enforces it — anything it would catch is a real bug).
- A wire-order violation in a client or a fake: params frame not first, files
  out of order, a half-close missing — the server rejects it at runtime only.
- Presence lost on an optional proto field: a `Get*()` on a pointer field, or a
  zero value sent where nil was meant. The `//nolint:protogetter // presence
  matters` markers are deliberate; "simplifying" one to a getter is a bug.
- An error that stops being a `*rootpullersdk.Error`, loses its code, or loses
  the RetryInfo hint — retry and backpressure logic hinges on them.
- A fake (`rootpullertest/`) that is more lenient than the real server: tests
  pass against it and the production call fails.
- A truncated stream treated as a result (missing metadata frame, file count
  mismatch) instead of an Internal/DataLoss error.

## Blast radius

Consumed by other teams' services and the showroom; released by git tag. A
breaking change in an exported signature or a behaviour change in a fake breaks
consumers at their next `go get`, not here.

## Reporting bar

- Majors: facade leaks, wire-order bugs, presence bugs, error-identity loss,
  fakes diverging from the server, goroutine/stream leaks (unclosed streams).
- Not findings: the 66 pre-existing lint findings (exhaustruct, goconst) the
  repo deliberately carries; style golangci-lint already enforces; `internal/gen/`
  (never reviewed); doc wording that matches the proto comments.

## Deliberate conventions

- Required inputs are positional, optional ones in a nil-able `*XxxOptions`;
  every method starts with `if opts == nil { opts = &XxxOptions{} }`.
- Enums are Go string types with proto maps; values newer than the SDK fall
  back to the proto identifier string, never an error.
- `OnProgress` callbacks are called synchronously from the receive loop.
- Comments start lowercase; only non-obvious code is commented. Tests use the
  std library style of this repo: plain `testing`, no testify in new packages
  that don't already use it.
