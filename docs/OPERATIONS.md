# Operations Guide

## Listeners

- Public traffic listens on `server.public_addr`.
- The control plane listens on `server.admin_addr` and should remain private through network policy or a Unix-socket front end.
- Metrics and pprof are exposed only from the authenticated control-plane listener.
- Configure `tls_cert_file` and `tls_key_file` together for TLS termination. New TLS handshakes reload changed certificate files.

## Monitoring

Scrape `GET /metrics` from the control-plane listener after authenticating. The initial metrics include total requests, responses, server errors, and active requests. Request logs include method, path, status, duration, and the supplied request ID; credentials, cookies, and authorization headers are not logged.

Use `/health` for process liveness and `/ready` for traffic readiness. A deployment must remove the instance from service before sending `SIGTERM` and allow `server.shutdown_timeout` for draining.

## Security checklist

- Run as the dedicated non-root service account from the systemd or Kubernetes examples.
- Keep the admin listener off the public network.
- Inject OIDC client and session secrets through files or a secret manager.
- Restrict pprof and metrics to trusted operators.
- Use TLS for public traffic and OIDC redirects outside local development.
- Review route `require_auth`, `scopes`, body limits, upstream timeouts, retries, and circuit-breaker thresholds before production.
- Do not log tokens, cookies, authorization codes, or secret file contents.

## Release checks

```sh
go test ./...
go vet ./...
CGO_ENABLED=1 go test -race ./...
```

Also run load tests with slow clients and upstreams, fuzz configuration parsing, verify TLS rotation, test configuration rollback, and confirm that metrics and pprof are unavailable from the public listener.