package gitcloud

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"deckard/internal/model"
)

// GitLabProvider fetches MR data via the glab CLI.
type GitLabProvider struct{}

// glabMR mirrors the fields we care about from glab's JSON output.
type glabMR struct {
	IID    int    `json:"iid"`
	Title  string `json:"title"`
	State  string `json:"state"`
	WebURL string `json:"web_url"`
	Pipeline *struct {
		Status string `json:"status"`
	} `json:"pipeline"`
	BlockingDiscussionsResolved *bool `json:"blocking_discussions_resolved"`
}

// FetchMR returns the most relevant MR for the given branch using the glab CLI.
// Returns (nil, nil) if glab is unavailable, not a GitLab repo, or no MR exists.
func (p *GitLabProvider) FetchMR(branch string) (*model.MR, error) {
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

	mr := &model.MR{
		IID:    found.IID,
		Title:  found.Title,
		WebURL: found.WebURL,
		State:  found.State,
	}
	if found.Pipeline != nil {
		mr.PipelineStatus = found.Pipeline.Status
	}
	if found.BlockingDiscussionsResolved != nil {
		mr.HasUnresolved = !*found.BlockingDiscussionsResolved
	}

	// Fetch full description via a second glab call.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	viewOut, viewErr := exec.CommandContext(ctx2,
		"glab", "mr", "view", fmt.Sprintf("%d", mr.IID), "--output", "json",
	).Output()
	if viewErr == nil {
		var detail struct {
			Description string `json:"description"`
		}
		if json.Unmarshal(viewOut, &detail) == nil {
			mr.Description = detail.Description
		}
	}

	return mr, nil
}
