---
bump: minor
---

New `decision` package wrapping DecisionService: `Decide(ctx, state, questions, opts)` asks typed choice, score and noul questions about a string or JSON state and returns calibrated probabilities per answer, backed by Laya (local, the gateway default) or TypeSafe Jev (hosted), selected via `ModelRef.Provider`; `ListModels` lists the available models with their limits. Deployment-routable and backpressure-aware like rerank. `rootpullertest.Decision` provides a fake for consumer tests.
