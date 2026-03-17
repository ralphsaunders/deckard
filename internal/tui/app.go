package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"deckard/internal/git"
	"deckard/internal/gitcloud"
	"deckard/internal/model"
	"deckard/internal/status"
	"deckard/internal/tmux"
)

// — state ———————————————————————————————————————————————————————————————————

type appState int

const (
	stateNormal appState = iota
	stateNewSession
	stateDeleteConfirm
	stateReview
)

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
	case i.s.InputReason == model.InputReasonBlocked:
		indicator = "✕"
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
	provider gitcloud.Provider

	state        appState
	nameInput    textinput.Model
	inputErr     string
	spinnerFrame int
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
		provider:  gitcloud.Detect(),
	}
}

// — commands ————————————————————————————————————————————————————————————————

func fetchSessionsCmd(repoRoot string, provider gitcloud.Provider) tea.Cmd {
	return func() tea.Msg {
		sessions, err := git.ListWorktrees()
		if err != nil {
			return sessionsLoadedMsg{sessions: nil, err: err}
		}

		// Enrich sessions concurrently: tmux status, git cloud MR data, agent status.
		var wg sync.WaitGroup
		for i := range sessions {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				sessions[i].TmuxRunning = tmux.SessionExists(sessions[i].Slug)
				if sessions[i].TmuxRunning {
					sessions[i].NeedsInput = tmux.NeedsInput(sessions[i].Slug)
				}
				mr, _ := provider.FetchMR(sessions[i].Branch)
				sessions[i].MR = mr
				agentStatus, _ := status.Read(repoRoot, sessions[i].Slug)
				sessions[i].AgentStatus = agentStatus
				sessions[i].NeedsInput, sessions[i].InputReason = computeNeedsInput(sessions[i])
			}(i)
		}
		wg.Wait()

		return sessionsLoadedMsg{sessions: sessions, err: nil}
	}
}

// computeNeedsInput determines whether a session needs human input based
// exclusively on the explicit agent status file. MR signals (CI failures,
// unresolved threads) do not surface sessions — the agent is responsible
// for resolving those before writing needs_review.
//
// needs_review without an mr_url is treated as blocked: the agent surfaced
// prematurely without raising an MR first.
func computeNeedsInput(s model.Session) (bool, model.InputReason) {
	if s.AgentStatus == nil {
		return false, model.InputReasonNone
	}
	switch s.AgentStatus.Status {
	case "needs_review":
		if s.AgentStatus.MRURL == "" {
			// Agent wrote needs_review without an MR — treat as blocked.
			return true, model.InputReasonBlocked
		}
		return true, model.InputReasonReviewReady
	case "blocked":
		return true, model.InputReasonBlocked
	}
	return false, model.InputReasonNone
}

