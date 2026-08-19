# Changelog

## v0.3.0 (2026-08-19)

### Minor Changes

- New `escriba` package wrapping TranscriptionService: `OpenLive` for full-duplex speech to text (send PCM, read partial/committed/revision events, finish with an authoritative transcript), `Transcribe` for a complete recording, and `GetCapabilities` for the served model and session limits. `rootpullertest.Escriba` is the matching protocol-strict fake. Deployment-routable: `escriba.WithDeployment(name)` pins a service client at one worker, and `rootpullersdk.ContextWithEscribaDeployment(ctx, name)` overrides it per call — enough to send the same audio at a CPU deployment in one cluster and a GPU deployment in another from a single client.

## v0.2.0 (2026-08-09)

### Minor Changes

- Honor HTTP_PROXY/HTTPS_PROXY/NO_PROXY: https via standard transport proxying, h2c via an explicit CONNECT tunnel so gRPC streams work through HTTP proxies

## v0.1.0 (2026-08-07)

### Minor Changes

- Initial release: idiomatic Go client for all 14 rootpuller-api services with encapsulated streaming, OAuth, retry, per-deployment backpressure, and in-process test fakes (rootpullertest)
