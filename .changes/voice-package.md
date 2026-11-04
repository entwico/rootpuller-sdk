---
bump: minor
---

voice: new package wrapping `VoiceService` — `Design` creates previews of synthetic voices from a description, `Save` turns a preview handle or sample audio into an opaque `voice.Ref`, `Speak` says a text in it (`Usage.Characters` reports what the provider billed), `Delete` frees the voice's slot and `GetCapabilities` reports what the provider allows. A `Ref` from a provider the server no longer uses fails, and `voice.IsProviderMismatch` recognises it: save the stored sample again. `video.Service.GenerateTalkingSpeech` lip-syncs a portrait to a text spoken in a saved voice (new `video.StageSpeaking`). `rootpullertest.Voice` fakes the service, and `rootpullertest.Video` accepts speech. See `docs/voice.md`.
