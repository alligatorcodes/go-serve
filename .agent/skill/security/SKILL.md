# Security Skill

## Purpose
Prevent security regressions across the project.

## Never weaken
- authentication,
- authorization,
- trusted-header handling,
- secret redaction,
- request limits,
- TLS validation,
- configuration validation,
- admin/public network separation.

## Secrets
Never commit or log:
- credentials,
- access/refresh tokens,
- cookies,
- session secrets,
- private keys,
- authorization codes,
- sensitive identity claims.

Secrets must not cross the control-plane API or Raft FSM.

## Review checklist
Before finalizing a security-sensitive change, verify:
- client-controlled identity cannot become trusted identity,
- admin endpoints remain private,
- unsafe redirects are rejected,
- request and connection limits still apply,
- timeouts/cancellation still propagate,
- errors do not reveal secrets.
