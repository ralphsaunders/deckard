package tui

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"deckard/internal/git"
	"deckard/internal/model"
	"deckard/internal/tmux"
)

// — state ———————————————————————————————————————————————————————————————————

type appState int

const (
	stateNormal appState = iota
	stateNewSession
	stateCommit
)

// — styles ——————————————————————————————————————————————————————————————————

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			MarginLeft(2)

	dimStyle  = lipgloss.NewStyle().Faint(true)
	boldStyle = lipgloss.NewStyle().Bold(true)
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	helpStyle = lipgloss.NewStyle().
			Faint(true).
			PaddingLeft(2)

	modalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("205")).
			Padding(1, 3).
			Width(58)
)

// — spinner —————————————————————————————————————————————————————————————————

var spinnerFrames = []string{"|", "/", "-", "\\"}

type tickMsg struct{}

func tickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

// — messages ————————————————————————————————————————————————————————————————

type sessionsLoadedMsg struct {
	sessions []model.Session
	err      error
}

type worktreeCreatedMsg struct {
	slug string
	path string
	err  error
}

type sessionEnsuredMsg struct {
	slug string
	err  error
}

type claudeExitedMsg struct {
	err error
}

type commitResultMsg struct {
	err error
}

// — list item ———————————————————————————————————————————————————————————————

type sessionItem struct {
	s           model.Session
	spinnerChar string
}

func (i sessionItem) Title() string {
	var indicator string
	switch {
	case i.s.NeedsInput:
		indicator = "*"
	case i.s.TmuxRunning:
		indicator = i.spinnerChar
	default:
		indicator = " "
	}
	return indicator + " " + i.s.Slug
}

func (i sessionItem) Description() string { return i.s.Branch }
func (i sessionItem) FilterValue() string { return i.s.Slug }

// — model ———————————————————————————————————————————————————————————————————

type Model struct {
	list     list.Model
	sessions []model.Session
	width    int
	height   int
	loading  bool
	err      error
	repoRoot string

	state        appState
	nameInput    textinput.Model
	inputErr     string
	spinnerFrame int
}

func New() Model {
	root, _ := git.RepoRoot()

	delegate := list.NewDefaultDelegate()

	l := list.New([]list.Item{}, delegate, 0, 0)
	l.Title = "Worktrees"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.SetShowHelp(false)
	l.Styles.Title = titleStyle

	ti := textinput.New()
	ti.Placeholder = "e.g. phase-2-gitlab-mr-linking"
	ti.CharLimit = 100

	return Model{
		list:      l,
		repoRoot:  root,
		loading:   true,
		nameInput: ti,
	}
}

// — commands ————————————————————————————————————————————————————————————————

func fetchSessions() tea.Msg {
	sessions, err := git.ListWorktrees()
	if err != nil {
		return sessionsLoadedMsg{sessions: nil, err: err}
	}

	type result struct {
		idx        int
		running    bool
		needsInput bool
	}
	ch := make(chan result, len(sessions))
	for i, s := range sessions {
		i, s := i, s
		go func() {
			exists := tmux.SessionExists(s.Slug)
			needs := false
			if exists {
				needs = tmux.NeedsInput(s.Slug)
			}
			ch <- result{i, exists, needs}
		}()
	}
	for range sessions {
		r := <-ch
		sessions[r.idx].TmuxRunning = r.running
		sessions[r.idx].NeedsInput = r.needsInput
	}

	return sessionsLoadedMsg{sessions: sessions, err: nil}
}

func createWorktreeCmd(repoRoot, branch string) tea.Cmd {
	return func() tea.Msg {
		path, err := git.CreateWorktree(repoRoot, branch)
		return worktreeCreatedMsg{
			slug: git.BranchToSlug(branch),
			path: path,
			err:  err,
		}
	}
}

func ensureAndAttachCmd(s model.Session) tea.Cmd {
	return func() tea.Msg {
		if err := tmux.EnsureSession(s.Slug, s.Path); err != nil {
			return sessionEnsuredMsg{err: err}
		}
		return sessionEnsuredMsg{slug: s.Slug}
	}
}

