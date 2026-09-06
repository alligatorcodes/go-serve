# Control Plane Skill

## Purpose
Maintain the admin API, health/readiness endpoints, configuration API, OpenAPI contract, documentation UI, and pprof exposure.

## Security
- Admin functionality remains isolated from the public listener.
- Never expose pprof or admin APIs publicly.
- Never return secret-bearing configuration fields.

## Configuration writes
The write sequence is:
1. Strictly decode one JSON object.
2. Validate the complete proposal.
3. Check `If-Match` while holding the write lock.
4. Deep-copy/install the immutable snapshot.
5. Retain rollback state.
6. Increment the version.
7. Return the version through `ETag`.

## Tests
Cover:
- successful writes,
- invalid input,
- stale `ETag`/concurrent writers,
- rollback,
- secret redaction.

Keep `api/openapi/control-plane.yaml` synchronized with handler behavior.
