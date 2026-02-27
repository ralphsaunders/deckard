package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"deckard/internal/model"
)

type gitHub struct{}

func (g *gitHub) Kind() string { return "github" }

// ghPR mirrors the fields we care about from gh's JSON output.
type ghPR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"` // "OPEN", "MERGED", "CLOSED"
	URL    string `json:"url"`
	IsDraft bool  `json:"isDraft"`
	// StatusCheckRollup summarises all CI checks.
	StatusCheckRollup string `json:"statusCheckRollup"` // "SUCCESS", "FAILURE", "PENDING", ""
	// ReviewDecision is the overall review state.
	ReviewDecision string `json:"reviewDecision"` // "APPROVED", "CHANGES_REQUESTED", "REVIEW_REQUIRED", ""
}

func (g *gitHub) FetchPR(branch string) (*model.PR, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx,
		"gh", "pr", "list",
		"--head", branch,
		"--state", "all",
		"--json", "number,title,state,url,isDraft,statusCheckRollup,reviewDecision",
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

	pr := &model.PR{
		Number:         found.Number,
		Title:          found.Title,
		WebURL:         found.URL,
		State:          ghState(found.State),
		Draft:          found.IsDraft,
		PipelineStatus: ghCIStatus(found.StatusCheckRollup),
		HasUnresolved:  found.ReviewDecision == "CHANGES_REQUESTED" || found.ReviewDecision == "REVIEW_REQUIRED",
		Forge:          "github",
	}
	return pr, nil
}

func (g *gitHub) CreatePR(opts CreateOpts) (*model.PR, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	args := []string{
		"pr", "create",
		"--title", opts.Title,
		"--base", opts.BaseBranch,
		"--body", "",
	}
	if opts.Draft {
		args = append(args, "--draft")
	}

	out, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh pr create: %s", trimOutput(out))
	}
	return nil, nil // caller will trigger a refresh
}

func (g *gitHub) UpdatePR(number int, opts UpdateOpts) (*model.PR, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if opts.Draft == nil {
		return nil, nil
	}

	var args []string
	if *opts.Draft {
		args = []string{"pr", "ready", fmt.Sprintf("%d", number), "--undo"}
	} else {
		args = []string{"pr", "ready", fmt.Sprintf("%d", number)}
	}

	out, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh pr update: %s", trimOutput(out))
	}
	_ = out
	return nil, nil // caller will trigger a refresh
}

// ghState maps GitHub PR state strings to our unified model.
func ghState(s string) string {
	switch s {
	case "OPEN":
		return "open"
	case "MERGED":
		return "merged"
	case "CLOSED":
		return "closed"
	default:
		return s
	}
}

// ghCIStatus maps GitHub's statusCheckRollup to our pipeline status strings.
func ghCIStatus(s string) string {
	switch s {
	case "SUCCESS":
		return "success"
	case "FAILURE", "ERROR":
		return "failed"
	case "PENDING", "EXPECTED", "STALE":
		return "pending"
	case "":
		return ""
	default:
		return "pending"
	}
}
