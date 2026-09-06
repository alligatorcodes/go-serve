# Production HTTP Server Plan

## Goals

Build a production-ready Go HTTP server with:

- A high-throughput data plane similar in role to NGINX or Envoy.
- A separately protected control-plane API for configuration and operations.
- OAuth2/OIDC authentication offload for protected client traffic.
- An optional OpenAPI/Swagger UI for the control plane, enabled with `ui = true`.
- Safe handling of many concurrent client connections.
- Atomic configuration changes, observability, graceful shutdown, and operational safety.

The first release should favor a small, well-defined feature set over protocol breadth. HTTP/1.1 and HTTP/2 should be first-class; HTTP/3 can follow once the core lifecycle and security model are stable.

## Architecture

```text
Clients
  |
  v
Public listener -> connection/request limits -> TLS -> routing -> middleware -> upstreams
                                           |
                                           +-> OAuth2/OIDC offload when configured

Operators
  |
  v
Admin listener or Unix socket -> authentication/authorization -> control-plane API
                                                     |
                                                     +-> validate -> compile -> atomically activate

Metrics/traces/logs are emitted by both planes, with admin endpoints kept off the public listener.
```

Keep the control plane and data plane as separate packages and preferably separate listeners. The control plane owns desired configuration, validation, compilation, status, and rollback. The data plane consumes immutable, already-validated runtime snapshots and must not observe partially applied state.

### Runtime manager

Use one runtime manager as the activation boundary for both planes. A runtime snapshot should contain the validated configuration plus compiled data-plane routes, upstream pools, TLS state, and authentication policy. A successful control-plane write must parse and validate the candidate, compile a complete replacement runtime, atomically swap it, then publish the new control-plane version. If compilation fails, neither live traffic nor the published version changes. Retain the previous runtime for rollback; do not let the control plane and data plane own independent configuration snapshots. The current implementation provides this boundary through `internal/runtime.Manager`; future TLS, authentication, and cluster reconfiguration should extend that manager rather than adding parallel callbacks.

Suggested repository layout:

```text
cmd/server/
internal/config/
internal/controlplane/
internal/dataplane/
internal/auth/
internal/routing/
internal/proxy/
internal/tlsmanager/
internal/health/
internal/observability/
api/openapi/
docs/
deploy/
```

## Configuration

Use a typed configuration schema with explicit defaults and versioning. A TOML-style example:

```toml
[server]
public_addr = ":8080"
admin_addr = "127.0.0.1:9901"

[control_plane]
ui = true

[auth]
mode = "oidc"
issuer_url = "https://identity.example.com"
client_id = "edge-server"
client_secret_file = "/run/secrets/oidc-client-secret"
redirect_url = "https://gateway.example.com/oauth2/callback"
session_cookie_name = "gateway_session"

[[routes]]
match = "api.example.com"
upstream = "api"
require_auth = true
```

Requirements:

- Reject unknown or unsafe values during validation.
- Never store client secrets in ordinary configuration responses or logs.
- Resolve secrets from files or a secret manager rather than requiring plaintext values.
- Compile routes, middleware, upstream pools, TLS state, and authentication policy into an immutable runtime snapshot.
- Activate only a fully validated snapshot using an atomic swap.
- Retain the last-known-good snapshot and support explicit rollback.
- Associate every active snapshot with a monotonically increasing version and audit event.
- Treat listener addresses, TLS files, authentication identity-provider settings, and cluster membership as restart-only until coordinated multi-listener reconfiguration is implemented.
- `route.headers` is implemented as request-header set semantics with a denylist for credentials and trusted identity headers. Response transforms and explicit add/remove operations remain separate future features.

The published module path is `github.com/alligatorcodes/go-serve`, matching the repository. Keep internal imports, deployment examples, and release metadata aligned with this canonical path.

## OAuth2/OIDC Authentication Offload

The server should act as an authentication-aware edge component so upstream applications do not each need to implement the browser-facing OAuth2/OIDC flow.

### Supported flow

1. A request reaches a route with `require_auth = true`.
2. The auth middleware checks a secure, signed/encrypted session cookie.
3. If no valid session exists, the server starts Authorization Code flow with PKCE and redirects to the configured identity provider.
4. The callback exchanges the code, validates the ID token, validates issuer, audience, nonce bound to the login state, state, expiry, and (where applicable) PKCE.
5. The server creates a bounded session and redirects to the original safe destination.
6. The proxy forwards selected identity claims to the upstream using configured headers, or forwards an access token only when explicitly requested.
7. Logout invalidates the local session and optionally invokes the provider logout endpoint.

For non-browser clients, support bearer-token validation as a separate configured mode. Do not silently mix cookie and bearer semantics.

### Security requirements

