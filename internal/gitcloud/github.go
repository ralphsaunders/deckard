package gitcloud

import (
	"context"
	"encoding/json"
	"os/exec"
	"time"

	"deckard/internal/model"
)

// GitHubProvider fetches PR data via the gh CLI.
type GitHubProvider struct{}

type ghPR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	State  string `json:"state"`
	Body   string `json:"body"`
	// headRefName is the source branch
	HeadRefName string `json:"headRefName"`
	StatusCheckRollup []struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	} `json:"statusCheckRollup"`
	ReviewDecision string `json:"reviewDecision"`
}

// FetchMR returns the most relevant PR for the given branch using the gh CLI.
// Returns (nil, nil) if gh is unavailable, not a GitHub repo, or no PR exists.
func (p *GitHubProvider) FetchMR(branch string) (*model.MR, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx,
		"gh", "pr", "list",
		"--head", branch,
		"--json", "number,title,url,state,body,headRefName,statusCheckRollup,reviewDecision",
	).Output()
	if err != nil {
		return nil, nil
	}

	var prs []ghPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, nil
	}

	// prefer open PR; fall back to most recent
	var found *ghPR
	for i := range prs {
		if prs[i].State == "OPEN" {
			found = &prs[i]
			break
		}
	}
	if found == nil && len(prs) > 0 {
		found = &prs[0]
	}
	if found == nil {
		return nil, nil
	}

	mr := &model.MR{
		IID:         found.Number,
		Title:       found.Title,
		WebURL:      found.URL,
		Description: found.Body,
	}

	switch found.State {
	case "OPEN":
		mr.State = "opened"
	case "MERGED":
		mr.State = "merged"
	case "CLOSED":
		mr.State = "closed"
	default:
		mr.State = found.State
	}

	// Derive pipeline status from status check rollup.
	mr.PipelineStatus = derivePipelineStatus(found.StatusCheckRollup)

	return mr, nil
}

func derivePipelineStatus(checks []struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}) string {
	if len(checks) == 0 {
		return ""
	}
	hasFailure := false
	hasRunning := false
	allSuccess := true
	for _, c := range checks {
		switch c.Status {
		case "IN_PROGRESS", "QUEUED", "REQUESTED", "WAITING", "PENDING":
			hasRunning = true
			allSuccess = false
		}
		switch c.Conclusion {
		case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE":
			hasFailure = true
			allSuccess = false
		case "SUCCESS", "SKIPPED", "NEUTRAL":
			// ok
		default:
			if c.Status != "COMPLETED" {
				allSuccess = false
			}
		}
	}
	if hasFailure {
		return "failed"
	}
	if hasRunning {
		return "running"
	}
	if allSuccess {
		return "success"
	}
	return "pending"
}
