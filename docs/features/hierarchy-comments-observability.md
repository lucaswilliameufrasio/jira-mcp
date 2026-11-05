# Feature: Hierarchy, rich comments, and operational views

## Scope

- Manage Jira issues in epic and initiative hierarchies, including separate operations to link, unlink, and permanently delete an issue.
- Allow rich Jira Cloud comments, including mentions, while preserving plain-text compatibility.
- Provide separate overview, monitoring, and audit views in both remote and stdio modes.

## Confirmed rules

- Linking, unlinking, and permanent deletion are separate operations. Deletion requires an explicit request and is not the same as unlinking. Source: user request.
- Audit MCP actions using metadata without issue content or credentials; retention is configurable. Source: user request.
- The stdio web interface is optional and restricted to localhost. The remote interface requires authenticated, per-user access. Source: user request.
- Jira remains the authority for hierarchy rules, permissions, and supported deployment-specific formats.

## Open questions

- Determine hierarchy and rich-comment support for each Jira deployment using its actual API contracts.

## Implementation slices

1. Hierarchy operations with explicit link, unlink and delete semantics, tests, and documentation.
2. Rich comments with backward-compatible text input and deployment-specific validation.
3. Secure, isolated audit and monitoring instrumentation, followed by the three connected web views for each runtime mode.

## Local decisions

- Rich comments accept the existing `comment` string or an ADF document object. Cloud forwards valid document envelopes unchanged; Server/Data Center rejects objects rather than silently losing mentions. Jira validates the complete document. Existing text callers remain compatible.
- Generic hierarchy operations use the editable `parent` field, with the legacy Epic Link field as a fallback only for an Epic parent. Jira validates permitted hierarchy levels. Unlinking examines the current relationship before clearing it; deletion is a distinct operation without implicit subtask deletion.
- Instrument MCP tool execution at the shared MCP boundary, recording tool name, timestamp, duration and outcome only. Never record arguments, responses, credentials, issue keys or upstream error text. This boundary covers both transports and keeps observability independent from Jira operation implementations.
- Audit retention defaults to 30 days and is configured with `JIRA_MCP_AUDIT_RETENTION` as a positive Go duration, capped at 365 days. The stdio mode uses one private event file per call under the local state directory, permitting multiple MCP processes to append without overwriting each other. Expired files are cleaned on writes and reads; an inactive installation cannot remove them until the next access. Remote mode uses per-identity Valkey keys with server-side expiry and an atomic sorted-set index. The index is bounded, so audit is operational best-effort rather than a compliance-grade immutable ledger. A storage failure is reported to stderr without sensitive payloads and does not change the result of a Jira operation, which may already have happened. Source: implementation choice; revisit for compliance or stronger delivery requirements.
- Browser authentication and session isolation follow ADR-002.
- Remote operational pages use server-rendered Go templates, without third-party assets or JavaScript. Overview, monitoring and audit are separate read-only journeys. Each request derives the audit identity solely from its authenticated account and Jira site. Indicators summarize at most the 200 latest retained events, explicitly label that limit, and must not imply complete system-wide metrics or current Jira connectivity. A failed audit read is shown as unavailable, never as zero activity. Page output is private, uncached and automatically escaped. The stdio interface reuses the same renderer. Source: implementation choice.
- The optional stdio console shares those templates and local audit records, with the loopback-only boundary and limitations in ADR-003.
