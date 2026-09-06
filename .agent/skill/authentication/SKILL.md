# Authentication Skill

## Purpose
Keep OIDC, OAuth2/PKCE, sessions, bearer tokens, scopes, and trusted identity handling secure.

## Rules
- Prefer injected verifier fakes in unit tests.
- Never log access/refresh tokens, cookies, authorization codes, session secrets, or sensitive claims.
- Remove/overwrite trusted identity headers before proxying.
- Do not weaken authentication to make tests pass.
- Validate redirect targets and OAuth2 state.
- Enforce PKCE and required scopes.
- Handle token expiry correctly.

## Test cases
Cover:
- expired tokens,
- invalid signatures at the verifier boundary,
- missing scopes,
- OAuth2 state failures,
- PKCE failures,
- unsafe redirects,
- cookie expiry,
- spoofed identity headers.
