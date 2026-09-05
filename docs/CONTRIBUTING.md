# Contributing to go-serve

This guide is for developers changing the gateway, its control plane, or its operational tooling.

## Project Shape

The project is a Go HTTP gateway with two deliberately separate planes:

```text
cmd/server/main.go
  |-- loads and validates TOML
  |-- initializes authentication and data plane
  |-- creates public and admin listeners
  |-- owns graceful shutdown

internal/config/
  typed configuration, defaults, strict TOML parsing, validation,
  deep-copy snapshots

internal/controlplane/
  health/readiness, versioned configuration API, rollback,
  OpenAPI document, conditional documentation UI, pprof routes

internal/dataplane/
  host/path/method routing, request limits, authentication hook,
  reverse proxy, upstream pools, health checks, retries, circuit breaker,
  TLS listener and connection limits

internal/auth/
  OIDC discovery and token verification, OAuth2 authorization-code + PKCE,
  encrypted sessions, bearer tokens, scope checks, trusted identity headers

internal/observability/
  request logging, counters, metrics exposition, active-request tracking

api/openapi/
  versioned control-plane API contract
config/
  example TOML configuration
deploy/
  systemd and Kubernetes deployment examples
docs/
  plans, configuration, operations, changelog, and this guide
```

The `internal` boundary is intentional. Reuse within this repository is welcome; external consumers should use the documented HTTP and configuration contracts rather than importing internal packages.

## Request Lifecycles

### Public traffic

1. The public listener accepts a connection through the connection limit.
2. Optional TLS performs a handshake and reloads changed certificate files for new handshakes.
3. Observability wraps the request and records counters/logs.
4. The data plane applies the in-flight and body-size limits.
5. The compiled route table selects the most specific host/path match.
6. Required authentication verifies a session or bearer token and adds trusted identity headers.
7. The upstream pool selects a healthy endpoint and applies timeout, retry, and circuit-breaker policy.
8. The reverse proxy forwards the request and streams the response.

### Control-plane changes

1. The API decodes one strict JSON object and rejects unknown fields.
2. Configuration validation runs before any state changes.
3. `If-Match` is checked while holding the state write lock.
4. A new deep-copied snapshot is atomically installed.
5. The previous snapshot remains available for rollback and the version increments.
6. Responses redact secret paths and expose the active version through `ETag`.

Keep these boundaries intact. Do not mutate a live snapshot, bypass validation, or let public traffic reach admin-only endpoints.

## Local Development

Requirements:

- Go 1.23 or newer
- cgo and a C compiler for race detection

Basic commands:

```sh
go test ./...
go vet ./...
CGO_ENABLED=1 go test -race ./...
go run ./cmd/server -config config/example.toml
```

Equivalent project workflows are available through the `Makefile`:

```sh
make test
make vet
make race
make fuzz
make build
make docker-build IMAGE=go-serve:dev
```

The example configuration intentionally uses `auth.mode = "disabled"` and `require_auth = false` so it can be inspected without identity-provider secrets. The default ports are public `:8080` and admin `127.0.0.1:9901`; change both when another local service owns those ports.

For a minimal local config, use the defaults and add a route/upstream only when testing proxy behavior. Do not commit real client secrets, session keys, certificates, access tokens, or production endpoints.

## Making Changes

### Configuration changes

1. Add or update the typed field in `internal/config/config.go` with both TOML and JSON tags where appropriate.
2. Preserve secure defaults in `Default()`.
3. Add validation for required pairs, unsafe combinations, bounds, and cross-references.
4. Update `config/example.toml`, `docs/CONFIGURATION.md`, and `api/openapi/control-plane.yaml` when the field is user-facing.
5. Add parser, default, invalid-input, and snapshot-isolation tests.

Secret-bearing fields must use `json:"-"` and must be loaded from files or a secret manager. Never return secret values from the control plane.

### Data-plane changes

Compile configuration into immutable runtime objects in `NewServer`. Keep per-request mutable state local or synchronized. For upstream behavior:

- Only retry idempotent methods unless a caller explicitly opts into another policy.
- Bound retry attempts and circuit-breaker recovery time.
- Preserve request cancellation and upstream timeouts.
- Remove or overwrite trusted identity headers before proxying.
- Add a test for healthy, unhealthy, slow, and unavailable upstream behavior as applicable.

