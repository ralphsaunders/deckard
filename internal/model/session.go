package model

// PR holds pull/merge request metadata fetched via gh or glab.
type PR struct {
	Number         int    // GitLab IID or GitHub PR number
	Title          string
	WebURL         string
	State          string // "open", "merged", "closed"
	PipelineStatus string // "success", "failed", "running", "pending", "canceled", etc.
	HasUnresolved  bool   // true if blocking discussions / review requests unresolved
	Draft          bool
	Forge          string // "gitlab" | "github"
}

// Session represents a git worktree and its associated work context.
type Session struct {
	Path        string
	Branch      string
	Slug        string // normalised task name, e.g. "JIRA-182-payment-retries"
	NeedsInput  bool
	TmuxRunning bool // whether a live tmux session exists for this worktree
	PR          *PR  // nil if no PR found or forge CLI unavailable
}
