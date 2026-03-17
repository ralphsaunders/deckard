# Deckard — Claude Code Instructions

## Handoff Protocol

Before resurfacing to the human for review, or when blocked and unable to proceed, always write:

```
.claude/sessions/<slug>/status.md
```

with the following YAML frontmatter:

```yaml
---
status: needs_review | blocked | working
summary: "one-paragraph summary of what was done"
uncertainty:
  - "open question the agent has"
blockers:
  - "hard blocker preventing completion"
---
```

**Field guidance:**
- `status: needs_review` — work is complete and ready for human review
- `status: blocked` — a hard blocker prevents progress; human input required
- `status: working` — still in progress (write this to update summary mid-task)
- `summary` — one paragraph, present tense, describing what was accomplished
- `uncertainty` — open questions the human should consider (omit if none)
- `blockers` — concrete blockers requiring human action (omit if none)

**The slug** is the normalised branch name: lowercase, with `/` replaced by `-`.
For example, branch `feat/JIRA-123-payment-retries` → slug `feat-JIRA-123-payment-retries`.

Deckard reads this file to surface structured status in its dashboard, sort sessions by urgency, and present a review screen before the human attaches.
