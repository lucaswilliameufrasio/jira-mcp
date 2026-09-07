# Security Policy

## Supported Versions

Only the latest release receives security fixes:

| Version | Supported |
|---|---|
| latest tagged release | yes |
| older releases | no |
| `main` | yes (best effort) |

## Reporting a Vulnerability

Please report vulnerabilities privately using
[GitHub private vulnerability reporting](https://github.com/lucaswilliameufrasio/jira-mcp/security/advisories/new).
Do not open a public issue for anything you believe is exploitable.

Include, when possible:

- affected version or commit;
- deployment mode (`stdio`, `remote`);
- impact and a minimal reproduction;
- any relevant logs (redact tokens and URLs containing secrets).

You can expect:

- an initial response within 72 hours;
- a fix or a mitigation plan, with credit if you want it;
- coordinated disclosure after a patched release.

## Scope Notes

- **Remote mode** is the primary surface: the OAuth 2.0 flow (PKCE,
  single-use state), MCP access tokens, token storage in Valkey (encrypted at
  rest), rate limiting and the public HTTP surface.
- **stdio mode** assumes a trusted local user; the process speaks JSON-RPC on
  stdin/stdout and holds Jira credentials in its environment. Local privilege
  escalation through this mode is out of scope.
- Credentials must never be committed, logged, or echoed. If you find a
  secret in the repository or in release artifacts, treat it as a
  vulnerability and report it.