### Control-plane changes

Control-plane writes must validate before activation and use the version check atomically with the state mutation. Add tests for success, invalid input, stale `ETag`, rollback, redaction, and concurrent writers. Keep OpenAPI and handler behavior synchronized.

### Authentication changes

Use injected verifier fakes for unit tests instead of live identity providers. Test expired tokens, invalid signatures through the verifier boundary, missing scopes, state/PKCE failures, unsafe redirects, cookie expiry, and spoofed identity headers. Do not log tokens, cookies, authorization codes, or claims containing sensitive data.

### Documentation changes

At the end of every implementation step:

1. Update `docs/CHANGELOG.md`.
2. Run the relevant tests and static checks.
3. Commit the completed step with a focused message.

## Testing Expectations

Use the narrowest useful test first, then the complete suite:

```sh
go test ./internal/config ./internal/controlplane ./internal/dataplane ./internal/auth ./internal/observability
go test ./...
go vet ./...
CGO_ENABLED=1 go test -race ./...
go test ./internal/config -fuzz=FuzzParseTOML -fuzztime=10s
```

Use `httptest` servers for proxy and control-plane tests. Avoid network-dependent tests unless they are explicitly integration tests. Every new goroutine needs a cancellation path, and every new shared field needs race-test coverage.

## Troubleshooting

### `listen tcp ... address already in use`

Find the owner with an available process tool, or choose alternate `public_addr` and `admin_addr` values in a temporary TOML file. The project should not terminate unrelated processes. Remember that the admin and public listeners are separate.

### `unknown configuration keys`

TOML decoding is strict. Compare the spelling against `internal/config/config.go` and `config/example.toml`. Unknown fields are rejected intentionally to prevent silent misconfiguration.

### `tls_cert_file and tls_key_file must be configured together`

Provide both paths. Confirm the process can read them, the key matches the certificate, and the files contain a valid PEM certificate/key pair. Certificate changes affect new TLS handshakes; existing connections are not renegotiated.

### OIDC startup fails during discovery or secret loading

Check issuer URL reachability and discovery metadata, client ID, client-secret file permissions, session-secret file permissions, and the redirect URL. Session secrets must contain at least 32 bytes. For local work, keep authentication disabled rather than committing test secrets.

### Protected request returns `401`, `403`, or redirects to login

`401` means no valid session or bearer token was accepted. `403` means authentication succeeded but required scopes were missing. Browser GETs with `Accept: text/html` may redirect to `/oauth2/login`; API clients should send a bearer token. Check route `require_auth` and `scopes` values.

### Request returns `503 upstream unavailable`

Check the upstream URL, `health_path`, health interval, DNS, TLS trust, dial timeout, and the upstream service logs. An endpoint returning outside the 2xx/3xx range is removed from selection. The circuit breaker can temporarily reject calls after repeated failures.

### Requests return `502` after a timeout or oversized body

`502` indicates proxy/transport failure. Check `request_timeout`, dial timeout, retry behavior, and upstream logs. A body larger than `limits.max_body_bytes` can fail while the proxy is reading it; lower limits are intentional resource protection.

### Metrics or pprof are unavailable

Use the authenticated admin listener, not the public listener. Metrics are at `/metrics`; pprof starts at `/debug/pprof/`. Authentication middleware protects both when auth is configured.

### Race tests fail to start

The race detector requires cgo and a C compiler. Check `CGO_ENABLED=1` and `gcc --version` or the equivalent compiler. Do not disable the race detector to hide a real data race; install the missing toolchain instead.

### The server exits during an example startup

Check the first structured error line. Common causes are occupied ports, invalid TOML, missing OIDC files, incomplete TLS pairs, or an upstream route referencing an unknown pool. Validate configuration before investigating runtime traffic.

## Pull Request Checklist

- [ ] The change is scoped to one behavior or milestone.
- [ ] Configuration and API contracts are updated if needed.
- [ ] Secrets are not committed, logged, or returned.
- [ ] Unit/integration tests cover success and failure paths.
- [ ] Concurrency-sensitive changes pass the race detector.
- [ ] `go test ./...`, `go vet ./...`, and relevant fuzz/load checks pass.
- [ ] `docs/CHANGELOG.md` and affected operational docs are updated.
- [ ] The commit message describes the behavior delivered.
