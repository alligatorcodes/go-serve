# Development Skill

## Purpose
Provide the standard local development workflow.

## Requirements
- Go 1.23 or newer.
- cgo and a C compiler for race detection.

## Commands
```bash
go test ./...
go vet ./...
CGO_ENABLED=1 go test -race ./...
go run ./cmd/server -config config/example.toml
```

Make targets:
```bash
make test
make vet
make race
make fuzz
make build
make docker-build IMAGE=go-serve:dev
```

The example configuration intentionally disables authentication for local inspection. Default listeners are:
- Public: `:8080`
- Admin: `127.0.0.1:9901`

Do not add real credentials or production configuration to examples.
