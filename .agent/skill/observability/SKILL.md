# Observability Skill

## Purpose
Preserve safe request logging, counters, metrics, and active-request tracking.

## Rules
- Add new request paths to the existing observability model.
- Keep metrics exposure on the authenticated admin listener.
- Never put secrets into logs or metric labels.
- Background work must have an explicit lifecycle/cancellation strategy.

Relevant areas:
- `internal/observability/`
- `/metrics`
- `/debug/pprof/`
