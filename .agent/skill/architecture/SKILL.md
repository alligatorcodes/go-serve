# Architecture Skill

## Purpose
Preserve the project's architectural boundaries when modifying code.

## Core invariants
- Keep the control plane and data plane separate.
- Public traffic must never reach admin-only endpoints.
- Keep reusable behavior inside `internal/` unless an explicit public API is intended.
- Treat the repository's existing documentation and code as authoritative.

## Before changing architecture
Read the relevant package and:
- `docs/CONTRIBUTING.md`
- `docs/PLAN.md`
- any affected package documentation/tests.

Identify whether the change affects control plane, data plane, authentication, clustering, or observability.

## Agent rules
- Prefer small, focused changes.
- Avoid unrelated refactors.
- Preserve package boundaries and established naming.
- Do not introduce dependencies without a clear reason.
