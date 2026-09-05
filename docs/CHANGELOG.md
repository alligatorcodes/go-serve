# Changelog

All notable changes to this project are documented here.

## [Unreleased]

### Added

- Added `docs/CONTRIBUTING.md` with architecture guidance, development workflow, testing expectations, security practices, troubleshooting, and pull-request checklist.
- Added request metrics and structured request logging, with `/metrics` and pprof exposed only through the authenticated control-plane listener.
- Added configuration fuzz coverage and an operations/security guide covering listener isolation, secret handling, release checks, and runtime probes.
- Added bounded idempotent-method retries and cooldown-based upstream circuit breakers; unsafe methods are never retried automatically.
- Added optional TLS termination with TLS 1.2 minimums and certificate/key reloads for new handshakes.
- Added active upstream health checks with healthy-endpoint selection and clean health-worker shutdown.
- Added systemd and Kubernetes deployment examples with non-root execution, probes, resource hardening, and graceful termination settings.
- Added TLS configuration and health failover tests.
- Protected the control-plane API, OpenAPI document, and documentation UI with the configured authentication middleware while leaving OAuth login/callback/logout endpoints reachable.
- Upgraded the local documentation page to render the served OpenAPI document without external runtime CDN dependencies.
- Implemented OAuth2/OIDC authentication offload with provider discovery, JWKS-backed token validation, authorization-code flow with PKCE, encrypted secure session cookies, login/callback/logout endpoints, bearer-token mode, scope authorization, and safe return-path handling.
- Added route-level authentication enforcement and trusted identity header propagation with client-supplied identity headers removed first.
- Added authentication tests for bearer verification, scope rejection, session expiry, encrypted cookies, safe redirects, and protected-route integration.
- Implemented the concurrent public data plane with host/path routing, longest-prefix matching, method restrictions, reverse proxying, upstream connection pooling, dial/request timeouts, and round-robin endpoint selection.
- Added bounded in-flight request and request-body limits plus a connection-limiting listener.
- Wired separate public and control-plane listeners into the shared graceful-shutdown lifecycle.
- Added data-plane tests for routing, proxy forwarding, method/path rejection, concurrent requests, body limits, and upstream timeouts.
- Implemented strict TOML file loading through the `-config` flag, including default preservation, unknown-key rejection, and duration parsing.
- Added immutable, deep-copied configuration snapshots for runtime state isolation.
- Implemented the control-plane configuration lifecycle with strict JSON decoding, validation, atomic replacement, ETag-based optimistic concurrency, redacted reads, and rollback.
- Added control-plane server and route status endpoints.
- Added served OpenAPI JSON and a configuration-gated Swagger UI at `/api/docs`.
- Added unit tests for bootstrap status, validation, replacement, version conflicts, rollback, secret redaction, UI gating, OpenAPI serving, invalid input, and concurrent status reads.
- Initialized the Go project skeleton with separate command, configuration, and control-plane packages.
- Added a runnable bootstrap control plane with graceful shutdown and configurable HTTP timeouts.
- Added bootstrap endpoints:
  - `GET /health`
  - `GET /ready`
  - `GET /api/v1/config/status`
- Added typed server configuration with defaults and validation for listeners, timeouts, authentication mode, control-plane UI settings, and resource limits.
- Added a TOML configuration example covering server, control plane, authentication, limits, routes, and upstreams.
- Added the versioned OpenAPI specification for the control-plane API, including configuration validation, replacement, rollback, status, server, route, health, readiness, and optional Swagger UI endpoints.
- Added configuration contract documentation describing parsing, validation, immutable activation, rollback, secret handling, and `control_plane.ui` behavior.
- Added initial CI checks for `go test ./...` and `go vet ./...`.

### Verification

- `go test ./...` passes.
- `go vet ./...` passes.
- Runtime smoke tests pass for `/health` and `/api/v1/config/status`.

### Not Yet Implemented

- Broader upstream resilience features such as outlier detection.
- Persistent/shared session storage for multi-instance deployments.
