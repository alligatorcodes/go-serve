# AGENTS.md

Go 1.23 HTTP gateway with separated data plane (public traffic) and control plane (admin API). Canonical module path: `github.com/alligatorcodes/go-serve`.

## Commands

```sh
go test ./...                      # full suite (CI also runs this)
go vet ./...                       # static checks (CI)
CGO_ENABLED=1 go test -race ./...  # race detector REQUIRES cgo + C compiler; never disable it to hide a race
go test ./internal/config -fuzz=FuzzParseTOML -fuzztime=10s   # config fuzz (make fuzz)
make build                         # builds bin/go-serve, -trimpath -ldflags='-s -w'
go run ./cmd/server -config config/example.toml
```

All equivalent targets exist in the Makefile (`test`, `vet`, `race`, `fuzz`, `build`, `run`, `fmt`, `docker-build`, `docker-run`, `clean`). `make fmt` runs `gofmt -w cmd internal`.

## Architecture

- `cmd/server/main.go` wires everything: loads/validates TOML, builds auth + data plane through `internal/runtime.Manager`, then runs two `http.Server`s. Control plane defaults to `127.0.0.1:9901`, public to `:8080`.
- `internal/runtime.Manager` is the single activation boundary. Control-plane PUT/rollback go through `Manager.Activate`, which compiles the candidate before atomically swapping the data plane. `server.*` listeners, `auth`, and `cluster` settings are **restart-only** and rejected by `Activate` (`internal/runtime/manager.go:43`).
- Config is immutable: `config.Config` is deep-copied (`Clone`) and passed by value. Never mutate a live snapshot or store one with shared slices/maps (`internal/config/config.go`).
- TOML loading is **strict** — unknown keys are rejected intentionally. After adding a config field update `internal/config/config.go`, `config/example.toml`, `docs/CONFIGURATION.md`, and `api/openapi/control-plane.yaml` together.
- Metrics (`/metrics`) and pprof (`/debug/pprof/`) are served only on the authenticated admin listener, never the public one.
- Cluster (`internal/cluster`) is HashiCorp Raft: request bodies, tokens, cookies, and secrets must never be stored in the Raft FSM. `config/example-cluster.toml` is single-node dev-only; multi-node needs 3/5 voting nodes (see `docs/CLUSTERING.md`).

## Conventions

- Secrets use `json:"-"` tags, load from files, and must never be logged, returned by the API, or committed. `docs/CONTRIBUTING.md` is the canonical contributor contract (proxy/retry rules, auth testing with injected fakes, troubleshooting).
- Update `docs/CHANGELOG.md` as part of every behavior-changing commit.
- Use `httptest` servers for proxy/control-plane tests; no network dependencies in unit tests. Every new goroutine needs a cancellation path; concurrent fields need race coverage.
- Retries apply only to idempotent methods; strip trusted identity headers before proxying.

## Gotchas

- Running the cluster example (`config/example-cluster.toml`) writes Raft state to `./data/example-cluster-node-1/`, which is **untracked and not gitignored**. Delete it to reset the dev cluster identity; don't commit it.
- TLS cert/key pairs reload for new handshakes only, not existing connections. OIDC in non-dev configs rejects HTTP redirect URLs unless `allow_insecure_redirect = true`.
- `address already in use` means change `public_addr`/`admin_addr` in a copy of the config — never kill unrelated processes.
- Admin and public listeners are separate; port bindings are independent.