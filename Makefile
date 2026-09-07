SHELL := /bin/sh

# Tooling (override anything on the command line, e.g. make lint GO=mise\ exec\ --\ go).
# Prefer mise when available so local runs match the pinned toolchain; plain go
# also self-corrects via GOTOOLCHAIN=auto + the toolchain directive in go.mod.
ifneq (,$(shell command -v mise 2>/dev/null))
GO ?= mise exec -- go
GOFMT ?= mise exec -- gofmt
GOLANGCI_LINT ?= mise exec -- golangci-lint
else
GO ?= go
GOFMT ?= gofmt
GOLANGCI_LINT ?= golangci-lint
endif
DOCKER ?= docker
GIT_CLIFF ?= git-cliff
COMPOSE := $(DOCKER) compose -f docker-compose.test.yml
VALKEY_URL ?= redis://127.0.0.1:6379
VALKEY_URL_DOCKER ?= redis://host.docker.internal:6379

# Build/publish knobs
VERSION ?= dev
BIN_DIR ?= /tmp
IMAGE_TAG ?= jira-mcp:local
RELEASE_IMAGE ?= goreleaser/goreleaser:latest
PORT ?= 8080
JIRA_MCP_PUBLIC_URL ?= http://127.0.0.1:$(PORT)

# Benchmark knobs (see internal/remote/benchmark_test.go)
BENCH_RUNS ?= 5
BENCH_OPS ?= 800

# Lazily generated so it only runs when a recipe actually needs it.
ENCRYPTION_KEY ?= $(shell openssl rand -base64 32 | tr -d '=\n')

.DEFAULT_GOAL := help

.PHONY: help setup setup-tools deps tidy fmt fmt-check lint vet vuln check quality \
	test-unit test-integration test-e2e test-docs test test-race \
	infra-up infra-down infra-logs infra-ps \
	build build-version run-stdio setup-config run-remote \
	docker-build docker-size docker-run docker-health docker-stop \
	bench-remote-inproc bench-remote-binary bench-remote-docker \
	release-check changelog clean

## ---------- Getting started ----------

help:
	@echo "jira-mcp — main targets (see Makefile for env knobs):"
	@echo ""
	@echo "  make setup          install toolchain + project dependencies (mise, go modules, pnpm)"
	@echo "  make setup-tools    add/refresh go tools in go.mod (edits go.mod/go.sum: commit after)"
	@echo "  make deps           download go module dependencies"
	@echo "  make tidy           go mod tidy"
	@echo ""
	@echo "  make fmt            format go files (gofmt -w)"
	@echo "  make fmt-check      fail if gofmt finds formatting issues"
	@echo "  make lint           golangci-lint run ./..."
	@echo "  make vet            go vet ./..."
	@echo "  make vuln           go tool govulncheck ./..."
	@echo "  make check          fmt-check + lint + build + vet + vuln"
	@echo "  make quality        check + full test suite (the commit gate)"
	@echo ""
	@echo "  make test           unit + integration (real Valkey) + e2e + docs build"
	@echo "  make test-unit      go test ./..."
	@echo "  make test-integration  integration tests against real Valkey (compose)"
	@echo "  make test-e2e       e2e contract tests (stdio, cloud + data center)"
	@echo "  make test-docs      build docs-site (pnpm)"
	@echo "  make test-race      go test -race ./..."
	@echo ""
	@echo "  make infra-up       start Valkey (docker compose)"
	@echo "  make infra-down     stop Valkey and remove volumes"
	@echo "  make infra-logs     tail Valkey logs"
	@echo "  make infra-ps       show compose status"
	@echo ""
	@echo "  make build          go build ./..."
	@echo "  make build-version  build ./bin/jira-mcp with -ldflags version"
	@echo "  make setup-config   interactive config wizard (jira-mcp setup)"
	@echo "  make run-stdio      run the MCP server in stdio mode"
	@echo "  make run-remote     run the remote/OAuth server locally (needs Atlassian creds)"
	@echo ""
	@echo "  make docker-build   build $(IMAGE_TAG)"
	@echo "  make docker-size    show image size"
	@echo "  make docker-run     run the image locally (rootless docker: Valkey must be a container)"
	@echo "  make docker-health  curl /health of the local container"
	@echo "  make docker-stop    remove the local container"
	@echo ""
	@echo "  make bench-remote-inproc   remote benchmark, server in-process (pprof)"
	@echo "  make bench-remote-binary   remote benchmark, production binary subprocess"
	@echo "  make bench-remote-docker   remote benchmark, $(IMAGE_TAG)-like container"
	@echo "                              knobs: BENCH_RUNS=$(BENCH_RUNS) BENCH_OPS=$(BENCH_OPS)"
	@echo ""
	@echo "  make release-check  validate .goreleaser.yaml (dockerized goreleaser)"
	@echo "  make changelog      regenerate CHANGELOG.md (GIT_CLIFF=$(GIT_CLIFF))"
	@echo "  make clean          remove local build artifacts"

