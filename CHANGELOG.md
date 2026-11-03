# Changelog

## v0.8.0 (2026-11-03)

### Minor Changes

- video: new package wrapping `VideoGeneratorService` — `Generate` renders clips with sound (`ModelLTX25`) from text, `FirstFrame`, or `FirstFrame` + `LastFrame`; `GenerateTalking` lip-syncs a portrait to a voice track (`ModelInfiniteTalk`); `video.WithComfyUIDeployment` and `rootpullersdk.ContextWithComfyUIDeployment` pick the deployment; `rootpullertest.Video` fakes the service and exposes the received frames, audio and deployment header

## v0.7.0 (2026-11-03)

### Minor Changes

- painter: `GenerateComfy` and `EditComfy` select the self-hosted ComfyUI models (`ComfyModelQwenImage`, `ComfyModelMOSSImage`, `ComfyModelKrea2Turbo`); `GenerateOptions.References` sends ordered reference images (`<image1>`, `<image2>`, …); `painter.WithComfyUIDeployment` and `rootpullersdk.ContextWithComfyUIDeployment` pick the ComfyUI deployment; `rootpullertest.Painter.GenerateRequestFunc` exposes references and the deployment header to tests

## v0.6.0 (2026-10-16)

### Minor Changes

- composer: new package wrapping ComposerService — Plan, ComposeSong (multi-take, decoder choice, sampling overrides, instrumental), Transcribe and GetCapabilities, plus the rootpullertest.Composer fake

## v0.5.0 (2026-09-23)

### Minor Changes

- New `decision` package wrapping DecisionService: `Decide(ctx, state, questions, opts)` asks typed choice, score and noul questions about a string or JSON state and returns calibrated probabilities per answer, backed by Laya (local, the gateway default) or TypeSafe Jev (hosted), selected via `ModelRef.Provider`; `ListModels` lists the available models with their limits. Deployment-routable and backpressure-aware like rerank. `rootpullertest.Decision` provides a fake for consumer tests.

## v0.4.0 (2026-09-19)

### Minor Changes

- escriba.TranscribeRecording: transcribe a recording of any length, with optional speaker labels, OnProgress and OnSegment
- rootpullertest.Escriba fakes TranscribeRecording via RecordingFunc
- escriba.Capabilities reports MaxLongRecording, MaxRecordingBytes and SpeakerMethods

## v0.3.0 (2026-08-19)

### Minor Changes

- New `escriba` package wrapping TranscriptionService: `OpenLive` for full-duplex speech to text (send PCM, read partial/committed/revision events, finish with an authoritative transcript), `Transcribe` for a complete recording, and `GetCapabilities` for the served model and session limits. `rootpullertest.Escriba` is the matching protocol-strict fake. Deployment-routable: `escriba.WithDeployment(name)` pins a service client at one worker, and `rootpullersdk.ContextWithEscribaDeployment(ctx, name)` overrides it per call — enough to send the same audio at a CPU deployment in one cluster and a GPU deployment in another from a single client.

## v0.2.0 (2026-08-09)

### Minor Changes

- Honor HTTP_PROXY/HTTPS_PROXY/NO_PROXY: https via standard transport proxying, h2c via an explicit CONNECT tunnel so gRPC streams work through HTTP proxies

## v0.1.0 (2026-08-07)

### Minor Changes

- Initial release: idiomatic Go client for all 14 rootpuller-api services with encapsulated streaming, OAuth, retry, per-deployment backpressure, and in-process test fakes (rootpullertest)
