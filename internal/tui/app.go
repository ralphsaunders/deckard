package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

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
	stateCommitType
	stateCommit
	stateDeleteConfirm
)

// — conventional commit types ————————————————————————————————————————————————

type ccType struct {
	key   string
	typ   string
	label string
}

var commitTypes = []ccType{
	{"f", "feat", "new feature"},
	{"x", "fix", "bug fix"},
	{"r", "refactor", "code restructure"},
	{"d", "docs", "documentation"},
	{"t", "test", "tests"},
	{"c", "chore", "maintenance"},
	{"i", "ci", "CI/CD"},
	{"p", "perf", "performance"},
}

// — styles ——————————————————————————————————————————————————————————————————

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("86")).
			MarginLeft(2)

	dimStyle  = lipgloss.NewStyle().Faint(true)
	boldStyle = lipgloss.NewStyle().Bold(true)
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))

	helpStyle = lipgloss.NewStyle().
			Faint(true).
			PaddingLeft(2)

	detailHeadStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("86"))

	// dim teal — for field labels
	labelStyle = lipgloss.NewStyle().
			Faint(true).
			Foreground(lipgloss.Color("86"))

	modalStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("86")).
			Padding(1, 3).
			Width(58)

	deleteModalStyle = lipgloss.NewStyle().
				Border(lipgloss.DoubleBorder()).
				BorderForeground(lipgloss.Color("196")).
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

type worktreeRemovedMsg struct {
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
		indicator = "▲"
	case i.s.TmuxRunning:
		indicator = i.spinnerChar
	default:
		indicator = "·"
	}
	return indicator + " " + i.s.Slug
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

	state        appState
	nameInput    textinput.Model
	inputErr     string
	spinnerFrame int
	commitType   string
}

func New() Model {
	root, _ := git.RepoRoot()

	delegate := list.NewDefaultDelegate()
	// BR-style selection: teal left-bar + teal text
	delegate.Styles.SelectedTitle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("86")).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(lipgloss.Color("86")).
		PaddingLeft(1)
	delegate.Styles.SelectedDesc = lipgloss.NewStyle().
		Faint(true).
		Foreground(lipgloss.Color("86")).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(lipgloss.Color("86")).
		PaddingLeft(1)

	l := list.New([]list.Item{}, delegate, 0, 0)
	l.Title = "WORKTREES"
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
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sessions[i].TmuxRunning = tmux.SessionExists(sessions[i].Slug)
			if sessions[i].TmuxRunning {
				sessions[i].NeedsInput = tmux.NeedsInput(sessions[i].Slug)
			}
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

// buildItems rebuilds the list items with the current spinner frame.
func (m *Model) buildItems() {
	char := spinnerFrames[m.spinnerFrame]
	items := make([]list.Item, len(m.sessions))
	for i, s := range m.sessions {
		items[i] = sessionItem{s: s, spinnerChar: char}
	}
	m.list.SetItems(items)
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
	case stateCommitType:
		return m.updateCommitType(msg)
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
				m.state = stateCommitType
				m.commitType = ""
				m.inputErr = ""
				return m, nil
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

func (m Model) updateCommitType(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.state = stateNormal
			m.inputErr = ""
			return m, nil
		}
		for _, t := range commitTypes {
			if msg.String() == t.key {
				m.commitType = t.typ
				m.state = stateCommit
				m.inputErr = ""
				m.nameInput.Placeholder = "short description"
				m.nameInput.Reset()
				m.nameInput.Focus()
				return m, textinput.Blink
			}
		}
	}
	return m, nil
}

func (m Model) updateCommit(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			// step back to type selection
			m.state = stateCommitType
			m.inputErr = ""
			m.nameInput.Blur()
			return m, nil
		case "enter":
			desc := strings.TrimSpace(m.nameInput.Value())
			if desc == "" {
				m.inputErr = "description cannot be empty"
				return m, nil
			}
			s := m.selectedSession()
			if s == nil {
				m.inputErr = "no session selected"
				return m, nil
			}
			m.inputErr = ""
			return m, commitCmd(s.Path, m.commitType+": "+desc)
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
		return lipgloss.NewStyle().Padding(1, 2).Render("LOADING WORKTREES…")
	}

	if m.err != nil {
		return lipgloss.NewStyle().Padding(1, 2).Render(
			fmt.Sprintf("ERR: %v\n\nPress r to retry, q to quit.", m.err),
		)
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top, m.list.View(), m.renderDetail())
	base := lipgloss.JoinVertical(lipgloss.Left, body, m.renderHelp())

	switch m.state {
	case stateNewSession:
		return m.renderModalOver(base)
	case stateCommitType:
		return m.renderCommitTypeModalOver(base)
	case stateCommit:
		return m.renderCommitModalOver(base)
	case stateDeleteConfirm:
		return m.renderDeleteConfirmOver(base)
	}
	return base
}