setup:
	@if command -v mise >/dev/null 2>&1; then mise install; else echo "mise not found; skipping (install from https://mise.jdx.dev or pin go manually)"; fi
	$(GO) mod download
	@if command -v pnpm >/dev/null 2>&1; then $(MAKE) --no-print-directory test-docs || true; else echo "pnpm not found; skipping docs-site dependencies (https://pnpm.io)"; fi

# go get -tool rewrites go.mod/go.sum on purpose; commit the result.
setup-tools:
	$(GO) get -tool golang.org/x/vuln/cmd/govulncheck@latest
	$(GO) mod tidy

deps:
	$(GO) mod download

tidy:
	$(GO) mod tidy

## ---------- Quality ----------

fmt:
	$(GOFMT) -l -w .

fmt-check:
	@if $(GOFMT) -l . | grep -q .; then echo "files need formatting:"; $(GOFMT) -l .; echo "run 'make fmt'"; exit 1; fi

lint:
	$(GOLANGCI_LINT) run ./...

vet:
	$(GO) vet ./...

vuln:
	$(GO) tool govulncheck ./...

check: fmt-check lint build vet vuln

quality: check test

## ---------- Tests ----------

test-unit:
	$(GO) test -count=1 ./...

test-integration:
	set -e; trap '$(COMPOSE) down -v' EXIT; $(COMPOSE) up -d --wait; VALKEY_URL=$(VALKEY_URL) $(GO) test -tags=integration -count=1 ./...

test-e2e:
	$(GO) build -o $(BIN_DIR)/jira-mcp-e2e ./cmd/jira-mcp
	JIRA_MCP_BINARY=$(BIN_DIR)/jira-mcp-e2e $(GO) test -tags=e2e -count=1 ./tests/e2e

test-docs:
	cd docs-site && pnpm install --frozen-lockfile && pnpm build

test: test-unit test-integration test-e2e test-docs

test-race:
	$(GO) test -race -count=1 ./...

## ---------- Local infrastructure ----------

infra-up:
	$(COMPOSE) up -d --wait

infra-down:
	$(COMPOSE) down -v

infra-logs:
	$(COMPOSE) logs -f

infra-ps:
	$(COMPOSE) ps

## ---------- Build and run ----------

build:
	$(GO) build ./...

