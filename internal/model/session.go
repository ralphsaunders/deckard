package model

// MR holds GitLab merge request metadata fetched via glab.
type MR struct {
	IID            int
	Title          string
	WebURL         string
	State          string // "opened", "merged", "closed"
	PipelineStatus string // "success", "failed", "running", "pending", "canceled", etc.
	HasUnresolved  bool   // true if blocking discussions are unresolved
}

// Session represents a git worktree and its associated work context.
type Session struct {
	Path        string
	Branch      string
	Slug        string // normalised task name, e.g. "JIRA-182-payment-retries"
	NeedsInput  bool
	TmuxRunning bool // whether a live tmux session exists for this worktree
	MR          *MR  // nil if no MR found or glab unavailable
}
