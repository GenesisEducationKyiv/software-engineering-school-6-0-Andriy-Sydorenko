# Architecture Decision Records

## Naming

Format:
- `XXX-decision-topic.md`

**Name the decision, not the chosen answer.** A title like `001-use-postgresql.md` bakes the conclusion into the filename; if the decision is later superseded, the name lies. Prefer titles that describe the *question* the ADR resolves, so the topic stays stable across revisions.

Examples:
- ✅ `001-primary-datastore.md` - the question is "which primary datastore?"
- ✅ `002-release-detection-strategy.md` - the question is "how do we detect releases?"
- ❌ `001-postgresql-storage.md` - answer in the filename
- ❌ `002-github-api-polling.md` - answer in the filename

## Statuses

- Proposed
- Accepted
- Rejected
- Deprecated
- Superseded

## Template

Use:
- TEMPLATE.md
