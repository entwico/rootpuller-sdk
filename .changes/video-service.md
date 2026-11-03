---
bump: minor
---

video: new package wrapping `VideoGeneratorService` — `Generate` renders clips with sound (`ModelLTX25`) from text, `FirstFrame`, or `FirstFrame` + `LastFrame`; `GenerateTalking` lip-syncs a portrait to a voice track (`ModelInfiniteTalk`); `video.WithComfyUIDeployment` and `rootpullersdk.ContextWithComfyUIDeployment` pick the deployment; `rootpullertest.Video` fakes the service and exposes the received frames, audio and deployment header