// — layout helpers ——————————————————————————————————————————————————————————

func (m Model) listDimensions() (width, height int) {
	return m.width / 3, m.height - 2
}

func (m Model) renderDetail() string {
	lw, _ := m.listDimensions()
	dw := m.width - lw
	dh := m.height - 2

	style := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(lipgloss.Color("86")).
		PaddingLeft(3).
		PaddingRight(2).
		Width(dw - 1).
		Height(dh)

	// Width of inner text area: box width minus padding
	contentWidth := (dw - 1) - 3 - 2

	s := m.selectedSession()
	if s == nil {
		return style.Render(dimStyle.Render("NO SESSIONS FOUND"))
	}

	row := func(lbl, val string) string {
		return labelStyle.Render(lbl) + val + "\n"
	}

	var statusVal string
	switch {
	case s.NeedsInput:
		statusVal = warnStyle.Render("▲ INPUT REQ")
	case s.TmuxRunning:
		statusVal = okStyle.Render("◆ ACTIVE")
	default:
		statusVal = dimStyle.Render("· IDLE")
	}

	var b strings.Builder
	b.WriteString(detailHeadStyle.Render(strings.ToUpper(s.Slug)) + "\n\n")
	b.WriteString(row("BRANCH   ", s.Branch))
	b.WriteString(row("PATH     ", s.Path))
	b.WriteString(row("STATUS   ", statusVal))
	b.WriteString("\n")
	b.WriteString(sectionSep("MR", contentWidth) + "\n\n")

	if s.MR != nil {
		b.WriteString(renderMR(s.MR, contentWidth))
	} else {
		b.WriteString(dimStyle.Render("NO MR FOUND") + "\n")
	}

	b.WriteString("\n")
	if s.TmuxRunning {
		b.WriteString(dimStyle.Render("CTRL+]  DETACH WITHOUT STOPPING CLAUDE\n"))
	}

	return style.Render(b.String())
}

// sectionSep renders a labeled divider: "─── LABEL ──────────────"
func sectionSep(label string, width int) string {
	const pre = "─── "
	content := pre + label + " "
	remaining := width - len([]rune(content))
	if remaining < 0 {
		remaining = 0
	}
	return dimStyle.Render(content + strings.Repeat("─", remaining))
}

func renderMR(mr *model.MR, contentWidth int) string {
	var b strings.Builder

	row := func(lbl, val string) string {
		return labelStyle.Render(lbl) + val + "\n"
	}

	// Truncate title if it would overflow the content area
	title := mr.Title
	maxTitleLen := contentWidth - 9 // 9 = label column width
	if maxTitleLen > 0 && len([]rune(title)) > maxTitleLen {
		title = string([]rune(title)[:maxTitleLen-1]) + "…"
	}

	inactive := mr.State == "merged" || mr.State == "closed"

	b.WriteString(row("MR       ", fmt.Sprintf("!%d", mr.IID)))
	if inactive {
		b.WriteString(dimStyle.Render("         "+title) + "\n")
	} else {
		b.WriteString("         " + title + "\n")
	}

	var stateStr string
	switch mr.State {
	case "merged":
		stateStr = dimStyle.Render("MERGED")
	case "closed":
		stateStr = dimStyle.Render("CLOSED")
	default:
		stateStr = okStyle.Render("OPEN")
	}
	b.WriteString(row("STATE    ", stateStr))

	b.WriteString(row("PIPELINE ", pipelineLabel(mr.PipelineStatus)))

	if mr.HasUnresolved {
		b.WriteString(row("THREADS  ", warnStyle.Render("▲ UNRESOLVED")))
	} else if mr.PipelineStatus != "" {
		b.WriteString(row("THREADS  ", okStyle.Render("◆ RESOLVED")))
	}

	return b.String()
}

