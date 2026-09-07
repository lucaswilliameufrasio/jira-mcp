# Releasing Jira MCP

Jira MCP follows Semantic Versioning and publishes tags as `vX.Y.Z`.

## Automated Release Preparation

1. Open **Actions -> Prepare Release -> Run workflow** on `main`.
2. Enter a version such as `0.2.0` or `v0.2.0`.
3. The workflow validates SemVer, generates `CHANGELOG.md`, creates a
   `release/vX.Y.Z` branch, and opens a pull request.
4. Review and merge the release pull request.
5. Pull the updated `main` branch and create the tag:

```bash
git checkout main
```

The tag starts the release workflow.

## Release Workflow

The release workflow:

- runs the unit, integration, E2E, and documentation suites;
- builds Linux, macOS, and Windows binaries for `amd64` and `arm64`;
- injects the tag version with Go linker flags;
- creates archives and SHA256 checksums;
- publishes a GitHub Release with generated notes.

The binary reports `dev` for local builds and the release version for tagged
builds.

## Required GitHub Configuration

- Actions must be allowed to write repository contents.
- The `release` environment should require an approval before release
  preparation is allowed to push a branch and open a PR.
- Branch protection should require the test workflow before merging.

## Documentation Site

The Astro site deploys independently through Cloudflare Pages from `main`.
See [`cloudflare-pages.md`](deployment/cloudflare-pages.md) for its static
build settings and the `docs-site/**` watch path. A Go-only release does not
need a separate site deployment.

## Manual Checks

Before starting a release, verify:

- the working tree is clean;
- the release branch is based on current `main`;
- `make test` passes;
- `go test -race -count=1 ./...` passes;
- no credentials are present in the changelog or artifacts.
