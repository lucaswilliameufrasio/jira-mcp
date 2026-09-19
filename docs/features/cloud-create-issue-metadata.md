# Feature: Reliable Cloud Issue Creation

## Scope

Make `jira_create_issue` work with current Jira Cloud create-metadata APIs,
including issue types whose display names are localized, while preserving the
legacy metadata path for Jira Server/Data Center.

## Confirmed Rules

- Jira Cloud create metadata is resolved through the project-scoped issue type
  and field endpoints. Source: current Atlassian REST API v3 documentation.
- The create payload uses project and issue type IDs when they can be resolved;
  this avoids relying on project keys and localized issue type names.
- Jira Server/Data Center keeps the existing `/issue/createmeta` flow because
  this repository supports both API families.
- Epic association remains represented by the Jira `parent` field in
  `extra_fields`, or by the existing `jira_set_issue_epic` tool after creation.

## Local Decisions

- Resolve metadata once in the MCP handler and pass the resolved IDs to the
  client. This avoids a second metadata request and keeps validation and the
  create payload consistent.
- Match an issue type by ID first when the input is an ID, otherwise by name;
  names are compared case-insensitively for usability.

## Open Questions

- [ ] Whether this project wants a dedicated `epic_key` argument in a future
  tool revision instead of requiring `extra_fields.parent`.

## Dependencies

- Jira Cloud permissions and scopes must allow reading create metadata and
  creating issues in the target project.
