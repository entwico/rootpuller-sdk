# Changelog

## v0.12.0 (2026-11-05)

### Minor Changes

- voice: `SpeakOptions.Format` picks the encoding of the speech (MP3 or lossless WAV, at the sample rates and bit rates in `Capabilities.AudioFormats`); `Speech.Format` reports what was delivered. montage: `Output.AudioBitrate` overrides the quality's AAC bit rate, `Metadata` reports the delivered audio (sample rate, channels, bit rate), and the quality levels are documented. The fakes record and enforce both.

## v0.11.0 (2026-11-05)

### Minor Changes

- New `montage` package for `MontageService`: `Render` turns a `Timeline` of video segments and stills (cuts or crossfades, kept, ducked or muted clip sound), audio tracks (voice-overs, looping music beds, fades) and image overlays (captions, logos) plus the assets it names into one MP4, with progress and the result's duration and size; `GetCapabilities` reports the server's limits. `rootpullertest.Montage` fakes it, enforcing the wire protocol and the timeline rules that need no media. See docs/montage.md.

### Patch Changes

- A streaming upload that fails part-way (its reader returns an error, or a frame cannot be sent) now cancels the call instead of ending it cleanly. Before, the server received a well-formed but truncated upload and processed it: a talking-head video could start rendering from half a voice track. This covers the uploads that stream params, then files, then read results: painter (`GenerateImage`, `EditImage`, `OutpaintImage`, `RefineFaces`), video (`Generate`, `GenerateTalking`, `GenerateTalkingSpeech`), assetia (`TransformImage`, `TransformVideo`, `GeneratePreview`), `bgremover.RemoveBackground`, `unshakaler.UpscaleImage`, facefixer (`RestoreFace`, `ColorizeFace`, `InpaintFace`), `webcontent.ExtractContent`, `composer.Transcribe`, `escriba.TranscribeRecording`, vectorops (`ClusterHdbscan`, `ProjectUmap`) and `montage.Render`. `assetia.ProbeMedia`, `escriba.Transcribe` and `completion.CompleteWithAttachments` are not covered yet.

## v0.10.1 (2026-11-04)

### Patch Changes

- voice: `Save` no longer lets the server save a voice when a frame fails to send on the client side (a cancelled context, a name or sample name that is not valid UTF-8): a failed sample chunk aborts the call instead of closing it cleanly, so no slot is taken by a voice whose `Ref` would be lost, and a params frame that cannot be sent reports its own cause instead of the server's "missing params". `rootpullertest.Voice` now rejects what the server rejects: language codes other than lowercase ISO 639-1/639-3, whitespace-only texts and names, names over 100 characters, descriptions and texts over 40,000 characters, empty samples, samples over 64 MiB, previews outside 1-4, empty model overrides and speed or stability outside 0-1. `rootpullertest.Video` checks a talking video's speech the same way and rejects a blank model or prompt on both RPCs.

## v0.10.0 (2026-11-04)

### Minor Changes

- voice: new package wrapping `VoiceService` — `Design` creates previews of synthetic voices from a description, `Save` turns a preview handle or sample audio into an opaque `voice.Ref`, `Speak` says a text in it (`Usage.Characters` reports what the provider billed), `Delete` frees the voice's slot and `GetCapabilities` reports what the provider allows. A `Ref` from a provider the server no longer uses fails, and `voice.IsProviderMismatch` recognises it: save the stored sample again. `video.Service.GenerateTalkingSpeech` lip-syncs a portrait to a text spoken in a saved voice (new `video.StageSpeaking`). `rootpullertest.Voice` fakes the service, and `rootpullertest.Video` accepts speech. See `docs/voice.md`.

## v0.9.0 (2026-11-04)

### Minor Changes

- `painter.Service.RefineFaces` redraws small faces inside an image at high resolution on the comfy backend (`RefineFacesComfy`, Qwen) and blends them back, leaving the rest of the image as it was (bit-identical for 8-bit PNG). References carry the identity, `SearchRegions` or `FaceBoxes` choose the faces, and `Metadata.Faces` reports the refined boxes. `rootpullertest.Painter` gains `RefineFacesFunc`.

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
