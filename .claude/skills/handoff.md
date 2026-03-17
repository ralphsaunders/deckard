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
uncertainty:
  - "Any open question the human should consider"
blockers:
  - "Any hard blocker preventing completion"
---
```

Replace `<slug>` with the normalised branch name (lowercase, `/` → `-`).

Set `status` to:
- `needs_review` — work is done, ready for human review
- `blocked` — cannot proceed without human input
- `working` — still in progress (update summary periodically)

Omit `uncertainty` and `blockers` sections if there are none.

## Why

Deckard reads this file to surface structured status in its TUI dashboard. When the human selects a session and presses Enter, Deckard shows a review screen with the summary, uncertainties, and blockers before offering to attach to the tmux session. Sessions with `blocked` status sort to the top of the list.
