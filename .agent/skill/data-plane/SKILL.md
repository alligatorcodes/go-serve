# Data Plane Skill

## Purpose
Safely modify request routing, limits, authentication hooks, proxying, upstream health, retries, and circuit breaking.

## Expected request flow
1. Accept connections subject to limits.
2. Perform optional TLS handshake.
3. Apply observability.
4. Apply in-flight/request-body limits.
5. Match the most-specific host/path route.
6. Authenticate when required.
7. Add trusted identity only after authentication.
8. Select a healthy upstream.
9. Apply timeout/retry/circuit-breaker policy.
10. Reverse-proxy while preserving cancellation.

## Rules
- Client-controlled trusted identity headers must never reach upstreams unchanged.
- Preserve request cancellation and timeouts.
- Retry idempotent methods by default.
- Bound retry attempts and circuit-breaker recovery.
- Handle healthy, unhealthy, slow, and unavailable upstreams.
- Every new goroutine needs a cancellation/lifetime strategy.

## Testing
Prefer `httptest` servers. Cover successful and failure paths, including upstream failures and request limits.
