package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"deckard/internal/model"
)

// RepoRoot returns the absolute path of the current git repository root.
func RepoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// CreateWorktree creates a new worktree at .claude/worktrees/<slug> on a new branch.
// Returns the path of the created worktree.
func CreateWorktree(repoRoot, branch string) (string, error) {
	slug := BranchToSlug(branch)
	path := filepath.Join(repoRoot, ".claude", "worktrees", slug)

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	cmd := exec.Command("git", "worktree", "add", path, "-b", branch)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}

	return path, nil
}

// ListWorktrees runs git worktree list --porcelain and returns parsed sessions.
func ListWorktrees() ([]model.Session, error) {
	out, err := exec.Command("git", "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	return parseWorktrees(string(out))
}

func parseWorktrees(raw string) ([]model.Session, error) {
	var sessions []model.Session
	for _, block := range strings.Split(strings.TrimSpace(raw), "\n\n") {
		s := parseBlock(strings.TrimSpace(block))
		if s != nil {
			sessions = append(sessions, *s)
		}
	}
	return sessions, nil
}

func parseBlock(block string) *model.Session {
	var path, branch string
	detached := false

	for _, line := range strings.Split(block, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "detached":
			detached = true
		}
	}

	if path == "" {
		return nil
	}
	if detached {
		branch = "detached"
	}

	return &model.Session{
		Path:   path,
		Branch: branch,
		Slug:   BranchToSlug(branch),
	}
}

// DeleteWorktree removes the worktree at path and attempts to delete the branch.
// The repoRoot is used as the working directory for git commands.
func DeleteWorktree(repoRoot, path, branch string) error {
	cmd := exec.Command("git", "worktree", "remove", path)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	// Best-effort branch deletion — ignore errors (e.g. branch not fully merged).
	exec.Command("git", "-C", repoRoot, "branch", "-d", branch).Run()
	return nil
}

// BranchToSlug normalises a branch name into a filesystem/tmux-safe slug.
func BranchToSlug(branch string) string {
	if branch == "" {
		return "unknown"
	}
	s := strings.ToLower(branch)
	s = strings.ReplaceAll(s, "/", "-")
	return s
}
