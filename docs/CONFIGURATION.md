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
- The admin listener must not be exposed through the public listener by default.
- `auth.mode = "oidc"` requires `issuer_url`, `client_id`, and `redirect_url`; client secrets come from `client_secret_file` or a future secret-manager integration.
- Route identity headers must be stripped from incoming requests before trusted values are added.
- Durations use Go duration syntax such as `5s`, `30s`, and `1m`.
- Configuration responses are redacted and never include client secrets, tokens, session material, or private keys.
- `PUT /api/v1/config` requires the current `ETag` through `If-Match` to prevent lost updates.
