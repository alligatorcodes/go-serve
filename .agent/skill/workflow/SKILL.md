# Contributor Workflow Skill

## Purpose
Define the expected workflow for implementation steps and pull requests.

## Before coding
Identify:
- affected subsystem,
- configuration/API impact,
- concurrency implications,
- security implications,
- required tests,
- required documentation.

Read the relevant package tests and documentation first.

## During coding
- Make the smallest change that solves the request.
- Avoid unrelated refactors.
- Preserve existing package boundaries.
- Add/update tests with behavior changes.
- Give every new goroutine a cancellation strategy.

## After coding
```bash
gofmt -w <changed-go-files>
go test ./...
go vet ./...
CGO_ENABLED=1 go test -race ./...
```

Run focused fuzz/load checks when relevant.

Update `docs/CHANGELOG.md` and affected documentation.

## PR checklist
- [ ] Architecture boundaries preserved.
- [ ] Configuration remains immutable.
- [ ] Validation occurs before activation.
- [ ] ETag/version semantics preserved.
- [ ] No secrets committed, logged, serialized, or returned.
- [ ] Authentication cannot be bypassed.
- [ ] Goroutines have lifecycle handling.
- [ ] Timeouts/cancellation preserved.
- [ ] Retries are bounded and safe.
- [ ] Tests cover success and failure paths.
- [ ] Race detector passes.
- [ ] `go test ./...` passes.
- [ ] `go vet ./...` passes.
- [ ] Documentation/changelog updated.
- [ ] Final diff contains no unrelated changes.
