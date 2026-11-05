# ADR-002: Remote browser authentication

**Date:** 2026-11-05

**Status:** Accepted
**Evidence:** Request for authenticated, per-user remote operational pages.

## Context

The existing OAuth authorization-code flow gives MCP clients bearer tokens; a browser must not put these tokens or Atlassian credentials into page scripts or URLs as session credentials. A browser login must also prevent cross-user session substitution and authorization-code replay.

## Decision

Reuse the existing Atlassian authorization and Jira-site selection, with an internally registered browser OAuth client and a server-generated PKCE verifier. Browser logins use a separate randomly identified user record so they cannot overwrite existing MCP sessions' granted tools or credentials. Bind short-lived state to an HttpOnly SameSite cookie. Consume both browser state and the issued authorization code atomically from Valkey; reject replay and invalid PKCE. Issue a separate random opaque browser session, storing only its SHA-256 digest and the authenticated user/site reference in Valkey for eight hours. Set HttpOnly, SameSite=Lax and, for HTTPS, Secure; do not use browser sessions as MCP bearer tokens. Require HTTPS for non-loopback public URLs. Protect logout using an exact same-origin check and a per-session CSRF token; delete the server-side session and cookie on logout. Set no-store and no-referrer headers on browser auth responses. Fail closed if atomic consumption or session storage is unavailable.

## Alternatives Considered

- Expose the MCP bearer token to browser JavaScript: rejected because this broadens credential exposure and mixes audiences.
- Implement a second independent Atlassian token flow: rejected because it duplicates existing site selection and token handling.

## Consequences

- Operational pages can use server-side identity without receiving Atlassian credentials.
- Sessions depend on Valkey and expire after eight hours; logout does not revoke existing MCP sessions or Atlassian consent. The current flow requires reauthentication after expiry.
- Reverse proxies must serve the configured public HTTPS origin and avoid logging OAuth callback query parameters.
- Authenticated console pages use `Referrer-Policy: same-origin` so Firefox sends the same-origin form submission's Origin; OAuth and session endpoints retain `no-referrer`. Cross-origin referrers remain suppressed and logout still requires an exact Origin and CSRF token.

## Review Condition

Revisit when SSO/session lifecycle, distributed revocation, or a separate identity provider is introduced.
