---
bump: minor
---

New `escriba` package wrapping TranscriptionService: `OpenLive` for full-duplex
speech to text (send PCM, read partial/committed/revision events, finish with an
authoritative transcript), `Transcribe` for a complete recording, and
`GetCapabilities` for the served model and session limits. `rootpullertest.Escriba`
is the matching protocol-strict fake.

Deployment-routable: `escriba.WithDeployment(name)` pins a service client at one
worker, and `rootpullersdk.ContextWithEscribaDeployment(ctx, name)` overrides it
per call — enough to send the same audio at a CPU deployment in one cluster and a
GPU deployment in another from a single client.
