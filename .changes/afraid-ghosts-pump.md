---
bump: minor
---

painter: `GenerateComfy` and `EditComfy` take `NegativePrompt` and `GuidanceScale` (1-10) on the models that sample with guidance (qwen-image-2.1, whose default is 1, and moss-image, default 3); a negative prompt needs guidance above 1. The fake records both, adds `EditRequestFunc`, and applies the server's rules.
