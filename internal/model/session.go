package model

// Session represents a git worktree and its associated work context.
// Phase 2+ will add MR and Pipeline fields.
type Session struct {
	Path       string
	Branch     string
	Slug       string // normalised task name, e.g. "JIRA-182-payment-retries"
	NeedsInput bool
}
