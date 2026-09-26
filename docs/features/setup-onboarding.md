# Feature: MCP setup and agent onboarding

## Scope

Make first-time setup understandable without coaching, add standalone commands
to register/unregister Jira MCP clients and install/update Jira-specific agent
usage instructions, and make saved credentials harder to disclose accidentally.

## Confirmed Rules

- MCP configuration and agent usage instructions are separate concerns and
  receive separate CLI commands. (Source: user request, 2026-09-26)
- The instructions command installs managed Jira-MCP guidance in an agent rules
  file; Jira MCP does not introduce lifecycle event hooks because it has no
  session-event behavior to capture. (Source: user clarification, 2026-09-26)
- Setup must explain where credentials come from, what each prompt means, and
  what to do after registration (including restarting/reloading the client and
  checking the connection).
- Existing saved secrets must not be printed or prefilled in prompts. Existing
  values may be retained by submitting an empty value; replacing one requires
  explicitly entering a new value.
- On first setup, no MCP client is preselected; the user explicitly chooses
  one or more clients. Existing registrations remain preselected on later runs.
- New setup output and docs must make clear that API tokens / PATs are secrets
  and should only be pasted into the trusted local CLI prompt.

## Local Decisions

- **Decision**: Use idempotent managed markers for installed agent instructions,
  preserving all user-authored text outside the managed block. **Why**: setup
  can refresh guidance without clobbering project instructions. **Source**:
  user request and existing MCP client configuration behavior.
- **Decision**: Keep client registration available as a standalone CLI command
  in addition to the guided setup. **Why**: users can configure another client
  after credentials have already been saved. **Source**: user request.

## Open Questions

- [ ] None blocking implementation.

## Dependencies

- Existing client config discovery and safe JSON/TOML upsert logic in
  `internal/config`.
- Existing README and setup command, updated to explain the guided flow.
