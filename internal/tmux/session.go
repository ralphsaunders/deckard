package tmux

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const socketName = "deckard"

// SessionExists reports whether a named session exists on the Deckard socket.
func SessionExists(slug string) bool {
	return exec.Command("tmux", "-L", socketName, "has-session", "-t", slug).Run() == nil
}

// NeedsInput reports whether the named session is idle and awaiting input.
// It takes two pane snapshots 300 ms apart: a static pane means Claude has
// finished and is waiting; a changing pane means Claude is still processing.
func NeedsInput(slug string) bool {
	snap := func() []byte {
		out, _ := exec.Command("tmux", "-L", socketName,
			"capture-pane", "-t", slug, "-p", "-J").Output()
		return out
	}
	a := snap()
	time.Sleep(300 * time.Millisecond)
	b := snap()
	return bytes.Equal(a, b)
}

// configPath returns the Deckard tmux config path, writing defaults if absent.
// The config adds F12 as a no-prefix detach key so users can return to Deckard
// without needing to know tmux shortcuts.
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config dir: %w", err)
	}
	p := filepath.Join(dir, "deckard", "tmux.conf")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	const conf = "# Deckard tmux config — do not edit manually\n" +
		"# Ctrl+] returns you to the Deckard dashboard without stopping Claude\n" +
		"bind-key -n C-] detach-client\n" +
		"# Mouse wheel / PageUp enters scroll mode so you can read long plans\n" +
		"set -g mouse on\n" +
		"bind-key -n PageUp copy-mode\n"
	if err := os.WriteFile(p, []byte(conf), 0644); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	return p, nil
}

// EnsureSession creates a detached session running claude in path if one does
// not already exist. Idempotent: safe to call before every attach.
func EnsureSession(slug, path string) error {
	if SessionExists(slug) {
		return nil
	}
	cfgPath, err := configPath()
	if err != nil {
		return err
	}
	cmd := exec.Command("tmux", "-L", socketName, "-f", cfgPath,
		"new-session", "-d", "-s", slug, "-c", path,
		"claude", "--dangerously-skip-permissions")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("new-session: %s", out)
	}
	return nil
}

// AttachCmd returns a command that attaches the terminal to a named session.
// Pass the result to tea.ExecProcess — Deckard resumes when the user detaches
// (F12) or when Claude exits naturally.
func AttachCmd(slug string) *exec.Cmd {
	return exec.Command("tmux", "-L", socketName, "attach-session", "-t", slug)
}
