# Project Agent Guidance

This repository is a Go MCP server for Jira Cloud and Data Center.

## Test Strategy

- Use `.agents/skills/integration-testing/SKILL.md` when changing production code,
  tests, Docker test infrastructure, or CI.
- Unit tests isolate the component and do not use network or local infrastructure.
- Integration tests use real local Valkey and protocol-level fakes for Atlassian.
- E2E tests exercise the built `jira-mcp` binary through MCP transports.
- Every bug fix requires a regression test.

## Quality Gate

Run `make test` plus `go test -race -count=1 ./...`, `go build ./...`,
`go vet ./...`, and `gofmt -l .` before declaring work complete.

## Security

Follow `.agents/skills/secrets-safety/SKILL.md`. Never read, print, or commit
resolved API tokens, OAuth secrets, encryption keys, or Valkey credentials.

## Decisions

Record non-trivial architecture and integration choices using
`.agents/skills/document-decisions/SKILL.md` in `docs/adr/` or
`docs/features/`.
