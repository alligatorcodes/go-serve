# Documentation Skill

## Purpose
Keep implementation, API, configuration, operations, and project history synchronized.

## Required updates
For relevant changes update:
- `docs/CHANGELOG.md`
- affected documentation
- `api/openapi/control-plane.yaml`
- `config/example.toml`

When a public configuration field changes, also update:
- `internal/config/`
- `docs/CONFIGURATION.md`

## Source-of-truth documents
- `docs/CONTRIBUTING.md`
- `docs/CONFIGURATION.md`
- `docs/CLUSTERING.md`
- `docs/PLAN.md`
- `api/openapi/control-plane.yaml`
- `config/example.toml`
- `docs/CHANGELOG.md`

At the end of each implementation step, review the diff for stale documentation and accidental secrets.
