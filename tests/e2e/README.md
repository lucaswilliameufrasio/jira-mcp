# MCP E2E and Jira Contract Tests

These tests start the built `jira-mcp` binary as an MCP server and exercise the
real stdio protocol. They do not depend on Claude, Grok, OpenCode, or another
host application.

## Run

```bash
make test-e2e
```

To run a manually built binary:

```bash
JIRA_MCP_BINARY=/absolute/path/to/jira-mcp go test -tags=e2e -count=1 ./tests/e2e
```

## Fake Jira Contract

The fake Jira server in `contract_test.go` is a protocol-level test double. The
production Jira client and MCP handlers run unchanged; only the external Jira
HTTP boundary is local.

The contract currently covers:

- Jira Cloud v3 enhanced JQL search;
- Jira Data Center v2 classic search;
- Cloud Basic authentication;
- Data Center Bearer authentication;
- issue creation and Atlassian Document Format (ADF);
- Jira Software Agile board listing;
- MCP `initialize`, `tools/list`, and `tools/call`.

## Updating the Fake Jira API

When Jira adds an API or changes an existing one:

1. Read the current Atlassian API documentation and record the endpoint,
   authentication rules, request schema, response schema, and API version.
2. Add or update a route in `fakeJira.handle`.
3. Capture the request path, method, headers, query, and body in
   `requestRecord` when the contract needs to assert the outgoing request.
4. Return the smallest valid Jira response needed by the production client.
5. Add a production-client or MCP contract assertion that fails without the
   route or schema change.
6. Cover success, authentication failure, HTTP 4xx/5xx, and malformed response
   behavior where the production code handles those cases.
7. Run `make test-e2e` twice before committing.
8. Update this document and the relevant ADR when the API contract changes.

Never copy production Jira data into fixtures. Use unique synthetic keys and
minimal payloads so tests remain deterministic and safe to publish.
