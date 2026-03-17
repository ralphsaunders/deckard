# Skill: Handoff Protocol

Write a structured status file before resurfacing to the human.

## When to use

- You have completed the assigned work and want the human to review it
- You are blocked by something requiring human input or decision
- You want to update the human on progress mid-task

## Instructions

Write `.claude/sessions/<slug>/status.md` with this content:

```yaml
---
status: needs_review
summary: "Concise one-paragraph summary of what was accomplished. Use present-perfect tense (e.g. 'Added X, refactored Y, fixed Z')."
mr_url: "https://gitlab.com/org/repo/-/merge_requests/123"
uncertainty:
  - "Any open question the human should consider"
blockers:
  - "Any hard blocker preventing completion"
---
```

Replace `<slug>` with the normalised branch name (lowercase, `/` → `-`).

Set `status` to:
- `needs_review` — work is done, MR raised, ready for human review
- `blocked` — cannot proceed without human input
- `working` — still in progress (update summary periodically)

**`mr_url` is required for `needs_review`.** Raise an MR before writing this
status. Deckard treats `needs_review` without an `mr_url` as `blocked` —
the session will surface as a blocker, not a review candidate.

Omit `uncertainty` and `blockers` sections if there are none.

## Why

Deckard reads this file to surface structured status in its TUI dashboard.

- `needs_review` with `mr_url` → review screen shows the MR description inline;
  `o` opens the MR in the browser. The human reviews and merges; no Claude
  attach needed.
- `blocked` → review screen shows blockers; `Enter` attaches to the Claude
  session so the human can unblock the agent.

Sessions with `blocked` status sort to the top of the list; `needs_review`
sorts second.
