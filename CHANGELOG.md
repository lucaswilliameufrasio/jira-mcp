# Changelog

All notable changes to Jira MCP will be documented in this file.

## [0.7.0] - 2026-09-26

### Features

- *(cli)* Improve Jira MCP setup and client onboarding

## [0.6.5] - 2026-09-23

### Bug Fixes

- *(jira)* Handle ADF for cloud issue updates (#14)


### Chores

- *(release)* Prepare for v0.6.5


### Other

- Merge pull request #15 from lucaswilliameufrasio/release/v0.6.5

Release v0.6.5

## [0.6.4] - 2026-09-19

### Bug Fixes

- Respect Jira Cloud metadata page limits


### Chores

- *(release)* Prepare for v0.6.4


### Other

- Merge pull request #12 from lucaswilliameufrasio/bugfix/cloud-create-metadata-page-limit

fix: respect Jira Cloud metadata page limits

- Merge pull request #13 from lucaswilliameufrasio/release/v0.6.4

Release v0.6.4

## [0.6.3] - 2026-09-19

### Bug Fixes

- Use current Jira Cloud create metadata APIs


### Chores

- *(release)* Prepare for v0.6.3


### Other

- Merge pull request #10 from lucaswilliameufrasio/bugfix/cloud-create-issue-metadata

fix: use current Jira Cloud create metadata APIs

- Merge pull request #11 from lucaswilliameufrasio/release/v0.6.3

Release v0.6.3

## [0.6.2] - 2026-09-19

### Bug Fixes

- *(cli)* Exit after printing version


### Chores

- *(release)* Prepare for v0.6.2


### Other

- Merge pull request #9 from lucaswilliameufrasio/release/v0.6.2

Release v0.6.2

## [0.6.1] - 2026-09-17

### Bug Fixes

- Expose issue ranking tools in selection


### Chores

- *(release)* Prepare for v0.6.1


### Other

- Merge pull request #8 from lucaswilliameufrasio/release/v0.6.1

Release v0.6.1

## [0.6.0] - 2026-09-17

### Chores

- *(release)* Prepare for v0.6.0


### Features

- Add Jira comment and card ranking tools

## [0.5.0] - 2026-09-12

### Chores

- *(release)* Prepare for v0.5.0 (#7)


### Features

- Add profile and client selectors to setup tui

## [0.4.1] - 2026-09-11

### Bug Fixes

- Pass terminal input directly to setup tui


### Chores

- *(release)* Prepare for v0.4.1 (#6)

## [0.4.0] - 2026-09-11

### Chores

- *(release)* Prepare for v0.4.0 (#5)


### Features

- Improve setup and Jira validation

## [0.3.0] - 2026-09-10

### Chores

- *(release)* Prepare for v0.3.0 (#4)


### Features

- Add field metadata tool

## [0.2.0] - 2026-09-10

### Chores

- *(release)* Prepare for v0.2.0 (#3)


### Features

- Support custom fields and issue links

## [0.1.6] - 2026-09-10

### Bug Fixes

- Read installer confirmation from terminal

## [0.1.5] - 2026-09-10

### Features

- Add setup client discovery and doctor

## [0.1.4] - 2026-09-10

### Bug Fixes

- Make piped installer safe

## [0.1.3] - 2026-09-10

### Bug Fixes

- Make release installer extract archives


### Testing

- Add coverage target and documentation

## [0.1.2] - 2026-09-10

### CI / Build

- Pin actions to SHA and update to latest majors


### Documentation

- Replace Astro favicon with project brand mark

- Add social preview metadata and image

- Add concise self-hosted remote setup guide


### Features

- Add release installers

## [0.1.1] - 2026-09-10

### Bug Fixes

- Wizard accepts tool names so Enter keeps the current selection


### Chores

- *(release)* Prepare for v0.1.1 (#2)


### Documentation

- Position remote mode as self-hosted

## [0.1.0] - 2026-09-07

### Bug Fixes

- Pin toolchain, use distroless runtime and configurable bind address


### CI / Build

- Fix docs-site job (pnpm 11 requires Node >= 22.13)


### Chores

- Pin go toolchain to 1.27.1 with mise

- Add govulncheck as go tool

- Add full Makefile dev workflow and pin go toolchain to 1.27.1

- Add MIT license, community policies, issue templates and expand CI

- *(release)* Prepare for v0.1.0 (#1)


### Features

- Establish Jira MCP server

- Add docs site, e2e contract tests and release pipeline

- Add remote benchmark harness and honor AtlassianAPIURL override

- Harden remote mode with revoke, single-use oauth state and rate limiting


### Refactor

- Move entrypoint to cmd and add railway deployment


### Testing

- Add unit integration and e2e coverage

