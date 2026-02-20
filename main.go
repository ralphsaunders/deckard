package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"deckard/internal/tui"
)

func main() {
	p := tea.NewProgram(tui.New(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
