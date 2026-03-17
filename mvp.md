## The Problem

Claude Code makes it easy to spin up parallel coding sessions using Git worktrees.

But once you have more than a few running, the workflow breaks down:

* You lose track of how many sessions exist
* You don't know which ones need input
* You waste time switching into sessions that aren't ready
* Terminal tabs multiply
* Finished worktrees linger after merge

There's no single place to see:

> What's in flight? What's blocked? What needs me right now?

---

## The Vision

Deckard is a **coordinator's dashboard**, not an IC tool.

The agent does the work. The agent raises the MR. The agent writes a
`status.md` when done. **The MR is the terminal artefact** — the human
reviews and merges the MR description, not a half-finished tmux session.

The human's role is:
1. Spin up sessions (new worktree → Claude attaches automatically)
2. Glance at the dashboard to see what's ready or blocked
3. Review and merge MRs
4. Unblock agents when they hit genuine human-only decisions

Deckard enables this loop at fleet scale.

---

## How the Loop Works

1. **Agent completes work** → raises an MR via `/mr` skill
2. **Agent writes `status: needs_review` + `mr_url`** in `status.md`
3. **Deckard surfaces the session** as review-ready (▲)
4. **Human opens review screen** → sees MR description inline
5. **Human presses `o`** → MR opens in browser for review/merge
6. **Human presses `d`** → clean up the worktree after merge

For blocked sessions:
1. **Agent writes `status: blocked`** with clear blockers
2. **Deckard surfaces the session** as blocked (✕), sorted first
3. **Human presses Enter** → attaches to Claude session to unblock

---

## MVP Scope

> Replace "a million terminal tabs" with one operational dashboard.

### 1) Worktree Discovery

Deckard parses `git worktree list --porcelain` and treats each worktree
as an active unit. Task names are inferred from branch/worktree slugs.

### 2) Session Mapping (Convention-based)

One Claude tmux session per worktree; session name matches the slug.

### 3) Status-driven Attention

Sessions surface to the human based **only** on explicit `status.md` signals:

- `needs_review` + `mr_url` → sorted second (▲ READY FOR REVIEW)
- `needs_review` without `mr_url` → sorted first (✕ BLOCKED — agent surfaced prematurely)
- `blocked` → sorted first (✕ BLOCKED)

CI failures, unresolved threads, and tmux idle detection do **not** surface
sessions. Those are agent problems to solve.

### 4) Git Cloud Integration

Deckard auto-detects the git hosting provider:

- **GitLab**: uses `glab` CLI to fetch MR metadata and description
- **GitHub**: uses `gh` CLI to fetch PR metadata and description
- **No provider**: dashboard still works; MR fields show as empty

MR data enriches the dashboard and review screen. Deckard does not create
MRs — that is the agent's responsibility.

### 5) Review Screen

When a session is selected and has agent status:

**For `needs_review`:** shows summary + MR description inline; `o` opens
the MR for review/merge. `Enter` (attach to Claude) is not offered — the
work is done.

**For `blocked`:** shows summary + blockers; `Enter` attaches to the Claude
session. `o` opens the MR if one exists.

### 6) Split-Pane TUI

```
┌─────────────────────────┬────────────────────────────┐
│ WORKTREES               │ JIRA-182-PAYMENT-RETRIES   │
│                         │                            │
│ ✕ JIRA-182 …            │ BRANCH   feat/JIRA-182     │
│ ▲ JIRA-201 …            │ STATUS   ✕ BLOCKED         │
│ / chore-jest …          │                            │
│ · perf-plp …            │ ─── MR ─────────────────── │
│                         │ MR       !4821              │
│                         │ STATE    OPEN               │
│                         │ PIPELINE ◆ PASSED           │
└─────────────────────────┴────────────────────────────┘
↑/↓ navigate  Enter attach  n new  o open MR  d delete  r refresh  q quit
```

### 7) Session Resume

Deckard launches `tmux attach` for the session's window. Returns to the
dashboard when the user detaches.

### 8) Worktree Cleanup (Manual)

`d` deletes the worktree and branch. Shows a warning if the MR is still open;
confirms that it's safe to clean up if the MR is merged.

---

## Non-Goals (for MVP)

* Human-initiated commits (the agent commits)
* Surfacing sessions based on CI failures or unresolved threads
* Background daemons or webhooks
* Jira integration
* CI log parsing
* Multi-repo aggregation

---

## Tech Stack

* **Go** — single static binary
* **Bubble Tea** — TUI framework
* **Bubbles** — list + viewport components
* **Lip Gloss** — styling/layout
* Shell integrations: `git`, `glab` or `gh`, `tmux`

---

## Design Principles

* Coordinator-first: the agent does the IC work, the human reviews outcomes
* Status-driven: only explicit `status.md` signals pull human attention
* MR as terminal artefact: `needs_review` means there is an MR to review
* Local-first, no external services
* Convention over configuration

---

## Future Ideas

* Auto-retire worktrees on MR merge
* Skill encoding flow triggered from review screen
* Multi-repo "fleet view"
* Agent auto-retry on pipeline failure with context from CI logs
