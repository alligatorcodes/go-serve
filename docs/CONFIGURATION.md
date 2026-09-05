# Configuration Contract

The canonical configuration format is TOML. The example configuration is in [`config/example.toml`](../config/example.toml), and the typed Go representation is in [`internal/config/config.go`](../internal/config/config.go).

## Loading and activation

The server starts from built-in defaults, then loads and validates the TOML configuration file supplied with `-config`. Unknown keys are rejected. A parse or validation failure stops startup before any listener is opened.

Configuration changes must follow this lifecycle:

1. Parse TOML into the typed `config.Config` structure, preserving defaults for omitted values.
2. Reject unknown fields, invalid durations, unsafe listener values, duplicate names, and broken references.
3. Resolve secret files without including secret values in status responses.
4. Compile routes, upstreams, TLS, and authentication policy into an immutable runtime snapshot.
5. Atomically activate the snapshot and increment its version.
6. Keep the previous known-good snapshot available for rollback.

A failed parse, validation, or compile step must leave the active configuration untouched. Runtime state is held in immutable snapshots; readers receive deep copies and cannot mutate the active snapshot through shared slices or maps.

Control-plane replacement and rollback activate the data-plane runtime before publishing the new control-plane snapshot. Listener addresses, TLS files, authentication settings, and cluster settings are restart-only fields; changing them through the API is rejected until coordinated multi-listener reconfiguration is implemented.

## Top-level sections

| Section | Purpose |
| --- | --- |
| `server` | Public/admin listeners, HTTP timeouts, and shutdown deadline. |
| `control_plane` | Admin API presentation settings, including `ui`. |
| `auth` | OAuth2/OIDC or bearer-token authentication settings. |
| `limits` | Connection, concurrency, header, and body limits. |
| `routes` | Host/path/method matching and per-route auth policy. |
| `upstreams` | Named backend pools and health-check/request settings. |

## Important rules

- `control_plane.ui = true` enables Swagger UI only. It does not weaken API authentication or authorization.
- When authentication is configured, the admin API, OpenAPI document, and documentation UI require authentication; OAuth login, callback, and logout endpoints remain available to establish a session.
- The admin listener must not be exposed through the public listener by default.
- `auth.mode = "oidc"` requires `issuer_url`, `client_id`, and `redirect_url`; client secrets come from `client_secret_file` or a future secret-manager integration.
- OIDC also requires `session_secret_file`, containing at least 32 bytes used to encrypt session cookies. Bearer mode requires `issuer_url` and `client_id` but does not create browser sessions.
- OIDC redirect URLs must use HTTPS by default. `allow_insecure_redirect = true` is for explicitly controlled local development only.
- Route identity headers must be stripped from incoming requests before trusted values are added.
- Durations use Go duration syntax such as `5s`, `30s`, and `1m`.
- Configure both `server.tls_cert_file` and `server.tls_key_file` to enable TLS termination. Certificates are reloaded for new handshakes when either file changes.
- `upstreams.health_path` enables active health checks; unhealthy endpoints are removed from selection until a later check succeeds.
- `upstreams.retry_attempts` applies only to idempotent methods (`GET`, `HEAD`, `OPTIONS`, `PUT`, and `DELETE`). `circuit_breaker_threshold` and `circuit_breaker_cooldown` bound repeated upstream failures.
- Configuration responses are redacted and never include client secrets, tokens, session material, or private keys.
- `PUT /api/v1/config` requires the current `ETag` through `If-Match` to prevent lost updates.
