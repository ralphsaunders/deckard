package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"deckard/internal/git"
	"deckard/internal/gitlab"
	"deckard/internal/model"
	"deckard/internal/tmux"
)

// — state ———————————————————————————————————————————————————————————————————

type appState int

const (
	stateNormal appState = iota
	stateNewSession
	stateCommit
	stateDeleteConfirm
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
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))

	helpStyle = lipgloss.NewStyle().
			Faint(true).
			PaddingLeft(2)

	modalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("205")).
			Padding(1, 3).
			Width(58)

	deleteModalStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("196")).
				Padding(1, 3).
				Width(58)
)

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

type worktreeRemovedMsg struct {
	err error
}

// — list item ———————————————————————————————————————————————————————————————

type sessionItem struct {
	s model.Session
}

func (i sessionItem) Title() string {
	prefix := ""
	if i.s.NeedsInput {
		prefix = "* "
	}
	if i.s.TmuxRunning {
		return prefix + i.s.Slug + " ●"
	}
	return prefix + i.s.Slug
}

func (i sessionItem) Description() string { return i.s.Branch }
func (i sessionItem) FilterValue() string  { return i.s.Slug }

// — model ———————————————————————————————————————————————————————————————————

type Model struct {
	list     list.Model
	sessions []model.Session
	width    int
	height   int
	loading  bool
	err      error
	repoRoot string

	state     appState
	nameInput textinput.Model
	inputErr  string
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

	// Enrich sessions concurrently: tmux status + GitLab MR data.
	var wg sync.WaitGroup
	for i := range sessions {
		sessions[i].TmuxRunning = tmux.SessionExists(sessions[i].Slug)

		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mr, _ := gitlab.FetchMR(sessions[i].Branch)
			sessions[i].MR = mr
			if mr != nil {
				sessions[i].NeedsInput = mr.PipelineStatus == "failed" || mr.HasUnresolved
			}
		}(i)
	}
	wg.Wait()

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

func openURLCmd(url string) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", url)
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		default:
			cmd = exec.Command("xdg-open", url)
		}
		cmd.Run()
		return nil
	}
}

func deleteWorktreeCmd(repoRoot, path, branch string) tea.Cmd {
	return func() tea.Msg {
		err := git.DeleteWorktree(repoRoot, path, branch)
		return worktreeRemovedMsg{err: err}
	}
}

// — tea.Model ———————————————————————————————————————————————————————————————

func (m Model) Init() tea.Cmd {
	return fetchSessions
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		lw, lh := m.listDimensions()
		m.list.SetSize(lw, lh)
		return m, nil

	case sessionsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.sessions = msg.sessions
		items := make([]list.Item, len(msg.sessions))
		for i, s := range msg.sessions {
			items[i] = sessionItem{s: s}
		}
		m.list.SetItems(items)
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

	case worktreeRemovedMsg:
		if msg.err != nil {
			m.inputErr = msg.err.Error()
			m.state = stateNormal
			return m, nil
		}
		m.state = stateNormal
		m.inputErr = ""
		m.loading = true
		return m, fetchSessions
	}

	switch m.state {
	case stateNewSession:
		return m.updateNewSession(msg)
	case stateCommit:
		return m.updateCommit(msg)
	case stateDeleteConfirm:
		return m.updateDeleteConfirm(msg)
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
		case "o":
			s := m.selectedSession()
			if s != nil && s.MR != nil && s.MR.WebURL != "" {
				return m, openURLCmd(s.MR.WebURL)
			}
			return m, nil
		case "d":
			s := m.selectedSession()
			if s != nil && s.Path != m.repoRoot {
				m.state = stateDeleteConfirm
				m.inputErr = ""
				return m, nil
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

func (m Model) updateDeleteConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "n", "N":
			m.state = stateNormal
			m.inputErr = ""
			return m, nil
		case "enter", "y", "Y":
			s := m.selectedSession()
			if s == nil {
				m.state = stateNormal
				return m, nil
			}
			return m, deleteWorktreeCmd(m.repoRoot, s.Path, s.Branch)
		}
	}
	return m, nil
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

	switch m.state {
	case stateNewSession:
		return m.renderModalOver(base)
	case stateCommit:
		return m.renderCommitModalOver(base)
	case stateDeleteConfirm:
		return m.renderDeleteConfirmOver(base)
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
	if s.TmuxRunning {
		b.WriteString(fmt.Sprintf("Status   %s\n", okStyle.Render("● running")))
	} else {
		b.WriteString(fmt.Sprintf("Status   %s\n", dimStyle.Render("idle")))
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("────────────────────────────────\n\n"))

	if s.MR != nil {
		b.WriteString(renderMR(s.MR))
	} else {
		b.WriteString(dimStyle.Render("No MR found") + "\n")
	}

	b.WriteString("\n")
	if s.TmuxRunning {
		b.WriteString(dimStyle.Render("Ctrl+] → back to Deckard without stopping Claude\n"))
	}

	return style.Render(b.String())
}

