# Changelog

All notable changes to Jira MCP will be documented in this file.

## [0.1.0] - 2026-09-07

### Bug Fixes

- Pin toolchain, use distroless runtime and configurable bind address


### CI / Build

- Fix docs-site job (pnpm 11 requires Node >= 22.13)


### Chores

- Pin go toolchain to 1.27.1 with mise

- Add govulncheck as go tool

- Add full Makefile dev workflow and pin go toolchain to 1.27.1

- Add MIT license, community policies, issue templates and expand CI


### Features

- Establish Jira MCP server

- Add docs site, e2e contract tests and release pipeline

- Add remote benchmark harness and honor AtlassianAPIURL override

- Harden remote mode with revoke, single-use oauth state and rate limiting


### Refactor

- Move entrypoint to cmd and add railway deployment


### Testing

- Add unit integration and e2e coverage