- Use an OIDC discovery document and JWKS endpoint with controlled refresh and key rotation handling.
- Validate issuer, audience, signature, nonce, state, token expiry, PKCE, and allowed claims. Generate a distinct nonce for every authorization request, bind it to server-side login state, and reject callbacks whose ID-token nonce does not match.
- Require TLS for redirect and public endpoints outside explicitly documented development mode; insecure OIDC redirects require an explicit development-only configuration flag.
- Protect cookies with `Secure`, `HttpOnly`, and an appropriate `SameSite` policy.
- Encrypt or authenticate server-side session data; use a shared session store when multiple instances are deployed.
- Prevent open redirects by allowing only configured post-login destinations.
- Bound session, token, header, and claim sizes.
- Never log authorization codes, tokens, cookies, or client secrets.
- Fail closed when token validation or key refresh cannot establish trust.
- Add route-level authorization rules for scopes, roles, or claims after authentication.
- Make upstream identity headers impossible for clients to spoof: strip incoming copies before adding trusted values.
- Test WebSocket upgrades, streaming responses, and any `http.ResponseWriter` interfaces used by the proxy. If WebSockets are out of scope, reject and document them explicitly.

Authentication should be a middleware in the request pipeline, not a special case embedded in routing or proxy code. This permits public routes, protected routes, and different policies per virtual host.

## Control-Plane API and Swagger UI

Expose a versioned API, preferably under `/api/v1`, for:

```text
GET  /api/v1/config
POST /api/v1/config/validate
PUT  /api/v1/config
POST /api/v1/config/rollback
GET  /api/v1/config/status
GET  /api/v1/servers
GET  /api/v1/routes
GET  /ready
GET  /health
```

Generate and version an OpenAPI document for these endpoints. Serve the document at a stable endpoint such as `/api/openapi.json`.

When `control_plane.ui = true`:

- Serve Swagger UI from `/api/docs`.
- Serve the UI assets from pinned, vendored, or otherwise integrity-controlled assets rather than downloading them at runtime.
- Point the UI at the local OpenAPI document.
- Protect the UI with the same control-plane authentication and authorization as the API.
- Add a clear status response when the UI is disabled, or return not found according to the API compatibility policy.
- Do not expose Swagger UI or the OpenAPI document on the public data-plane listener unless explicitly configured.
- Add a CSP and avoid embedding credentials in the generated HTML.

The `ui` flag controls presentation only. It must not disable API authentication, authorization, audit logging, or rate limiting.

For configuration writes, use optimistic concurrency with a version field or ETag. Return structured errors, record the actor and request ID, and make validation available without activation. A failed update must leave the active data plane unchanged.

### Route-header contract

Define route header behavior before implementation. Separate request headers sent upstream from response headers sent to clients, distinguish set/add/remove operations, and maintain an explicit denylist for client-spoofable trusted headers. Add compatibility tests for case-insensitive names and repeated headers.

## Concurrent Client Connections

Use Go's `net/http` server as the baseline. It handles each accepted connection/request concurrently, but production behavior must be made explicit:

- Configure separate public and admin `http.Server` instances.
- Use request contexts and enforce read-header, read, write, idle, and upstream timeouts.
- Configure bounded request body, header, URI, and response sizes.
- Use connection and in-flight request limits to protect memory and CPU.
- Use bounded worker or semaphore controls only where downstream work needs explicit admission control; do not add an unbounded goroutine per internal task.
- Keep blocking I/O in request-scoped goroutines and ensure every goroutine has a cancellation path.
- Use connection pooling and bounded idle connections for upstreams.
- Use a dedicated health-check transport that bypasses request retries and circuit breakers. Health checks must be able to detect recovery after the request circuit opens.
- Track health and circuit state per endpoint unless the documented policy intentionally takes the entire upstream pool offline. Prefer endpoint-level failure counters and a single guarded half-open probe after cooldown. This isolation is now the required implementation boundary.
- Add bounded exponential retry backoff with jitter and stop retrying when the request context deadline is nearly exhausted.
- Ensure shared configuration, metrics, sessions, and caches are race-free. Prefer immutable snapshots and synchronization at ownership boundaries.
- Run the race detector and load tests before release.

The request path should be:

```text
accept connection -> net/http scheduling -> limits -> auth -> route lookup -> middleware -> upstream I/O -> response
```

Every rejection should have a bounded cost and a useful metric. Backpressure is preferable to allowing unbounded goroutine, connection, queue, or response-buffer growth.

## Data-Plane Features

Deliver these in stages:

1. HTTP/1.1, HTTP/2, TLS termination, static responses, and reverse proxying.
2. Host/path/method/header routing and immutable route tables.
3. Upstream pools, round-robin or least-request load balancing, connection reuse, and health checks.
4. Per-route timeouts, retries limited to safe/idempotent requests by default, and circuit breaking where justified.
5. Compression, response buffering policy, request/response header transforms, and trusted forwarded-header handling.
6. Certificate loading, SNI selection, and certificate rotation without dropping active connections.
7. Optional HTTP/3 after the first release has stable operational and security coverage.

## Lifecycle and Failure Behavior

- On `SIGTERM` or `SIGINT`, mark readiness false first, wait for load balancers to observe the state, stop accepting new work, drain active requests, and exit after a bounded deadline.
- Decide and document whether an unavailable identity provider affects only new logins or also token refreshes and existing sessions.
- Treat invalid TLS or authentication configuration as an activation failure, not as a partial update.
- Define readiness as valid active configuration, available required dependencies, not draining, and, when clustering is enabled, a healthy quorum/leadership state. Liveness should only represent process health. Return `503` with a machine-readable reason when readiness is false.
- Support health, readiness, and liveness separately.
- Restrict pprof and debugging endpoints to the admin interface.
Provide:

