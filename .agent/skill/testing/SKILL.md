# Testing Skill

## Purpose
Provide the standard validation workflow for Go changes.

## Primary checks
```bash
go test ./...
go vet ./...
CGO_ENABLED=1 go test -race ./...
```

Make targets:
```bash
make test
make vet
make race
make fuzz
make build
```

## Focused workflow
Run the narrowest relevant package tests first, then the full suite.

For configuration, test:
- parsing,
- defaults,
- validation,
- snapshot isolation.

For concurrency, test:
- stale writers,
- simultaneous updates,
- cancellation,
- race-sensitive state.

For HTTP behavior, prefer `httptest`.

## Rules
- Prefer table-driven tests.
- Avoid network-dependent unit tests.
- Do not disable race detection to hide a race.
- Every background goroutine must have a cancellation path.
- Run fuzzing when parser/config behavior changes.

Example:
```bash
go test ./internal/config -fuzz=FuzzParseTOML -fuzztime=10s
```