// sessionSortKey returns a sort priority: blocked first, then review-ready,
// then active, then idle.
func sessionSortKey(s model.Session) int {
	switch s.InputReason {
	case model.InputReasonBlocked:
		return 0
	case model.InputReasonReviewReady:
		return 1
	}
	if s.TmuxRunning {
		return 2
	}
	return 3
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

// encodeSkillCmd writes a skill-draft.md into the session's state dir,
// then opens the main worktree's Claude session for skill encoding.
func encodeSkillCmd(repoRoot string, s model.Session) tea.Cmd {
	return func() tea.Msg {
		dir := filepath.Join(repoRoot, ".claude", "sessions", s.Slug)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return sessionEnsuredMsg{err: err}
		}
		var draft strings.Builder
		draft.WriteString("# Skill Draft: " + s.Slug + "\n\n")
		if s.AgentStatus != nil && s.AgentStatus.Summary != "" {
			draft.WriteString("## Summary\n\n" + s.AgentStatus.Summary + "\n\n")
		}
		draft.WriteString("## Task\n\nPlease encode the above work into a reusable skill.\n")
		draftPath := filepath.Join(dir, "skill-draft.md")
		if err := os.WriteFile(draftPath, []byte(draft.String()), 0644); err != nil {
			return sessionEnsuredMsg{err: err}
		}
		// Ensure and attach to the main worktree session
		if err := tmux.EnsureSession("main", repoRoot); err != nil {
			return sessionEnsuredMsg{err: err}
		}
		return sessionEnsuredMsg{slug: "main"}
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
	return tea.Batch(fetchSessionsCmd(m.repoRoot, m.provider), tickCmd())
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
		sort.Slice(m.sessions, func(i, j int) bool {
			return sessionSortKey(m.sessions[i]) < sessionSortKey(m.sessions[j])
		})
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
		m.state = stateNormal
		m.loading = true
		return m, fetchSessionsCmd(m.repoRoot, m.provider)

	case worktreeRemovedMsg:
		if msg.err != nil {
			m.inputErr = msg.err.Error()
			m.state = stateNormal
			return m, nil
		}
		m.state = stateNormal
		m.inputErr = ""
		m.loading = true
		return m, fetchSessionsCmd(m.repoRoot, m.provider)
	}

	switch m.state {
	case stateNewSession:
		return m.updateNewSession(msg)
	case stateDeleteConfirm:
		return m.updateDeleteConfirm(msg)
	case stateReview:
		return m.updateReview(msg)
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
			return m, fetchSessionsCmd(m.repoRoot, m.provider)
		case "n":
			m.state = stateNewSession
			m.inputErr = ""
			m.nameInput.Placeholder = "e.g. phase-2-gitlab-mr-linking"
			m.nameInput.Reset()
			m.nameInput.Focus()
			return m, textinput.Blink
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
				if s.AgentStatus != nil {
					m.state = stateReview
					return m, nil
				}
				return m, ensureAndAttachCmd(*s)
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m Model) updateReview(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.state = stateNormal
			return m, nil
		case "enter":
			// Attach is only available for blocked sessions.
			s := m.selectedSession()
			if s != nil && s.InputReason == model.InputReasonBlocked {
				return m, ensureAndAttachCmd(*s)
			}
			return m, nil
		case "o":
			s := m.selectedSession()
			// Prefer agent-written MR URL; fall back to fetched MR.
			if s != nil {
				url := mrURL(s)
				if url != "" {
					return m, openURLCmd(url)
				}
			}
			return m, nil
		case "s":
			s := m.selectedSession()
			if s != nil {
				return m, encodeSkillCmd(m.repoRoot, *s)
			}
			return m, nil
		}
	}
	return m, nil
}

