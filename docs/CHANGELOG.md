# Changelog

All notable changes to this project are documented here.

## [Unreleased]

### Added

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

- Public data-plane listener and reverse proxying.
- OAuth2/OIDC middleware and bearer-token validation.
- Atomic runtime configuration activation and rollback.
- OpenAPI document and Swagger UI serving.
