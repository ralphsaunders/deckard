package status

import (
	"os"
	"path/filepath"
	"strings"

	"deckard/internal/model"
)

// Read reads .claude/sessions/<slug>/status.md and parses YAML frontmatter.
// Returns nil, nil if the file does not exist.
func Read(repoRoot, slug string) (*model.AgentStatus, error) {
	p := filepath.Join(repoRoot, ".claude", "sessions", slug, "status.md")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return parseFrontmatter(string(data))
}

// parseFrontmatter extracts YAML frontmatter between --- delimiters.
// Uses a simple line-by-line parser — no external dependency.
func parseFrontmatter(content string) (*model.AgentStatus, error) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return nil, nil
	}
	rest := content[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, nil
	}
	fm := rest[:end]

	result := &model.AgentStatus{}
	var currentList *[]string

	for _, line := range strings.Split(fm, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// List item
		if strings.HasPrefix(trimmed, "- ") {
			if currentList != nil {
				item := strings.TrimPrefix(trimmed, "- ")
				item = strings.Trim(item, `"'`)
				*currentList = append(*currentList, item)
			}
			continue
		}

		// Key: value
		idx := strings.Index(trimmed, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:idx])
		val := strings.TrimSpace(trimmed[idx+1:])
		val = strings.Trim(val, `"'`)

		currentList = nil // reset list context on any new key

		switch key {
		case "status":
			result.Status = val
		case "summary":
			result.Summary = val
		case "mr_url":
			result.MRURL = val
		case "uncertainty":
			currentList = &result.Uncertainty
		case "blockers":
			currentList = &result.Blockers
		}
	}

	if result.Status == "" && result.Summary == "" {
		return nil, nil
	}
	return result, nil
}
