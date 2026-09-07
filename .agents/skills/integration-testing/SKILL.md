---
name: integration-testing
description: Use when adding, modifying, or reviewing unit or integration tests in this project. Defines the test strategy — what to mock, what to run for real against Docker infrastructure (Valkey, queues, filesystem), when to fake an external service at the protocol boundary, regression-test rules for bug fixes, and what may or may not be excluded from coverage. Triggered by changes to handlers, clients, adapters, test files, Makefile test targets, and CI config.
---

# Testing strategy — unit vs integration

## Definitions

**Unit test**: exercises a single component in isolation. Every external
dependency is replaced by a fake, stub, or mock. No real database, cache,
broker, network, or third-party service. This is where edge cases, error
branches, and invariants live.

**Integration test**: exercises the real wiring between components.
Infrastructure that can run locally (Docker) is **real** — never mocked.
Services that cannot run locally are faked **at the protocol boundary**
(a local HTTP/gRPC/SMTP server speaking the real protocol), never by
swapping the concrete client for a fake interface.

## The decision table — where does the dependency run?

| Dependency runs in Docker / locally | Cannot run locally (SaaS, third party) |
|---|---|
| PostgreSQL, Valkey, message brokers, MinIO/S3-compatible, Mailpit, filesystem, the app's own services | Stripe, OAuth/OIDC providers, email relays, any external API |
| **Real** in integration tests. No mocks, no fakes, no in-memory substitutes. | **Unit**: mock the client's interface. **Integration**: fake server at the protocol boundary; the real, production client code runs against it. |

The goal of an integration test is to prove that **the integrated system
works**: real migrations, real queries, real transactions, real serialization
round-trips, real concurrency. A mock standing in for something that could
have run for real proves nothing.

## Unit test rules

1. Mock only what cannot run for real. Cover **every** branch: success,
   validation errors, timeout, retry, invalid/missing response fields,
   4xx, 5xx, partial failure.
2. Deterministic — inject the clock where time matters.
3. Edge cases that are slow or awkward in integration tests belong here.

## Integration test rules

1. Every handler/controller/route added or modified gets an integration test
   through the **real** router/framework against the **real** database.
   A unit test with a fake repository is not sufficient.
2. Every repository/DAO method added or modified gets integration coverage
   against real PostgreSQL (or the project's actual store).
3. Cover: happy path, persistence (read back from the DB, not the response),
   transactions/rollback, authorization (who may, who may not — assert the
   rejection, not just the permission), side effects (audit rows, emitted
   events, cache state), and deterministic-clock scenarios.
4. Integration tests **never** call dev, staging, or production environments.
5. Tests are isolated and repeatable: unique data per run (suffix with a
   generated id, not fixed literals), cleanup per test, safe under `-count=N`
   / repeated runs.
6. Run the suite against a **fresh but used** database repeatedly — a DB that
   has been through many prior runs is what surfaces ordering/pagination/
   uniqueness assumptions.
7. Concurrency-sensitive behavior (claim-once rows, atomic rotation, single
   winner) gets a test firing concurrent requests and asserting exactly-one
   winner.
8. Hybrid flows: real local infra + protocol-level fakes only at the true
   external boundary. This validates the real client (headers, auth,
   serialization, error mapping) against a server that answers like the
   third party — including failure modes (timeout, 500, malformed body).

## Regression rule

Every bug that gets fixed gets a test that **fails without the fix**. When
chasing a reported bug, write the test first and confirm it reproduces the
exact real error (the literal DB/engine error message, not a paraphrase)
before touching implementation code. A test that never failed for the right
reason proves nothing.

## Coverage

Coverage is a lens, not a target to game.

**May be excluded** (entrypoints and assembly, not logic):

- `main`/entrypoints, server bootstrap, DI wiring/mounting
- infrastructure setup scripts, config parsing without business logic
- generated code

**May never be excluded** to raise the percentage:

- business logic, authorization, validation
- handlers/controllers, services, repositories/DAOs, adapters/clients
- error handling and retry logic

The threshold applies to **productive code**, with exclusions declared
explicitly where coverage is configured (Makefile/CI flags such as
`--ignore-filename-regex`, `coveragePathIgnorePatterns`, `-coverpkg`, etc.)
— documented, not silent. A change is not complete until everything added or
modified has appropriate coverage, and the error paths were actually
exercised — passing tests with zero error-branch coverage do not count.

## Patterns (language-neutral)

### Handler → real router → real database

```text
start real dependency (docker compose up postgres valkey)
run migrations against the fresh container
seed minimal fixture data (unique per run)
build the REAL router with REAL middleware, point it at the real store
issue an HTTP request through the router (include auth context)
assert: HTTP status, response body, AND the resulting DB state
```

### Real client → protocol-level fake for an external service

```text
start a local fake server speaking the real protocol (HTTP/gRPC/SMTP)
   - assert the request path, headers, and payload the client sent
   - script the response: success, then timeout, then 500, then malformed
build the PRODUCTION client pointed at the fake's URL
run the flow; assert the client's mapping of each response into domain
   results/errors — this is the code under test
```

## Commands

Use the project's own targets; typical shape:

```bash
make test-unit            # no infrastructure needed
make test-integration     # dependencies up via docker compose first
make test                 # full suite (unit + integration)
make coverage             # with the documented exclusions
```

If the project lacks these targets, add them rather than inventing ad-hoc
commands per task.

## Naming

For cache/queue infrastructure in docs, compose files, and tests, use
**Valkey** (never "Redis") — the org standard name for the
Redis-protocol-compatible server.
