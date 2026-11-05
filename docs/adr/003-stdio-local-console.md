# ADR-003: Optional stdio console on loopback

**Date:** 2026-11-05

**Status:** Accepted
**Evidence:** Request for the same operational pages in stdio, limited to local access.

## Context

The stdio process has no web origin or interactive login. The console must not corrupt the MCP stdout stream or expose Jira credentials, and should not require an Atlassian OAuth application for a local connection.

## Decision

When `JIRA_MCP_WEB=true`, start an ephemeral IPv4 listener bound only to `127.0.0.1`. Print its non-secret URL to stderr, never stdout. Serve the shared read-only console and the existing local audit identity while the stdio process runs. Reject non-loopback peers, unexpected Host or Origin headers, browser cross-site fetches and non-GET methods; apply no-store, frame and content-security headers. No authentication is added: any process or user able to access this host's loopback port can read its operational metadata. The local audit spans all stdio profiles sharing the same state directory; do not present it as a single Jira account's activity. Bind failures prevent startup when the user explicitly opted in. Closing the stdio process closes the listener.

## Alternatives Considered

- Bind all interfaces: rejected because it exposes local information to the network.
- Reuse remote Atlassian OAuth: rejected because local Server/Data Center setups may not have OAuth.
- Embed a bearer secret in a URL or log: rejected because it would expose a credential in browser history, logs or process arguments.

## Consequences

- Local developers can use the same pages without a separate service and MCP stdout remains protocol-only.
- Loopback is not per-user authentication; shared-host operators must leave the optional console disabled or provide isolation outside this application. The URL changes each start and may be visible only in the MCP client's stderr logs.
- Host and fetch-metadata checks are defense in depth, not protection from hostile local processes.

## Review Condition

Revisit if per-OS-user browser authentication, a stable management socket, or multi-user shared-host support is required.
