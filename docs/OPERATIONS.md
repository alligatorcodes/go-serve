# Operations Guide

## Listeners

- Public traffic listens on `server.public_addr`.
- The control plane listens on `server.admin_addr` and should remain private through network policy or a Unix-socket front end.
- Metrics and pprof are exposed only from the authenticated control-plane listener.
- Configure `tls_cert_file` and `tls_key_file` together for TLS termination. New TLS handshakes reload changed certificate files.

## Monitoring

Scrape `GET /metrics` from the control-plane listener after authenticating. The initial metrics include total requests, responses, server errors, and active requests. Request logs include method, path, status, duration, and the supplied request ID; credentials, cookies, and authorization headers are not logged.

Use `/health` for process liveness and `/ready` for traffic readiness. On shutdown, the server marks `/ready` as `503` with `{"status":"draining"}`, waits briefly for load balancers to observe it, then stops accepting connections and drains for up to `server.shutdown_timeout`.

## Security checklist

- Run as the dedicated non-root service account from the systemd or Kubernetes examples.
- Keep the admin listener off the public network.
- Inject OIDC client and session secrets through files or a secret manager.
- Restrict pprof and metrics to trusted operators.
- Use TLS for public traffic and OIDC redirects outside local development.
- For clustering, use three or five voting nodes, keep the Raft port private, and store each node's Raft data on its own durable volume.
- Treat virtual-IP ownership as a host integration: use audited, idempotent leader/follower hooks or keepalived rather than granting the gateway broad network privileges.
- Monitor leader changes, quorum loss, assignment expiry, forwarding failures, and VIP hook failures.
- Review route `require_auth`, `scopes`, body limits, upstream timeouts, retries, and circuit-breaker thresholds before production.
- Circuit breakers are endpoint-specific; a failed backend should not take healthy peers offline. Health checks use an independent transport and can restore an endpoint after request failures.
- Do not log tokens, cookies, authorization codes, or secret file contents.

## Release checks

```sh
go test ./...
go vet ./...
CGO_ENABLED=1 go test -race ./...
```

Also run load tests with slow clients and upstreams, fuzz configuration parsing, verify TLS rotation, test configuration rollback, and confirm that metrics and pprof are unavailable from the public listener.