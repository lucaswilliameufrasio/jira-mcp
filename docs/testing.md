# Testing

## Full Suite

Run the complete project test gate:

```bash
make test
```

This runs unit tests, integration tests against real Valkey, E2E contract tests,
and the documentation build.

The development and release toolchain is Go 1.27.3 (`mise.toml`, `go.mod`,
and the Docker build stage). The module's `go` directive remains 1.26.0; the
toolchain patch update addresses the Go standard-library security advisories
reported against 1.27.1. See the [Go release history](https://go.dev/doc/devel/release).

## Operational console verification

`TestBinarySpeaksMCPOverStdio` starts the real stdio binary with a synthetic Jira
HTTP server, invokes a tool over MCP, and verifies that its local audit event
appears on the console pages. `TestBrowserOAuthWithValkey` exercises the remote
OAuth and site-selection flow with synthetic Atlassian endpoints and real local
Valkey, then checks the three authenticated pages and logout with an HTTP cookie
client. `TestConsolePagesUseOnlyAuthenticatedUsersAudit` checks account
isolation and the audit-unavailable state.

These tests use HTTP clients, not a browser. Desktop Firefox inspection has
also been performed on synthetic server-rendered component snapshots; it does
not verify the running application's browser navigation, responsive behavior,
or login/logout interactions. Subsequent browser checks are described below.

The current sandbox attempt could not complete browser verification: the
available Firefox DevTools connection controls a browser outside the isolated
test network, while a separate headless Firefox invocation inside that network
reported `EPERM` setting up its user namespace and produced no screenshot. The
DevTools connector also exposes no viewport-resize operation. A successful
verification needs a controllable Firefox in the same isolated network as the
synthetic services, with desktop and mobile viewports; do not count the HTTP
client tests or static component screenshots as that verification.
An additional headless attempt with a fresh synthetic profile, temporary HOME,
loopback-only network namespace, and the non-root mapped user initialized its
profile but produced no screenshot despite a zero exit status. Safe mode timed
out. These results do not identify the root cause or establish browser support.
Firefox's built-in Marionette endpoint was subsequently reached on isolated
loopback and reported protocol version 3 without `geckodriver`. However,
`WebDriver:NewSession` timed out, including after a higher virtual-address
limit, and the synthetic Firefox log again reported `EPERM` while writing
`uid_map`. This proves transport availability only, not a usable browser
session or application navigation. No system permissions or kernel settings
were changed. A subsequent WebDriver BiDi test in the isolated namespace did
complete the WebSocket upgrade and `session.new` returned success with Firefox
158.0.1. A follow-up synthetic HTTP probe successfully called
`browsingContext.getTree`, but `browsingContext.setViewport` returned
`no such frame` because the browsing context had been discarded. Firefox
logged `uid_map` failures and child-process crashes. Navigation, screenshots
and responsive layouts were not verified by that attempt; a BiDi session alone
does not establish that the browser can render a page. A further attempt to
create a fresh tab with `browsingContext.create` before navigating or resizing
also returned `no such frame`, accompanied by child-process crashes and
`uid_map` permission errors.

An independent reachability probe bound a one-request synthetic HTTP server to
host `127.0.0.1`; the available Firefox DevTools browser loaded it and its
accessibility snapshot contained `synthetic only`. This demonstrates host
loopback connectivity, not access to services inside the isolated namespace.
A controlled loopback bridge to synthetic services is a possible next test;
it must not expose real Jira credentials or endpoints. The connector still has
no documented viewport-resize operation.

An attempt to use keyboard zoom in the running stdio console did not change
the captured viewport or trigger the mobile layout. It is not evidence of
mobile responsiveness.

A separate headless Firefox 158 instance with a temporary profile and BiDi
viewport control subsequently loaded the real stdio console and a remote
synthetic OAuth fixture at 390×844 CSS pixels. Overview, monitoring and audit
loaded in both modes; `window.innerWidth` was 390 and document scroll width
378 in each case. The remote audit contained a seeded synthetic event. Visual
captures show that the navigation and audit table use internal horizontal
scrolling, so not all items and columns are visible simultaneously. The
remote fixture used in-memory auth and a local audit file; this check did not
exercise touch input or validate other viewport sizes.

A subsequent desktop Firefox run accessed the actual stdio binary's optional
loopback console, launched with a clean synthetic configuration and a loopback
Jira URL. Browser clicks navigated overview → monitoring → audit, and the
audit page's empty state was visually inspected. No MCP tool was invoked in
that browser session. This does not establish remote login/logout, populated
audit rendering, or mobile responsiveness.

A later desktop Firefox run used a temporary loopback-only test fixture with
the real remote server handlers, a synthetic OAuth provider and a seeded
synthetic audit event. Login and site selection succeeded, and navigation to
overview, monitoring and audit displayed the seeded `jira_search` event.
The original logout form returned `forbidden` because Firefox sent `Origin:
null` from a `no-referrer` page. After changing the console page policy to
`same-origin`, Firefox repeated the synthetic OAuth and site selection, opened
overview, audit and monitoring, and reached “Sessão encerrada” via logout.
Exact Origin and CSRF checks remain in place. This browser fixture used an
in-memory auth store and a local audit file, not production Valkey. The
separate mobile-viewport check is described above.

The repository's `make quality` gate subsequently passed with Go 1.27.3,
explicit installed tool paths, a clean environment and a dedicated rootless
Compose project on an isolated test port. It ran formatting checks, lint,
build, vet, govulncheck, unit tests, real-Valkey integration tests, stdio E2E,
and the documentation install/build. This does not replace browser validation.

The rootless Docker daemon successfully built the actual Dockerfile after the
two public base images and Go modules were downloaded. A subsequent build with
`docker --context rootless build --network=none -t jira-mcp:verification-offline .`
failed at `go mod download`: BuildKit did not reuse the network-enabled layer
for the network-disabled instruction. `--network=none` restricts build RUN
instructions, not necessarily registry metadata resolution. Therefore neither
the successful network-enabled build nor the local base images prove that a
clean offline build works. A separate test supplied a derived build-stage image
containing a local module proxy from the host cache, with `GOPROXY=file://...`
and `GOSUMDB=off`, through BuildKit's `--build-context` override. With that
substituted build-stage base, the unchanged Dockerfile's `go mod download` and
Go build both ran successfully with `--network=none`; the resulting image
reported `jira-mcp dev` when run without network. This verifies the workaround,
not a stock-base fully offline build or online checksum-database verification.
Image metadata resolution was not isolated from the network.
As a separate prerequisite check, `go mod download` succeeded with network
disabled against the host's existing read-only module cache. That does not
populate Docker's build-stage cache or constitute an image build.

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
