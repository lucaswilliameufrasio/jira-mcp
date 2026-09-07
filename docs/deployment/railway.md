# Deploying the Remote MCP Server to Railway

The remote mode runs the same `jira-mcp` binary as a multi-user Streamable HTTP
server with Atlassian OAuth. Railway builds it from the repository root
`Dockerfile` and connects it to a Valkey instance.

## Services

1. Create a Railway project with:
   - the GitHub repo as the application service (Railway detects the root
     `Dockerfile`);
   - a **Valkey** service in the same environment;
   - a public domain on the app service (required for the OAuth callback).
2. Link the app to Valkey using a reference variable, never a pasted URL:

   ```text
   VALKEY_URL = ${{Valkey.REDIS_URL}}
   ```

## Required Variables

Set these on the application service (values stay in Railway, never in Git):

| Variable | Purpose |
|---|---|
| `JIRA_MCP_PUBLIC_URL` | Public base URL, e.g. `https://jira-mcp.up.railway.app`. Used to build OAuth callback and discovery URLs. |
| `JIRA_MCP_ATLASSIAN_CLIENT_ID` | OAuth app client ID. |
| `JIRA_MCP_ATLASSIAN_CLIENT_SECRET` | OAuth app client secret. |
| `JIRA_MCP_ENCRYPTION_KEY` | Base64 key for exactly 32 bytes, used to encrypt stored tokens. |
| `VALKEY_URL` | Valkey connection URL (prefer a `${{service.VAR}}` reference). |

Generate the encryption key locally without printing it anywhere shared:

```bash
openssl rand -base64 32 | tr -d '=\n'
```

Optional variables:

| Variable | Default | Purpose |
|---|---|---|
| `JIRA_MCP_LISTEN_ADDR` | unset | Fixed `host:port`. Takes precedence over `HOST`/`PORT`. Leave unset on Railway. |
| `HOST` | `0.0.0.0` | Bind address when `JIRA_MCP_LISTEN_ADDR` is unset. |
| `PORT` | `8080` | Bind port when `JIRA_MCP_LISTEN_ADDR` is unset. Railway injects it automatically. |
| `JIRA_MCP_RATE_LIMIT_RPS` | `20` | Per-client request limit for `/mcp` (burst is 2x, keyed by `X-Forwarded-For`/remote address). `0` disables limiting. |
| `JIRA_MCP_ATLASSIAN_AUTH_URL` | `https://auth.atlassian.com` | Override only for tests. |
| `JIRA_MCP_ATLASSIAN_API_URL` | `https://api.atlassian.com` | Override only for tests. |

## Atlassian OAuth App

In the Atlassian developer console, create an OAuth 2.0 (3LO) app and register
the callback:

```text
<JIRA_MCP_PUBLIC_URL>/oauth/callback
```

The callback host must match the Railway public domain exactly.

## Healthcheck and Port

- The server exposes `GET /health` unauthenticated; configure the Railway
  healthcheck to use it.
- The image is built from `gcr.io/distroless/static-debian13:nonroot` (no
  shell, runs as a non-root user) and does not declare a fixed port: the
  process binds to `$HOST:$PORT` (`0.0.0.0` and the Railway-injected `PORT`
  by default).
- The HTTP server sets `ReadHeaderTimeout` (10s) and `IdleTimeout` (2m) to
  shed slow-loris style connections; request bodies are not time-limited so
  long-lived MCP streams keep working.

## Token Lifecycle

- MCP access tokens live in Valkey for 30 minutes and are minted once per
  OAuth dance.
- `POST /oauth/revoke` with `token=<mcp access token>` (form-encoded) deletes
  the session immediately and always answers 200 (RFC 7009 semantics). Point
  client logout at it.
- Atlassian refresh tokens persist for up to 90 days so access keeps working
  across token refreshes; users can also revoke the app from their Atlassian
  account settings.

## Deployment Flow

- Pushes to the production branch trigger a new deployment after the Docker
  build succeeds.
- Rollbacks use previous Railway deployments.
- The Valkey service can be restarted independently; stored state survives app
  deploys.

## Security Notes

- Never log, echo, or commit any of the variable values above (see
  `.agents/skills/secrets-safety/SKILL.md`).
- If a secret was exposed, rotate it: generate a new client secret or
  encryption key and update the service; re-authorization by users is required
  after an encryption key rotation.
- Use Railway's variable references (`${{service.VAR}}`) between services so
  credentials are never visible to humans or agents.

## Related

- Documentation site deploys separately on Cloudflare Pages:
  [`cloudflare-pages.md`](cloudflare-pages.md).
- Release process for binaries and tags:
  [`../releasing.md`](../releasing.md).
