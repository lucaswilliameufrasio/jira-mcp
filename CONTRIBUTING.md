# Contributing to jira-mcp

Thanks for considering a contribution. This document explains how to set up
the project and what is expected from a pull request.

## Getting Started

Prerequisites: [mise](https://mise.jdx.dev) (recommended), Go, and Docker for
integration tests.

```bash
make setup    # toolchain + go modules + docs-site dependencies
make help     # list every target
```

## Development Loop

```bash
make fmt            # format
make lint           # golangci-lint
make vet            # go vet
make vuln           # govulncheck
make test-unit      # unit tests
make test-integration  # integration tests against real Valkey (docker compose)
make test-e2e       # end-to-end tests against the real binary
make quality        # the commit gate: check + full test suite
```

`make quality` must pass before you open or update a pull request. CI runs
the same suites, so a red local run means a red PR.

## Pull Requests

- Keep PRs small and focused; one logical change per PR.
- Add or update tests for any behavior change (unit tests for logic,
  e2e contract tests for protocol-visible behavior).
- Update documentation when behavior or configuration changes: `README.md`,
  `docs/`, or the docs-site.
- Commits follow [Conventional Commits](https://www.conventionalcommits.org)
  (`feat:`, `fix:`, `docs:`, `test:`, `chore:`, `refactor:`, `ci:`) — the
  changelog is generated from them.

## Security

Never commit or paste credentials (Jira API tokens, OAuth client secrets,
encryption keys). If you discover a vulnerability, follow
[`SECURITY.md`](SECURITY.md) instead of opening an issue.

## Reporting Bugs

Open an issue using the bug template and include the deployment mode
(`stdio` or `remote`), the Jira type (Cloud or Data Center), the version, and
steps to reproduce.
