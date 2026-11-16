---
bump: minor
---

decision: `Options.Images` sends images with a request (inline, or streamed through `DecideUpload` above `MaxInlineImageBytes`), `ImageRef` mints a reference for one, `ProviderSelfHosted` reaches the cluster's self-hosted models and `ModelInfo.SupportsImages` tells which models take images; the `rootpullertest` fake gains `DecideImagesFunc` and `DecideUpload`
