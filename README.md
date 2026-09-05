# go-serve

A production-oriented Go HTTP gateway with a separated control plane and data plane.

## Status

The repository currently contains strict TOML configuration loading, immutable configuration snapshots, concurrent public and control-plane HTTP servers, host/path routing, reverse proxying, request limits, OAuth2/OIDC authentication offload, and an OpenAPI specification.

## Run

```sh
go run ./cmd/server
```

Load a configuration file with:

```sh
go run ./cmd/server -config config/example.toml
```

The bootstrap control plane listens on `127.0.0.1:9901` by default:

```sh
curl http://127.0.0.1:9901/health
curl http://127.0.0.1:9901/api/v1/config/status
```

## Test

```sh
go test ./...
```

See [`docs/PLAN.md`](docs/PLAN.md), [`docs/CONFIGURATION.md`](docs/CONFIGURATION.md), [`config/example.toml`](config/example.toml), and [`api/openapi/control-plane.yaml`](api/openapi/control-plane.yaml) for the design and current contracts.