func commitCmd(path, message string) tea.Cmd {
	return func() tea.Msg {
		add := exec.Command("git", "-C", path, "add", "-A")
		if out, err := add.CombinedOutput(); err != nil {
			return commitResultMsg{err: fmt.Errorf("git add: %s", strings.TrimSpace(string(out)))}
		}
		commit := exec.Command("git", "-C", path, "commit", "-m", message)
		out, err := commit.CombinedOutput()
		if err != nil {
			return commitResultMsg{err: fmt.Errorf("%s", strings.TrimSpace(string(out)))}
		}
		_ = out
		return commitResultMsg{}
	}
}

// buildItems rebuilds the list items with the current spinner frame.
func (m *Model) buildItems() {
	char := spinnerFrames[m.spinnerFrame]
	items := make([]list.Item, len(m.sessions))
	for i, s := range m.sessions {
		items[i] = sessionItem{s: s, spinnerChar: char}
	}
	m.list.SetItems(items)
}

// — tea.Model ———————————————————————————————————————————————————————————————

func (m Model) Init() tea.Cmd {
	return tea.Batch(fetchSessions, tickCmd())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		lw, lh := m.listDimensions()
		m.list.SetSize(lw, lh)
		return m, nil

	case tickMsg:
		m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
		if !m.loading && m.err == nil {
			m.buildItems()
		}
		return m, tickCmd()

	case sessionsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.sessions = msg.sessions
		m.buildItems()
		return m, nil

	case worktreeCreatedMsg:
		if msg.err != nil {
			m.inputErr = msg.err.Error()
			return m, nil
		}
		m.state = stateNormal
		m.inputErr = ""
		m.nameInput.Reset()
		m.nameInput.Blur()
		return m, ensureAndAttachCmd(model.Session{Slug: msg.slug, Path: msg.path})

	case sessionEnsuredMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		return m, tea.ExecProcess(tmux.AttachCmd(msg.slug), func(err error) tea.Msg {
			return claudeExitedMsg{err: err}
		})

	case claudeExitedMsg:
		// Claude exited — refresh the session list and return to the overview.
		m.loading = true
		return m, fetchSessions

	case commitResultMsg:
		if msg.err != nil {
			m.inputErr = msg.err.Error()
			return m, nil
		}
		m.state = stateNormal
		m.inputErr = ""
		m.nameInput.Reset()
		m.nameInput.Blur()
		m.loading = true
		return m, fetchSessions
	}

	switch m.state {
	case stateNewSession:
		return m.updateNewSession(msg)
	case stateCommit:
		return m.updateCommit(msg)
	default:
		return m.updateNormal(msg)
	}
}

func (m Model) updateNormal(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "r":
			m.loading = true
			return m, fetchSessions
		case "n":
			m.state = stateNewSession
			m.inputErr = ""
			m.nameInput.Placeholder = "e.g. phase-2-gitlab-mr-linking"
			m.nameInput.Reset()
			m.nameInput.Focus()
			return m, textinput.Blink
		case "c":
			s := m.selectedSession()
			if s != nil {
				m.state = stateCommit
				m.inputErr = ""
				m.nameInput.Placeholder = "e.g. feat: add commit workflow"
				m.nameInput.Reset()
				m.nameInput.Focus()
				return m, textinput.Blink
			}
			return m, nil
		case "enter":
			s := m.selectedSession()
			if s != nil {
				return m, ensureAndAttachCmd(*s)
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m Model) updateNewSession(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.state = stateNormal
			m.inputErr = ""
			m.nameInput.Blur()
			return m, nil
		case "enter":
			branch := strings.TrimSpace(m.nameInput.Value())
			if branch == "" {
				m.inputErr = "branch name cannot be empty"
				return m, nil
			}
			if m.repoRoot == "" {
				m.inputErr = "could not determine git repo root"
				return m, nil
			}
			m.inputErr = ""
			return m, createWorktreeCmd(m.repoRoot, branch)
		}
	}
	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	return m, cmd
}

