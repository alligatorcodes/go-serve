# Configuration Skill

## Purpose
Maintain safe, immutable, validated runtime configuration.

## Rules
- Runtime configuration snapshots are immutable.
- Validate the complete proposed configuration before activation.
- Configuration writes must check `If-Match` atomically with mutation.
- Install a deep-copied snapshot.
- Retain rollback state and increment the configuration version.
- Secret-bearing fields must never be serialized into control-plane responses.

## When adding a configuration field
Update as applicable:
- `internal/config/config.go`
- `config/example.toml`
- `docs/CONFIGURATION.md`
- `api/openapi/control-plane.yaml`

Add tests for parsing, defaults, invalid input, and snapshot isolation.

## Security
Never commit:
- client secrets,
- session keys,
- access tokens,
- private keys/certificates,
- production credentials.

Use secure defaults and strict unknown-key rejection.
