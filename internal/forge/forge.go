package forge

import (
	"os/exec"
	"strings"

	"deckard/internal/model"
)

// Forge abstracts GitLab and GitHub operations.
type Forge interface {
	Kind() string // "gitlab" | "github"
	FetchPR(branch string) (*model.PR, error)
	CreatePR(opts CreateOpts) (*model.PR, error)
	UpdatePR(number int, opts UpdateOpts) (*model.PR, error)
}

// CreateOpts are the parameters for creating a PR/MR.
type CreateOpts struct {
	Title      string
	BaseBranch string
	Draft      bool
}

// UpdateOpts are the parameters for updating a PR/MR.
// Pointer fields: nil means "leave unchanged".
type UpdateOpts struct {
	Draft *bool
}

// Detect returns the appropriate Forge for the repo at repoRoot,
// or nil if the remote is unrecognised or no remote exists.
func Detect(repoRoot string) Forge {
	out, err := exec.Command("git", "-C", repoRoot, "remote", "get-url", "origin").Output()
	if err != nil {
		return nil
	}
	remote := strings.ToLower(strings.TrimSpace(string(out)))

	switch {
	case strings.Contains(remote, "github.com"):
		return &gitHub{}
	case strings.Contains(remote, "gitlab"):
		return &gitLab{}
	default:
		// Last-resort probe: if glab is configured for this repo, treat as GitLab.
		probe := exec.Command("glab", "repo", "view")
		probe.Dir = repoRoot
		if probe.Run() == nil {
			return &gitLab{}
		}
		return nil
	}
}

// DefaultBranch returns the repo's default remote branch (e.g. "main", "master").
// Falls back to "main" if it cannot be determined.
func DefaultBranch(repoRoot string) string {
	out, err := exec.Command(
		"git", "-C", repoRoot,
		"symbolic-ref", "--short", "refs/remotes/origin/HEAD",
	).Output()
	if err == nil {
		// output is "origin/main" — strip the remote prefix
		ref := strings.TrimSpace(string(out))
		if _, after, ok := strings.Cut(ref, "/"); ok {
			return after
		}
	}
	return "main"
}