func (m Model) updateCommit(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.state = stateNormal
			m.inputErr = ""
			m.nameInput.Blur()
			return m, nil
		case "enter":
			message := strings.TrimSpace(m.nameInput.Value())
			if message == "" {
				m.inputErr = "commit message cannot be empty"
				return m, nil
			}
			s := m.selectedSession()
			if s == nil {
				m.inputErr = "no session selected"
				return m, nil
			}
			m.inputErr = ""
			return m, commitCmd(s.Path, message)
		}
	}
	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}

	if m.loading {
		return lipgloss.NewStyle().Padding(1, 2).Render("Loading worktrees…")
	}

	if m.err != nil {
		return lipgloss.NewStyle().Padding(1, 2).Render(
			fmt.Sprintf("Error: %v\n\nPress r to retry, q to quit.", m.err),
		)
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top, m.list.View(), m.renderDetail())
	base := lipgloss.JoinVertical(lipgloss.Left, body, m.renderHelp())

	if m.state == stateNewSession {
		return m.renderModalOver(base)
	}
	if m.state == stateCommit {
		return m.renderCommitModalOver(base)
	}
	return base
}

// — layout helpers ——————————————————————————————————————————————————————————

func (m Model) listDimensions() (width, height int) {
	return m.width / 3, m.height - 1
}

func (m Model) renderDetail() string {
	lw, _ := m.listDimensions()
	dw := m.width - lw
	dh := m.height - 1

	style := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		PaddingLeft(3).
		PaddingRight(2).
		Width(dw - 1).
		Height(dh)

	s := m.selectedSession()
	if s == nil {
		return style.Render(dimStyle.Render("No sessions found"))
	}

	var b strings.Builder
	b.WriteString(boldStyle.Render(s.Slug) + "\n\n")
	b.WriteString(fmt.Sprintf("Branch   %s\n", s.Branch))
	b.WriteString(fmt.Sprintf("Path     %s\n", s.Path))
	switch {
	case s.NeedsInput && s.TmuxRunning:
		b.WriteString(fmt.Sprintf("Status   %s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render("needs input")))
	case s.NeedsInput:
		b.WriteString(fmt.Sprintf("Status   %s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render("needs input")))
	case s.TmuxRunning:
		b.WriteString(fmt.Sprintf("Status   %s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("running")))
	default:
		b.WriteString(fmt.Sprintf("Status   %s\n", dimStyle.Render("idle")))
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("────────────────────────────────\n\n"))
	if s.TmuxRunning {
		b.WriteString(dimStyle.Render("Ctrl+] → back to Deckard without stopping Claude\n\n"))
	}
	b.WriteString(dimStyle.Render("MR · CI · pipeline status in Phase 2"))

	return style.Render(b.String())
}

func (m Model) renderHelp() string {
	switch m.state {
	case stateNewSession:
		return helpStyle.Render("Enter create   Esc cancel")
	case stateCommit:
		return helpStyle.Render("Enter commit   Esc cancel")
	default:
		return helpStyle.Render("↑/↓ navigate   Enter attach   n new   c commit   r refresh   q quit   Ctrl+] detach")
	}
}

func (m Model) renderModalOver(base string) string {
	var b strings.Builder
	b.WriteString(boldStyle.Render("New Session") + "\n\n")
	b.WriteString("Branch name\n")
	b.WriteString(m.nameInput.View() + "\n")
	if m.inputErr != "" {
		b.WriteString("\n" + errStyle.Render(m.inputErr) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("Creates .claude/worktrees/<slug> · opens claude"))

	modal := modalStyle.Render(b.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal,
		lipgloss.WithWhitespaceBackground(lipgloss.Color("0")),
	)
}

func (m Model) renderCommitModalOver(base string) string {
	s := m.selectedSession()
	var b strings.Builder
	b.WriteString(boldStyle.Render("Commit Changes") + "\n\n")
	if s != nil {
		b.WriteString(dimStyle.Render(s.Slug) + "\n\n")
	}
	b.WriteString("Commit message\n")
	b.WriteString(m.nameInput.View() + "\n")
	if m.inputErr != "" {
		b.WriteString("\n" + errStyle.Render(m.inputErr) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("Stages all changes · git add -A · git commit"))

	modal := modalStyle.Render(b.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal,
		lipgloss.WithWhitespaceBackground(lipgloss.Color("0")),
	)
}

func (m Model) selectedSession() *model.Session {
	if len(m.sessions) == 0 {
		return nil
	}
	idx := m.list.Index()
	if idx < 0 || idx >= len(m.sessions) {
		return nil
	}
	return &m.sessions[idx]
}
