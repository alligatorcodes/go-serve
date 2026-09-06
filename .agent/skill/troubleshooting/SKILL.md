# Troubleshooting Skill

## Port already in use
Do not terminate unrelated processes. Use a temporary configuration with alternate public/admin addresses and identify the owning process with normal local process tooling.

## Unknown configuration key
Strict TOML parsing is intentional. Check:
- `internal/config/config.go`
- `config/example.toml`
- `docs/CONFIGURATION.md`

Do not silently accept unknown keys.

## TLS certificate/key error
Configure certificate and key together. Verify both files exist, are readable, valid PEM, and the key matches the certificate.

Certificate changes apply to new TLS handshakes; existing connections are not renegotiated.

## OIDC startup failure
Check issuer reachability, discovery metadata, client ID, client-secret file permissions, session-secret file permissions, and redirect URL.

Keep authentication disabled for local development rather than committing test secrets.

## 401 / 403 / login redirect
- `401`: no valid session or bearer token was accepted.
- `403`: authentication succeeded but required scopes were missing.
- Browser HTML GETs may redirect to `/oauth2/login`.
- API clients should normally use a bearer token.

Inspect route `require_auth` and `scopes`.

## 503 upstream unavailable
Check upstream URL, health path, health interval, DNS, TLS trust, dial timeout, and upstream logs.

## 502 proxy/transport failure
Inspect request timeout, dial timeout, retry behavior, upstream logs, and body-size limits.

## Metrics/pprof unavailable
Use the authenticated admin listener. Do not expose admin diagnostics on the public listener.
