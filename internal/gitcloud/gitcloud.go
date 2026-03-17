// Package gitcloud provides a unified interface for fetching MR/PR data
// from git hosting providers (GitLab and GitHub).
package gitcloud

import (
	"os/exec"

	"deckard/internal/model"
)

// Provider can fetch MR/PR data for a given branch.
type Provider interface {
	FetchMR(branch string) (*model.MR, error)
}

// Detect returns the appropriate Provider by probing the environment.
// Prefers GitLab (glab) over GitHub (gh); returns a no-op provider if
// neither CLI is found.
func Detect() Provider {
	if isAvailable("glab") {
		return &GitLabProvider{}
	}
	if isAvailable("gh") {
		return &GitHubProvider{}
	}
	return &noopProvider{}
}

func isAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

type noopProvider struct{}

func (noopProvider) FetchMR(_ string) (*model.MR, error) { return nil, nil }