build-version:
	$(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/jira-mcp ./cmd/jira-mcp

setup-config:
	$(GO) run ./cmd/jira-mcp setup

run-stdio:
	$(GO) run ./cmd/jira-mcp

# Needs real Atlassian OAuth credentials. Sessions do not survive restarts
# when ENCRYPTION_KEY is left empty (a fresh key is generated per run).
run-remote:
	@if [ -z "$(JIRA_MCP_ATLASSIAN_CLIENT_ID)" ] || [ -z "$(JIRA_MCP_ATLASSIAN_CLIENT_SECRET)" ]; then \
		echo "error: run with JIRA_MCP_ATLASSIAN_CLIENT_ID=... JIRA_MCP_ATLASSIAN_CLIENT_SECRET=..."; exit 1; fi
	$(COMPOSE) up -d --wait
	VALKEY_URL=$(VALKEY_URL) HOST=127.0.0.1 PORT=$(PORT) \
	JIRA_MCP_PUBLIC_URL=$(JIRA_MCP_PUBLIC_URL) \
	JIRA_MCP_ATLASSIAN_CLIENT_ID=$(JIRA_MCP_ATLASSIAN_CLIENT_ID) \
	JIRA_MCP_ATLASSIAN_CLIENT_SECRET=$(JIRA_MCP_ATLASSIAN_CLIENT_SECRET) \
	JIRA_MCP_ENCRYPTION_KEY=$(ENCRYPTION_KEY) \
	$(GO) run ./cmd/jira-mcp remote

## ---------- Container (image used by Railway) ----------

docker-build:
	$(DOCKER) build -t $(IMAGE_TAG) .
	@$(MAKE) --no-print-directory docker-size

docker-size:
	@$(DOCKER) image ls $(IMAGE_TAG) --format "{{.Repository}}:{{.Tag}} {{.Size}}"

# NOTE: on rootless docker the container cannot reach host processes; run
# Valkey inside docker and point VALKEY_URL_DOCKER at it.
docker-run:
	@if [ -z "$(JIRA_MCP_ATLASSIAN_CLIENT_ID)" ] || [ -z "$(JIRA_MCP_ATLASSIAN_CLIENT_SECRET)" ]; then \
		echo "error: run with JIRA_MCP_ATLASSIAN_CLIENT_ID=... JIRA_MCP_ATLASSIAN_CLIENT_SECRET=..."; exit 1; fi
	-@$(MAKE) --no-print-directory docker-stop
	$(DOCKER) run -d --name jira-mcp-local -p 127.0.0.1:$(PORT):18082 \
		-e PORT=18082 -e HOST=0.0.0.0 \
		-e VALKEY_URL=$(VALKEY_URL_DOCKER) \
		-e JIRA_MCP_PUBLIC_URL=$(JIRA_MCP_PUBLIC_URL) \
		-e JIRA_MCP_ATLASSIAN_CLIENT_ID=$(JIRA_MCP_ATLASSIAN_CLIENT_ID) \
		-e JIRA_MCP_ATLASSIAN_CLIENT_SECRET=$(JIRA_MCP_ATLASSIAN_CLIENT_SECRET) \
		-e JIRA_MCP_ENCRYPTION_KEY=$(ENCRYPTION_KEY) \
		$(IMAGE_TAG)
	@sleep 1
	@$(MAKE) --no-print-directory docker-health

docker-health:
	@curl -sS http://127.0.0.1:$(PORT)/health && echo

docker-stop:
	@$(DOCKER) rm -f jira-mcp-local 2>/dev/null || true

## ---------- Benchmarks (remote mode; see internal/remote/benchmark_test.go) ----------

bench-remote-inproc:
	$(GO) test -c -tags=benchmark -o $(BIN_DIR)/remote-bench.test ./internal/remote
	set -e; trap '$(COMPOSE) down -v' EXIT; $(COMPOSE) up -d --wait; \
	JIRA_MCP_BENCH=1 JIRA_MCP_BENCH_MODE=inproc JIRA_MCP_BENCH_PPROF=1 \
	JIRA_MCP_BENCH_RUNS=$(BENCH_RUNS) JIRA_MCP_BENCH_OPS=$(BENCH_OPS) \
	VALKEY_URL=$(VALKEY_URL) \
	$(BIN_DIR)/remote-bench.test -test.run TestRemoteBenchmark -test.v -test.timeout=45m

bench-remote-binary:
	$(GO) test -c -tags=benchmark -o $(BIN_DIR)/remote-bench.test ./internal/remote
	$(GO) build -trimpath -ldflags "-s -w" -o $(BIN_DIR)/jira-mcp-bench ./cmd/jira-mcp
	set -e; trap '$(COMPOSE) down -v' EXIT; $(COMPOSE) up -d --wait; \
	JIRA_MCP_BENCH=1 JIRA_MCP_BENCH_MODE=binary JIRA_MCP_BENCH_BINARY=$(BIN_DIR)/jira-mcp-bench \
	JIRA_MCP_BENCH_RUNS=$(BENCH_RUNS) JIRA_MCP_BENCH_OPS=$(BENCH_OPS) \
	VALKEY_URL=$(VALKEY_URL) \
	$(BIN_DIR)/remote-bench.test -test.run TestRemoteBenchmark -test.v -test.timeout=45m

bench-remote-docker:
	$(GO) test -c -tags=benchmark -o $(BIN_DIR)/remote-bench.test ./internal/remote
	$(DOCKER) build -t jira-mcp:bench .
	JIRA_MCP_BENCH=1 JIRA_MCP_BENCH_MODE=docker JIRA_MCP_BENCH_IMAGE=jira-mcp:bench \
	JIRA_MCP_BENCH_RUNS=$(BENCH_RUNS) JIRA_MCP_BENCH_OPS=$(BENCH_OPS) \
	$(BIN_DIR)/remote-bench.test -test.run TestRemoteBenchmark -test.v -test.timeout=45m

## ---------- Release ----------

release-check:
	$(DOCKER) run --rm -v $$(pwd):/workspace -w /workspace $(RELEASE_IMAGE) check

changelog:
	$(GIT_CLIFF) --output CHANGELOG.md

clean:
	rm -f $(BIN_DIR)/jira-mcp-e2e $(BIN_DIR)/jira-mcp-bench $(BIN_DIR)/remote-bench.test
	rm -rf bin docs-site/dist docs-site/.astro
