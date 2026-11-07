# ADR-004: Local console is a separate CLI process

**Date:** 2026-11-07

**Status:** Accepted
**Evidence:** Request to open the same operational pages without putting console settings in the MCP process environment.

## Context

ADR-003 started the local console inside the stdio MCP process when `JIRA_MCP_WEB=true` and printed the URL on that process's stderr. Using the panel therefore required the MCP client configuration to carry the variable, and the address showed up in the client's logs. The pages only read local audit files, which separate processes that share the state directory can already append to.

## Decision

`jira-mcp console` opens the local panel. It is a separate process. It binds IPv4 loopback on an OS-selected port, prints `http://127.0.0.1:<port>/web/overview` on its own stdout, and serves the same read-only overview, monitoring, and audit pages from the local audit directory. It does not load Jira credentials. It runs until interrupted.

The stdio MCP process does not listen for this panel. If `JIRA_MCP_WEB` is set, stdio ignores it and writes a short stderr notice. It does not print a URL.

These ADR-003 boundaries stay in force: reject non-loopback peers, unexpected Host or Origin headers, browser cross-site fetches, and non-GET methods; send no-store, frame, and content-security headers; add no local authentication. Any process or user who can reach this host's loopback port can read the operational metadata. The local audit spans every stdio profile that shares the state directory and is not one Jira account's activity.

## Alternatives Considered

- Keep `JIRA_MCP_WEB` inside stdio: rejected because activation and the URL stay in the MCP client environment and logs.
- Print the same indicators as terminal text: rejected because the request is for the existing web panel, not a second presentation.
- Add a `--web` argument to the MCP process: rejected because client configuration would still opt that process into a listener, and the URL would still land in client logs.

## Consequences

- The panel is opened with `jira-mcp console` while MCP clients stay free of console settings.
- The panel lives only as long as that command. Stopping stdio does not stop it, and stopping the command does not stop MCP.
- The URL stays the same until the command exits and is chosen again on the next run.
- Loopback is still not per-user authentication. On a shared host, leave the command stopped or isolate the host outside this application.

## Review Condition

Revisit if per-OS-user browser authentication, a stable management socket, or multi-user shared-host support is required.
