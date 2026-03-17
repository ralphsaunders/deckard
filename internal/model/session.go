package model

// MR holds GitLab merge request metadata fetched via glab.
type MR struct {
	IID            int
	Title          string
	WebURL         string
	State          string // "opened", "merged", "closed"
	PipelineStatus string // "success", "failed", "running", "pending", "canceled", etc.
	HasUnresolved  bool   // true if blocking discussions are unresolved
	Description    string // full MR description (from glab mr view)
}

// AgentStatus holds the structured status written by Claude before resurfacing.
type AgentStatus struct {
	Status      string   // "working", "needs_review", "blocked"
	Summary     string
	MRURL       string   // MR URL written by agent; needs_review without this degrades to blocked
	Uncertainty []string
	Blockers    []string
}

// InputReason describes why a session needs input.
type InputReason int

const (
	InputReasonNone        InputReason = iota
	InputReasonReviewReady             // status: needs_review with mr_url — agent signalled ready for review
	InputReasonBlocked                 // status: blocked (or needs_review without mr_url)
)

// Session represents a git worktree and its associated work context.
type Session struct {
	Path        string
	Branch      string
	Slug        string       // normalised task name, e.g. "JIRA-182-payment-retries"
	NeedsInput  bool
	TmuxRunning bool         // whether a live tmux session exists for this worktree
	MR          *MR          // nil if no MR found or glab unavailable
	AgentStatus *AgentStatus // nil if no status file written
	InputReason InputReason
}
