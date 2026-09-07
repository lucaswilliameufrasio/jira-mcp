# ADR-001: MCP Test Strategy

**Date:** 2026-09-06
**Status:** Accepted

## Context

The server must work with any MCP host, including OpenCode. Tests that depend on
a host-specific harness do not prove the server's protocol contract. The remote
mode also depends on local Valkey while Atlassian OAuth is an external service.

## Decision

- Unit tests isolate Go components and do not use network or local services.
- Integration tests use real Valkey `9.1.x` through Docker.
- Integration tests fake Atlassian at the HTTP protocol boundary and run the
  production OAuth client code.
- E2E tests start the built `jira-mcp` binary and use the official MCP SDK as a
  host-neutral client over stdio.
- Remote MCP transport is tested with the official Streamable HTTP client.
- OpenCode-specific validation remains an optional host smoke test; the required
  contract is the MCP protocol itself.

## Consequences

- CI does not require Claude, OpenCode, or real Atlassian credentials.
- Protocol regressions are detected independently of the MCP host.
- The integration suite requires Docker and the Valkey image.

## Review Condition

Revisit this decision if the MCP protocol or supported transport changes.
