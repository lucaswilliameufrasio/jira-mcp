# Feature: Jira Cloud Issue Update ADF

## Scope

Keep issue update payloads compatible with Atlassian Document Format (ADF) on
Jira Cloud while preserving Jira Server/Data Center behavior.

## Confirmed Rules

- A plain-text `description` update is converted to ADF on Jira Cloud, matching
  issue creation. Jira Server/Data Center continues to receive plain text.
- An explicitly supplied ADF document is preserved and accepted for `description`
  and custom fields, even when edit metadata describes a custom field as a
  `string`.
- Plain strings in custom fields remain unchanged. Their metadata does not
  reliably distinguish ordinary text from rich-text fields, so conversion is
  not inferred for all string-typed custom fields.

## Local Decisions

- Treat an object with ADF document shape (`type: doc`, `version: 1`, and array
  `content`) as ADF for validation. Other object values continue to be checked
  against Jira metadata normally.

## Open Questions

- None.

## Dependencies

- Jira Cloud rich-text fields must accept valid ADF documents for issue updates.