// mrURL returns the best MR/PR URL for the session: agent-written first, then fetched.
func mrURL(s *model.Session) string {
	if s.AgentStatus != nil && s.AgentStatus.MRURL != "" {
		return s.AgentStatus.MRURL
	}
	if s.MR != nil {
		return s.MR.WebURL
	}
	return ""
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

	// Full-screen states bypass the normal list/detail layout
	if m.state == stateReview {
		return m.renderReview()
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top, m.list.View(), m.renderDetail())
	base := lipgloss.JoinVertical(lipgloss.Left, body, m.renderHelp())

	switch m.state {
	case stateNewSession:
		return m.renderModalOver(base)
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
	switch s.InputReason {
	case model.InputReasonReviewReady:
		statusVal = okStyle.Render("▲ READY FOR REVIEW")
	case model.InputReasonBlocked:
		statusVal = errStyle.Render("✕ BLOCKED")
	default:
		if s.TmuxRunning {
			statusVal = okStyle.Render("◆ ACTIVE")
		} else {
			statusVal = dimStyle.Render("· IDLE")
		}
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

	if s.AgentStatus != nil {
		b.WriteString("\n")
		b.WriteString(sectionSep("AGENT", contentWidth) + "\n\n")
		b.WriteString(renderAgentStatus(s.AgentStatus, contentWidth))
	}

	b.WriteString("\n")
	if s.TmuxRunning {
		b.WriteString(dimStyle.Render("CTRL+]  DETACH WITHOUT STOPPING CLAUDE\n"))
	}

	return style.Render(b.String())
}

func (m Model) renderReview() string {
	s := m.selectedSession()
	if s == nil || s.AgentStatus == nil {
		// Fall back to normal layout
		body := lipgloss.JoinHorizontal(lipgloss.Top, m.list.View(), m.renderDetail())
		return lipgloss.JoinVertical(lipgloss.Left, body, m.renderHelp())
	}

	as := s.AgentStatus
	contentWidth := m.width - 8 // 4 padding each side

	var b strings.Builder
	b.WriteString(detailHeadStyle.Render("REVIEW: "+strings.ToUpper(s.Slug)) + "\n")
	b.WriteString(dimStyle.Render(strings.Repeat("─", contentWidth)) + "\n\n")

	// SUMMARY
	b.WriteString(detailHeadStyle.Render("SUMMARY") + "\n")
	if as.Summary != "" {
		for _, line := range strings.Split(wrapText(as.Summary, contentWidth-2), "\n") {
			b.WriteString("  " + line + "\n")
		}
	}
	b.WriteString("\n")

	// UNCERTAINTY (omit if empty)
	if len(as.Uncertainty) > 0 {
		b.WriteString(detailHeadStyle.Render("UNCERTAINTY") + "\n")
		for _, q := range as.Uncertainty {
			b.WriteString("  · " + q + "\n")
		}
		b.WriteString("\n")
	}

	// BLOCKERS (omit if empty)
	if len(as.Blockers) > 0 {
		b.WriteString(detailHeadStyle.Render("BLOCKERS") + "\n")
		for _, bl := range as.Blockers {
			b.WriteString("  · " + bl + "\n")
		}
		b.WriteString("\n")
	}

	// For needs_review: render MR description inline as primary content.
	// For blocked: the blockers list above is the primary content.
	if s.InputReason == model.InputReasonReviewReady {
		b.WriteString(sectionSep("MR DESCRIPTION", contentWidth) + "\n\n")
		if s.MR != nil && s.MR.Description != "" {
			for _, line := range strings.Split(wrapText(s.MR.Description, contentWidth-2), "\n") {
				b.WriteString("  " + line + "\n")
			}
		} else if as.MRURL != "" {
			b.WriteString(dimStyle.Render("  "+as.MRURL) + "\n")
		} else {
			b.WriteString(dimStyle.Render("  NO MR DESCRIPTION") + "\n")
		}
		b.WriteString("\n")
	}

	// MR metadata (always show if available)
	if s.MR != nil {
		b.WriteString(sectionSep("MR", contentWidth) + "\n\n")
		b.WriteString(renderMR(s.MR, contentWidth))
	}

	body := lipgloss.NewStyle().Padding(1, 4).Width(m.width).Render(b.String())
	return lipgloss.JoinVertical(lipgloss.Left, body, m.renderHelp())
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

func renderAgentStatus(as *model.AgentStatus, contentWidth int) string {
	var b strings.Builder

	if as.Summary != "" {
		b.WriteString(labelStyle.Render("SUMMARY  "))
		lines := strings.Split(wrapText(as.Summary, contentWidth-9), "\n")
		b.WriteString(lines[0] + "\n")
		for _, line := range lines[1:] {
			b.WriteString(strings.Repeat(" ", 9) + line + "\n")
		}
		b.WriteString("\n")
	}

	if len(as.Uncertainty) > 0 {
		b.WriteString(labelStyle.Render("UNCERTAIN") + "\n")
		for _, q := range as.Uncertainty {
			b.WriteString("           · " + q + "\n")
		}
		b.WriteString("\n")
	}

	if len(as.Blockers) > 0 {
		b.WriteString(labelStyle.Render("BLOCKED  ") + "\n")
		for _, bl := range as.Blockers {
			b.WriteString("           · " + bl + "\n")
		}
		b.WriteString("\n")
	}

	return b.String()
}

// wrapText wraps text to the given width, breaking at word boundaries.
func wrapText(text string, width int) string {
	if width <= 0 {
		return text
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return text
	}
	var lines []string
	var current strings.Builder
	currentLen := 0
	for _, word := range words {
		wLen := len([]rune(word))
		if currentLen == 0 {
			current.WriteString(word)
			currentLen = wLen
		} else if currentLen+1+wLen <= width {
			current.WriteString(" ")
			current.WriteString(word)
			currentLen += 1 + wLen
		} else {
			lines = append(lines, current.String())
			current.Reset()
			current.WriteString(word)
			currentLen = wLen
		}
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	return strings.Join(lines, "\n")
}

func pipelineLabel(ps string) string {
	switch ps {
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
		return dimStyle.Render(ps)
	}
}

func (m Model) renderHelp() string {
	var text string
	switch m.state {
	case stateNewSession:
		text = "Enter create   Esc cancel"
	case stateDeleteConfirm:
		text = "y/Enter confirm   n/Esc cancel"
	case stateReview:
		s := m.selectedSession()
		if s != nil && s.InputReason == model.InputReasonBlocked {
			text = "Enter attach · o open MR · s encode skill · Esc back"
		} else {
			text = "o open MR · s encode skill · Esc back"
		}
	default:
		text = "↑/↓ navigate   Enter attach   n new   o open MR   d delete   q quit"
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
