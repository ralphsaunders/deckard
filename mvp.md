## The Problem

Claude Code makes it easy to spin up parallel coding sessions using Git worktrees.

But once you have more than a few running, the workflow breaks down:

* You lose track of how many sessions exist
* You don’t know which ones need input
* MRs and CI failures get missed
* Terminal tabs multiply
* Finished worktrees linger after merge

There’s no single place to see:

> What’s in flight?
> What’s blocked?
> What needs me right now?

---

## The Solution

Deckard acts as a command center for your worktree fleet.

It:

* Discovers active Git worktrees
* Maps them to Claude sessions (1:1 convention)
* Links branches → MRs → pipelines
* Flags work needing attention
* Lets you jump directly into the right session

Think **lazygit**, but purpose-built for AI coding sessions.

---

## MVP Scope

The goal of the MVP is simple:

> Replace “a million terminal tabs” with one operational dashboard.

### 1) Worktree Discovery

Deckard will:

* Parse `git worktree list --porcelain`
* Treat each worktree as an active “unit”
* Infer task names from branch/worktree names

Output example:

```
JIRA-182-payment-retries
JIRA-201-klarna-webhooks
chore-jest-upgrade
perf-plp-hydration
```

---

### 2) Session Mapping (Convention-based)

MVP assumes:

* One Claude session per worktree
* Session name matches worktree/branch slug

Future versions may read Claude’s session store directly.

---

### 3) GitLab MR Linking

Using `glab` CLI, Deckard will:

* Find open MRs for each branch
* Display MR ID + title
* Provide quick-open in browser

---

### 4) CI Status Monitoring

Deckard will pull pipeline status:

* ✅ Passed
* ❌ Failed
* ⏳ Running

Failures will flag the session as needing attention.

---

### 5) “Needs Input” Detection

A session gets an `*` if:

* MR has unresolved threads
* Pipeline failed
* (Future) Claude requested input via notifications

Example list:

```
*JIRA-182-payment-retries
JIRA-201-klarna-webhooks
*chore-jest-upgrade
perf-plp-hydration
```

---

### 6) Split-Pane TUI

Layout:

```
┌─────────────────────────┬────────────────────────────┐
│ Worktrees               │ Deckard Scan Report        │
│                         │                            │
│ *JIRA-182 …             │ MR: !4821                  │
│ JIRA-201 …              │ Pipeline: Failed           │
│ *chore-jest …           │ Last activity: 2h          │
│ perf-plp …              │ Attention: Review comment  │
│                         │                            │
└─────────────────────────┴────────────────────────────┘
```

Navigation:

* ↑ ↓ → select session
* Enter/Space → resume session
* `o` → open MR
* `r` → refresh
* `?` → filter

---

### 7) Session Resume

Deckard will:

* Launch `claude --resume <session>`
* Attach to the same terminal
* Return to Deckard UI when Claude exits

No new tabs required.

---

### 8) Worktree Cleanup (Manual)

Deckard can:

* Detect merged MRs
* Offer to delete associated worktrees

Future: auto-retire on merge via GitLab events.

---

## Non-Goals (for MVP)

To avoid overbuilding, MVP will NOT include:

* Direct Claude session store parsing
* Background daemons
* Webhooks / event listeners
* Jira integration
* CI log parsing
* Multi-repo aggregation

Those can come later if useful.

---

## Tech Stack (Planned)

* **Go** — single static binary
* **Bubble Tea** — TUI framework
* **Bubbles** — list + viewport components
* **Lip Gloss** — styling/layout
* Shell integrations:

  * `git`
  * `glab`
  * `claude`

---

## Design Principles

* Worktree-first, not AI-first
* Local-first, no external services
* Convention over configuration
* Fast to open, fast to navigate
* Zero tab sprawl

---

## Future Ideas

* Auto-retire worktrees on MR merge
* Claude notification ingestion
* Jira ticket linking via MCP
* CI failure log summarisation
* Multi-repo “fleet view”
* Agent auto-retry on pipeline failure
