# Testing

## Full Suite

Run the complete project test gate:

```bash
make test
```

This runs unit tests, integration tests against real Valkey, E2E contract tests,
and the documentation build.

## Coverage

Run unit and integration tests with coverage:

```bash
make test-coverage
```

The target starts Valkey, writes `coverage.out`, prints function-level coverage,
and writes an HTML report to `coverage.html`. Override the output paths when
needed:

```bash
make test-coverage COVERAGE_PROFILE=/tmp/jira-mcp.coverage \
  COVERAGE_HTML=/tmp/jira-mcp-coverage.html
```

Coverage instruments `./internal/...`, which contains the productive packages.
The following are intentionally outside this report because they are entrypoint
or test wiring rather than business logic:

- `cmd/jira-mcp` bootstrap and process entrypoint;
- `tests/e2e` test harness and the separately built E2E binary;
- generated code and infrastructure scripts.

E2E tests still run in `make test`; they are not silently used to inflate or
reduce the Go package coverage percentage.

## Exclusions

Go does not provide a supported `coverage:ignore` comment for arbitrary lines or
files. Do not exclude business logic, handlers, validation, authorization,
clients, repositories, adapters, or error and retry paths to improve the
percentage.

If a package is genuinely bootstrap or generated code, exclude it by narrowing
the instrumented package pattern in the Makefile and document the reason here.
Keep exclusions explicit and review them when the package changes.
