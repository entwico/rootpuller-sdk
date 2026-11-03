---
bump: minor
---

painter: `GenerateComfy` and `EditComfy` select the self-hosted ComfyUI models (`ComfyModelQwenImage`, `ComfyModelMOSSImage`, `ComfyModelKrea2Turbo`); `GenerateOptions.References` sends ordered reference images (`<image1>`, `<image2>`, …); `painter.WithComfyUIDeployment` and `rootpullersdk.ContextWithComfyUIDeployment` pick the ComfyUI deployment; `rootpullertest.Painter.GenerateRequestFunc` exposes references and the deployment header to tests
