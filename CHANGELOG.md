# Changelog

## v0.2.0 (2026-08-09)

### Minor Changes

- Honor HTTP_PROXY/HTTPS_PROXY/NO_PROXY: https via standard transport proxying, h2c via an explicit CONNECT tunnel so gRPC streams work through HTTP proxies

## v0.1.0 (2026-08-07)

### Minor Changes

- Initial release: idiomatic Go client for all 14 rootpuller-api services with encapsulated streaming, OAuth, retry, per-deployment backpressure, and in-process test fakes (rootpullertest)
