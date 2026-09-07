SHELL := /bin/sh

.PHONY: test-unit test-integration test-e2e test-docs test build bench-remote-inproc bench-remote-binary bench-remote-docker

test-unit:
	go test -count=1 ./...

test-integration:
	set -e; trap 'docker compose -f docker-compose.test.yml down -v' EXIT; docker compose -f docker-compose.test.yml up -d --wait; VALKEY_URL=redis://127.0.0.1:6379 go test -tags=integration -count=1 ./...

test-e2e:
	go build -o /tmp/jira-mcp-e2e ./cmd/jira-mcp
	JIRA_MCP_BINARY=/tmp/jira-mcp-e2e go test -tags=e2e -count=1 ./tests/e2e

test-docs:
	cd docs-site && pnpm install --frozen-lockfile && pnpm build

test: test-unit test-integration test-e2e test-docs

build:
	go build ./...

bench-remote-inproc:
	go test -c -tags=benchmark -o /tmp/remote-bench.test ./internal/remote
	set -e; trap 'docker compose -f docker-compose.test.yml down -v' EXIT; docker compose -f docker-compose.test.yml up -d --wait; \
	JIRA_MCP_BENCH=1 JIRA_MCP_BENCH_MODE=inproc JIRA_MCP_BENCH_PPROF=1 VALKEY_URL=redis://127.0.0.1:6379 \
	/tmp/remote-bench.test -test.run TestRemoteBenchmark -test.v -test.timeout=45m

bench-remote-binary:
	go test -c -tags=benchmark -o /tmp/remote-bench.test ./internal/remote
	go build -trimpath -ldflags "-s -w" -o /tmp/jira-mcp-bench ./cmd/jira-mcp
	set -e; trap 'docker compose -f docker-compose.test.yml down -v' EXIT; docker compose -f docker-compose.test.yml up -d --wait; \
	JIRA_MCP_BENCH=1 JIRA_MCP_BENCH_MODE=binary JIRA_MCP_BENCH_BINARY=/tmp/jira-mcp-bench VALKEY_URL=redis://127.0.0.1:6379 \
	/tmp/remote-bench.test -test.run TestRemoteBenchmark -test.v -test.timeout=45m

bench-remote-docker:
	go test -c -tags=benchmark -o /tmp/remote-bench.test ./internal/remote
	docker build -t jira-mcp:bench .
	JIRA_MCP_BENCH=1 JIRA_MCP_BENCH_MODE=docker JIRA_MCP_BENCH_IMAGE=jira-mcp:bench \
	/tmp/remote-bench.test -test.run TestRemoteBenchmark -test.v -test.timeout=45m