func renderMR(mr *model.MR) string {
	var b strings.Builder

	mrLine := fmt.Sprintf("MR       !%d  %s\n", mr.IID, mr.Title)
	switch mr.State {
	case "merged":
		b.WriteString(dimStyle.Render(mrLine))
		b.WriteString(dimStyle.Render("         merged\n"))
	case "closed":
		b.WriteString(dimStyle.Render(mrLine))
		b.WriteString(dimStyle.Render("         closed\n"))
	default:
		b.WriteString(mrLine)
		b.WriteString(fmt.Sprintf("         %s\n", mr.State))
	}

	b.WriteString(fmt.Sprintf("Pipeline %s\n", pipelineLabel(mr.PipelineStatus)))

	if mr.HasUnresolved {
		b.WriteString(warnStyle.Render("Threads  unresolved comments") + "\n")
	} else if mr.PipelineStatus != "" {
		b.WriteString(okStyle.Render("Threads  all resolved") + "\n")
	}

	return b.String()
}

func pipelineLabel(status string) string {
	switch status {
	case "success":
		return okStyle.Render("✅ passed")
	case "failed":
		return errStyle.Render("❌ failed")
	case "running":
		return warnStyle.Render("⏳ running")
	case "pending", "waiting_for_resource", "preparing", "scheduled":
		return warnStyle.Render("⏳ pending")
	case "canceled":
		return dimStyle.Render("⊘ canceled")
	case "skipped":
		return dimStyle.Render("— skipped")
	case "":
		return dimStyle.Render("—")
	default:
		return dimStyle.Render(status)
	}
}

func (m Model) renderHelp() string {
	switch m.state {
	case stateNewSession:
		return helpStyle.Render("Enter create   Esc cancel")
	case stateCommit:
		return helpStyle.Render("Enter commit   Esc cancel")
	case stateDeleteConfirm:
		return helpStyle.Render("y/Enter confirm   n/Esc cancel")
	default:
		return helpStyle.Render("↑/↓ navigate   Enter attach   n new   c commit   o open MR   d delete   r refresh   q quit")
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

func (m Model) renderDeleteConfirmOver(base string) string {
	s := m.selectedSession()
	var b strings.Builder
	b.WriteString(errStyle.Render("Delete Worktree") + "\n\n")
	if s != nil {
		b.WriteString(fmt.Sprintf("Branch   %s\n", s.Branch))
		b.WriteString(fmt.Sprintf("Path     %s\n\n", s.Path))
		if s.MR != nil && s.MR.State == "merged" {
			b.WriteString(okStyle.Render("MR is merged — safe to clean up") + "\n\n")
		} else if s.MR != nil && s.MR.State == "opened" {
			b.WriteString(warnStyle.Render("⚠  MR is still open") + "\n\n")
		}
	}
	b.WriteString("This will run git worktree remove and delete the branch.\n")
	if m.inputErr != "" {
		b.WriteString("\n" + errStyle.Render(m.inputErr) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("y/Enter to confirm · Esc/n to cancel"))

	modal := deleteModalStyle.Render(b.String())
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