func pipelineLabel(status string) string {
	switch status {
	case "success":
		return okStyle.Render("◆ PASSED")
	case "failed":
		return errStyle.Render("✕ FAILED")
	case "running":
		return warnStyle.Render("~ RUNNING")
	case "pending", "waiting_for_resource", "preparing", "scheduled":
		return warnStyle.Render("◇ PENDING")
	case "canceled":
		return dimStyle.Render("· CANCELED")
	case "skipped":
		return dimStyle.Render("· SKIPPED")
	case "":
		return dimStyle.Render("─")
	default:
		return dimStyle.Render(status)
	}
}

func (m Model) renderHelp() string {
	var text string
	switch m.state {
	case stateNewSession:
		text = "Enter create   Esc cancel"
	case stateCommitType:
		text = "key select type   Esc cancel"
	case stateCommit:
		text = "Enter commit   Esc ← type"
	case stateDeleteConfirm:
		text = "y/Enter confirm   n/Esc cancel"
	default:
		text = "↑/↓ navigate   Enter attach   n new   c commit   o open MR   d delete   r refresh   q quit"
	}
	sep := dimStyle.Render(strings.Repeat("─", m.width))
	return sep + "\n" + helpStyle.Render(text)
}

func (m Model) renderModalOver(base string) string {
	var b strings.Builder
	b.WriteString(detailHeadStyle.Render("NEW SESSION") + "\n\n")
	b.WriteString(labelStyle.Render("BRANCH NAME") + "\n")
	b.WriteString(m.nameInput.View() + "\n")
	if m.inputErr != "" {
		b.WriteString("\n" + errStyle.Render(m.inputErr) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("creates .claude/worktrees/<slug> · opens claude"))

	modal := modalStyle.Render(b.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal,
		lipgloss.WithWhitespaceBackground(lipgloss.Color("0")),
	)
}

func (m Model) renderCommitTypeModalOver(base string) string {
	s := m.selectedSession()
	var b strings.Builder
	b.WriteString(detailHeadStyle.Render("COMMIT TYPE") + "\n\n")
	if s != nil {
		b.WriteString(dimStyle.Render(strings.ToUpper(s.Slug)) + "\n\n")
	}
	for _, t := range commitTypes {
		b.WriteString(fmt.Sprintf("  %s  %-10s  %s\n",
			okStyle.Render(t.key),
			boldStyle.Render(t.typ),
			dimStyle.Render(t.label),
		))
	}

	modal := modalStyle.Render(b.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal,
		lipgloss.WithWhitespaceBackground(lipgloss.Color("0")),
	)
}

func (m Model) renderCommitModalOver(base string) string {
	s := m.selectedSession()
	var b strings.Builder
	b.WriteString(detailHeadStyle.Render("COMMIT CHANGES") + "\n\n")
	if s != nil {
		b.WriteString(dimStyle.Render(strings.ToUpper(s.Slug)) + "\n\n")
	}
	b.WriteString(labelStyle.Render("TYPE        ") + boldStyle.Render(m.commitType) + "\n\n")
	b.WriteString(labelStyle.Render("DESCRIPTION") + "\n")
	b.WriteString(m.nameInput.View() + "\n")
	if m.inputErr != "" {
		b.WriteString("\n" + errStyle.Render(m.inputErr) + "\n")
	}
	// live preview of the final commit message
	desc := m.nameInput.Value()
	if desc == "" {
		desc = dimStyle.Render("…")
	}
	b.WriteString("\n" + dimStyle.Render("→ "+m.commitType+": ") + desc)

	modal := modalStyle.Render(b.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal,
		lipgloss.WithWhitespaceBackground(lipgloss.Color("0")),
	)
}

func (m Model) renderDeleteConfirmOver(base string) string {
	s := m.selectedSession()
	var b strings.Builder
	b.WriteString(errStyle.Render("DELETE WORKTREE") + "\n\n")
	if s != nil {
		b.WriteString(labelStyle.Render("BRANCH   ") + s.Branch + "\n")
		b.WriteString(labelStyle.Render("PATH     ") + s.Path + "\n\n")
		if s.MR != nil && s.MR.State == "merged" {
			b.WriteString(okStyle.Render("◆ MR merged — safe to clean up") + "\n\n")
		} else if s.MR != nil && s.MR.State == "opened" {
			b.WriteString(warnStyle.Render("▲ MR is still open") + "\n\n")
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
