package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"deckard/internal/model"
)

type gitLab struct{}

func (g *gitLab) Kind() string { return "gitlab" }

// glabMR mirrors the fields we care about from glab's JSON output.
type glabMR struct {
	IID    int    `json:"iid"`
	Title  string `json:"title"`
	State  string `json:"state"`
	WebURL string `json:"web_url"`
	Draft  bool   `json:"draft"`
	Pipeline *struct {
		Status string `json:"status"`
	} `json:"pipeline"`
	BlockingDiscussionsResolved *bool `json:"blocking_discussions_resolved"`
}

func (g *gitLab) FetchPR(branch string) (*model.PR, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx,
		"glab", "mr", "list",
		"--source-branch", branch,
		"-F", "json",
	).Output()
	if err != nil {
		return nil, nil
	}

	var mrs []glabMR
	if err := json.Unmarshal(out, &mrs); err != nil {
		return nil, nil
	}

	// prefer open MR; fall back to most recent (e.g. merged)
	var found *glabMR
	for i := range mrs {
		if mrs[i].State == "opened" {
			found = &mrs[i]
			break
		}
	}
	if found == nil && len(mrs) > 0 {
		found = &mrs[0]
	}
	if found == nil {
		return nil, nil
	}

	pr := &model.PR{
		Number: found.IID,
		Title:  found.Title,
		WebURL: found.WebURL,
		State:  normaliseState(found.State),
		Draft:  found.Draft,
		Forge:  "gitlab",
	}
	if found.Pipeline != nil {
		pr.PipelineStatus = found.Pipeline.Status
	}
	if found.BlockingDiscussionsResolved != nil {
		pr.HasUnresolved = !*found.BlockingDiscussionsResolved
	}
	return pr, nil
}

func (g *gitLab) CreatePR(opts CreateOpts) (*model.PR, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	args := []string{
		"mr", "create",
		"--title", opts.Title,
		"--target-branch", opts.BaseBranch,
		"--yes", // non-interactive
	}
	if opts.Draft {
		args = append(args, "--draft")
	}

	out, err := exec.CommandContext(ctx, "glab", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("glab mr create: %s", trimOutput(out))
	}

	// After creation, fetch the newly created MR by re-listing.
	// glab mr create outputs the MR URL but not structured JSON,
	// so we re-fetch by branch name.
	return nil, nil // caller will trigger a refresh
}

func (g *gitLab) UpdatePR(number int, opts UpdateOpts) (*model.PR, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if opts.Draft == nil {
		return nil, nil
	}

	var args []string
	if *opts.Draft {
		args = []string{"mr", "update", fmt.Sprintf("%d", number), "--draft"}
	} else {
		args = []string{"mr", "update", fmt.Sprintf("%d", number), "--ready"}
	}

	out, err := exec.CommandContext(ctx, "glab", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("glab mr update: %s", trimOutput(out))
	}
	_ = out
	return nil, nil // caller will trigger a refresh
}

// normaliseState maps GitLab state strings to our unified model.
func normaliseState(s string) string {
	switch s {
	case "opened":
		return "open"
	default:
		return s // "merged", "closed" are already canonical
	}
}

func trimOutput(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
