---
name: document-decisions
description: >-
  Enforce that every design decision is recorded before implementation.
  Use BEFORE writing any non-trivial feature, refactor, or integration.
  Also use when the conversation surfaces an unresolved design choice,
  when a previous decision needs revisiting, or when the user asks
  "did we decide X?" or "where is Y documented?".
  Do NOT use for: trivial bugfixes, dependency bumps, CI changes,
  or cosmetic-only changes. "If you had to think about it, it needs a record."
---

# Document Decisions

Every non-trivial choice must leave a trace. If a future developer
(or future you) could ask "why did we do it this way?", the decision
should be findable in a file.

## Where decisions live

| Document | Purpose | Location | Lifecycle |
|----------|---------|----------|-----------|
| **ADR** | Architectural/跨-cutting decisions that are hard to reverse | `docs/adr/<NNN>-<slug>.md` | Permanent — superseded ADRs get `Status: Superseded by ADR-NNN` |
| **Feature doc** | Local decisions, confirmed rules, open questions for ONE feature | `docs/features/<feature>.md` | Updated as feature evolves; deleted when feature is removed |
| **CONTEXT.md** | Domain glossary only (terms, entities, data flow) | `./CONTEXT.md` | Updated when domain language changes |
| **AGENTS.md** | Coding conventions and toolchain rules | `.agents/AGENTS.md` | Updated when project-level conventions change |

## What each document contains

### ADR (docs/adr/<NNN>-<slug>.md)

```markdown
# ADR-NNN: Title

**Date:** YYYY-MM-DD
**Status:** Accepted | Deprecated | Superseded by ADR-NNN
**Evidence:** [link to issue, spike, experiment, or discussion]

## Context

Why was this decision needed? What constraints or trade-offs were at play?

## Decision

What we decided. Include concrete details — tool, version, config key,
file path. Avoid vague language.

## Alternatives Considered

- **Alternative A**: (what and why rejected)
- **Alternative B**: (what and why rejected)

## Consequences

- What becomes easier
- What becomes harder
- What must be cleaned up later (if anything)

## Review Condition

When should this decision be revisited? (e.g., "when the backend
localizes event_types", "when we add auth", "never — this is
permanent")
```

### Feature doc (docs/features/<feature>.md)

```markdown
# Feature: <name>

## Scope

What is in and out of scope for this feature.

## Confirmed Rules

- Rule 1 (with source: ADR-NNN, API contract, user request)
- Rule 2

## Local Decisions

Decisions that affect only this feature and don't warrant an ADR.

- **Decision**: description. **Why**: reasoning. **Source**: who decided.

## Open Questions

- [ ] Question 1 — blocking on: X
- [ ] Question 2 — blocking on: Y

## Dependencies

- What must exist before this feature works.
```

## Process

1. **Before implementing**, ask: does this involve a choice between
   two or more valid approaches? If yes, find the right document above.
2. If the document doesn't exist, create it. If the section doesn't exist,
   add it.
3. Record the decision with rationale and alternatives.
4. Only then write code.

## When to elevate to ADR

A decision belongs in an ADR (not just a feature doc) when it:
- Affects multiple features or the whole project
- Is expensive to reverse
- Involves a framework, library, or integration point
- Changes the build, test, or deploy pipeline
- Introduces a new data flow or architectural boundary

## Maintenance

- When revisiting a decision, read its ADR/feature doc first.
- If reversing an ADR, create a new ADR superseding the old one.
  Do not edit the old ADR in-place (history matters).
- Update feature docs when rules change during implementation.
- Archive feature docs when their feature is removed.
