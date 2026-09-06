SHELL := /bin/sh

test-unit:
	go test -count=1 ./...

test-integration:
	set -e; trap 'docker compose -f docker-compose.test.yml down -v' EXIT; docker compose -f docker-compose.test.yml up -d --wait; VALKEY_URL=redis://127.0.0.1:6379 go test -tags=integration -count=1 ./...

test-e2e:
	go build -o /tmp/jira-mcp-e2e .
	JIRA_MCP_BINARY=/tmp/jira-mcp-e2e go test -tags=e2e -count=1 ./tests/e2e

test: test-unit test-integration test-e2e

build:
	go build ./...