- Structured JSON logs with request ID, trace ID, route, upstream, status, duration, configuration version, and leader/node identity where clustering is enabled.
- Prometheus metrics with bounded dimensions for route, upstream, status, and method: request totals, latency, status codes, active connections, rejected requests, auth outcomes, upstream failures, retries, circuit opens/half-open probes, health-check failures, configuration activations, and reload failures.
- OpenTelemetry traces with sensitive headers, tokens, and claims removed.
- Audit events for control-plane reads and writes, login/logout events, configuration changes, rollbacks, and authorization failures.
- Redacted startup and configuration status diagnostics.

## Security and Deployment

- Run as a non-root user with the minimum required filesystem and network privileges.
- Use separate public, admin, and metrics listeners where practical.
- Set file-descriptor, memory, CPU, and connection limits deliberately.
- Harden TLS defaults and document supported protocol versions.
- Protect admin endpoints with network policy plus application authentication.
- Provide container, systemd, and Kubernetes deployment examples with readiness probes and graceful termination settings.
- Document secret injection, key rotation, configuration migration, backups, and rollback.

## Testing and Acceptance Criteria

### Unit and integration tests

- Config parsing, semantic validation, compilation, atomic replacement, and rollback.
- Route matching, middleware ordering, auth policy, safe redirect handling, and trusted header stripping.
- OIDC discovery, JWKS rotation, token validation failures, session expiry, logout, and bearer-token behavior.
- Control-plane authorization, ETag/version conflicts, audit events, and UI enabled/disabled behavior.
- Proxy timeout, retry, health-check, circuit-breaker, and graceful-shutdown behavior.

### Security and resilience tests

- Fuzz HTTP and configuration parsing.
- Run `go test -race ./...`.
- Test malformed requests, oversized headers/bodies, request smuggling cases, spoofed identity headers, open redirects, CSRF/state failures, and expired or incorrectly signed tokens.
- Load-test concurrent connections, slow clients, slow upstreams, configuration reloads, and authentication bursts.
- Verify that memory, goroutine, connection, and queue counts remain bounded under overload.

### Release gates

A release is ready only when:

- All active configuration is validated and rollback works.
- Protected routes cannot be reached without valid authentication and authorization.
- `ui = true` exposes authenticated Swagger UI and `ui = false` does not expose it.
- Concurrent load does not produce data races or unbounded resource growth.
- Shutdown drains within the documented deadline.
- Metrics, logs, health endpoints, and audit records are usable in a deployment environment.

## Feedback-Driven Improvement Priorities

Before adding more gateway features, close these review findings in order:

1. **Runtime activation contract (implemented):** configuration and compiled data-plane state are coordinated through one runtime manager, with live routing and failed-activation coverage.
2. **Repository identity (implemented):** the canonical Go module path and internal imports use `github.com/alligatorcodes/go-serve`.
3. **OIDC correctness (implemented):** nonce generation, state binding, callback nonce validation, and secure redirect enforcement are covered by the authentication implementation and tests.
4. **Upstream isolation (implemented):** health checks use an independent transport, breaker state is endpoint-specific, and half-open recovery is guarded and covered by regression tests.
5. **Proxy correctness (implemented):** escaped paths, repeated slashes, trailing slashes, `RawPath`, encoded separators, streamed responses, connection upgrades, and trusted request-header set semantics are covered by regression tests.
6. **Lifecycle truthfulness (implemented):** readiness reflects draining and configured dependency state, including Raft availability and leadership; shutdown flips readiness before draining.
7. **Operational signal:** add bounded route/upstream/method/status dimensions and counters for retries, auth failures, health failures, circuit transitions, configuration activation, and request rejection. Include route, upstream, config version, and node identity in logs.

Each item requires focused tests, documentation updates, and a changelog entry before it is considered complete.

## Implementation Sequence

1. Establish the canonical module path, CI, linting, structured logging, and baseline server lifecycle.
2. Maintain typed configuration, immutable runtime snapshots, atomic data-plane activation, and graceful shutdown.
3. Harden concurrent HTTP handling, route/header semantics, URL-path preservation, proxy interfaces, limits, and upstream timeouts.
4. Complete the control-plane API with versioning, optimistic concurrency, activation failure handling, rollback, and audit events.
5. Harden OAuth2/OIDC with nonce validation, secure redirect enforcement, sessions, bearer validation, claim authorization, and secret handling.
6. Maintain OpenAPI generation and conditionally served, protected Swagger UI controlled by `ui = true`.
7. Isolate health-check transport, add endpoint-level health/circuit state, bounded backoff/jitter, TLS rotation, load balancing, and deployment manifests.
8. Make readiness and shutdown operationally truthful, then expand metrics, security testing, race/fuzz/load testing, documentation, and release gates.
