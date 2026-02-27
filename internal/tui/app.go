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

	"deckard/internal/forge"
	"deckard/internal/git"
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
	stateCreatePR   // creating a new PR/MR
	stateManagePR   // submenu for existing PR/MR
)

// createPRField tracks which field in the create modal is focused.
type createPRField int

const (
	fieldTitle createPRField = iota
	fieldBase
	fieldDraft
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

	statusInfoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("86")).
			PaddingLeft(2)

	statusWarnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			PaddingLeft(2)

	statusErrStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			PaddingLeft(2)
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

type pushResultMsg struct {
	err error
}

type prCreatedMsg struct {
	err error
}

type prUpdatedMsg struct {
	err error
}

type clearStatusMsg struct{}

// — status notification ——————————————————————————————————————————————————————

type statusLevel int

const (
	statusInfo statusLevel = iota
	statusWarn
	statusError
)

func clearStatusCmd() tea.Cmd {
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg {
		return clearStatusMsg{}
	})
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
	forge    forge.Forge

	state        appState
	nameInput    textinput.Model
	inputErr     string
	spinnerFrame int
	commitType   string

	// create PR state
	createPRTitle    textinput.Model
	createPRBase     textinput.Model
	createPRDraft    bool
	createPRField    createPRField

	// status notification
	statusText  string
	statusLevel statusLevel
}

func New() Model {
	root, _ := git.RepoRoot()
	f := forge.Detect(root)

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

	prTitle := textinput.New()
	prTitle.Placeholder = "short description"
	prTitle.CharLimit = 120

	prBase := textinput.New()
	prBase.Placeholder = "main"
	prBase.CharLimit = 60
	if root != "" {
		prBase.SetValue(forge.DefaultBranch(root))
	} else {
		prBase.SetValue("main")
	}

	return Model{
		list:          l,
		repoRoot:      root,
		forge:         f,
		loading:       true,
		nameInput:     ti,
		createPRTitle: prTitle,
		createPRBase:  prBase,
	}
}

// — commands ————————————————————————————————————————————————————————————————

func (m *Model) fetchSessionsCmd() tea.Msg {
	sessions, err := git.ListWorktrees()
	if err != nil {
		return sessionsLoadedMsg{sessions: nil, err: err}
	}

	f := m.forge

	var wg sync.WaitGroup
	for i := range sessions {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sessions[i].TmuxRunning = tmux.SessionExists(sessions[i].Slug)
			if sessions[i].TmuxRunning {
				sessions[i].NeedsInput = tmux.NeedsInput(sessions[i].Slug)
			}
			if f != nil {
				pr, _ := f.FetchPR(sessions[i].Branch)
				sessions[i].PR = pr
				if pr != nil {
					sessions[i].NeedsInput = pr.PipelineStatus == "failed" || pr.HasUnresolved
				}
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

func pushCmd(repoPath, branch string) tea.Cmd {
	return func() tea.Msg {
		err := git.Push(repoPath, branch)
		return pushResultMsg{err: err}
	}
}

func createPRCmd(f forge.Forge, repoPath, branch string, opts forge.CreateOpts) tea.Cmd {
	return func() tea.Msg {
		// Ensure branch is pushed before creating PR.
		if !git.HasUpstream(repoPath, branch) {
			if err := git.Push(repoPath, branch); err != nil {
				return prCreatedMsg{err: fmt.Errorf("push failed: %w", err)}
			}
		}
		_, err := f.CreatePR(opts)
		return prCreatedMsg{err: err}
	}
}

func updatePRCmd(f forge.Forge, number int, opts forge.UpdateOpts) tea.Cmd {
	return func() tea.Msg {
		_, err := f.UpdatePR(number, opts)
		return prUpdatedMsg{err: err}
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
	return tea.Batch(m.fetchSessionsCmd, tickCmd())
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

	case clearStatusMsg:
		m.statusText = ""
		return m, nil

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
		m.loading = true
		return m, m.fetchSessionsCmd

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
		m.setStatus(statusInfo, "◆ committed")
		return m, tea.Batch(m.fetchSessionsCmd, clearStatusCmd())

	case worktreeRemovedMsg:
		if msg.err != nil {
			m.inputErr = msg.err.Error()
			m.state = stateNormal
			return m, nil
		}
		m.state = stateNormal
		m.inputErr = ""
		m.loading = true
		return m, m.fetchSessionsCmd

	case pushResultMsg:
		if msg.err != nil {
			m.setStatus(statusError, "✕ push failed: "+msg.err.Error())
		} else {
			m.setStatus(statusInfo, "◆ pushed")
		}
		m.loading = true
		return m, tea.Batch(m.fetchSessionsCmd, clearStatusCmd())

	case prCreatedMsg:
		if msg.err != nil {
			m.inputErr = msg.err.Error()
			return m, nil
		}
		m.state = stateNormal
		m.inputErr = ""
		m.setStatus(statusInfo, "◆ PR created")
		m.loading = true
		return m, tea.Batch(m.fetchSessionsCmd, clearStatusCmd())

	case prUpdatedMsg:
		if msg.err != nil {
			m.setStatus(statusError, "✕ "+msg.err.Error())
			m.state = stateNormal
			return m, tea.Batch(clearStatusCmd())
		}
		m.state = stateNormal
		m.setStatus(statusInfo, "◆ PR updated")
		m.loading = true
		return m, tea.Batch(m.fetchSessionsCmd, clearStatusCmd())
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
	case stateCreatePR:
		return m.updateCreatePR(msg)
	case stateManagePR:
		return m.updateManagePR(msg)
	default:
		return m.updateNormal(msg)
	}
}

func (m *Model) setStatus(level statusLevel, text string) {
	m.statusText = text
	m.statusLevel = level
}

func (m Model) updateNormal(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "r":
			m.loading = true
			return m, m.fetchSessionsCmd
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
		case "p":
			s := m.selectedSession()
			if s != nil {
				return m, pushCmd(s.Path, s.Branch)
			}
			return m, nil
		case "o":
			s := m.selectedSession()
			if s != nil && s.PR != nil && s.PR.WebURL != "" {
				return m, openURLCmd(s.PR.WebURL)
			}
			return m, nil
		case "m":
			s := m.selectedSession()
			if s == nil || m.forge == nil {
				return m, nil
			}
			if s.PR == nil {
				return m.enterCreatePR(s)
			}
			m.state = stateManagePR
			m.inputErr = ""
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

func (m Model) enterCreatePR(s *model.Session) (tea.Model, tea.Cmd) {
	m.state = stateCreatePR
	m.inputErr = ""
	m.createPRDraft = false
	m.createPRField = fieldTitle

	// Pre-fill title from branch slug: replace hyphens with spaces, title-case first word.
	title := slugToTitle(s.Branch)
	m.createPRTitle.Reset()
	m.createPRTitle.SetValue(title)
	m.createPRTitle.Focus()

	// Reset base to repo default.
	base := forge.DefaultBranch(m.repoRoot)
	m.createPRBase.Reset()
	m.createPRBase.SetValue(base)
	m.createPRBase.Blur()

	return m, textinput.Blink
}

func (m Model) updateCreatePR(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.state = stateNormal
			m.inputErr = ""
			m.createPRTitle.Blur()
			m.createPRBase.Blur()
			return m, nil

		case "tab":
			// Cycle: title → base → draft → title
			switch m.createPRField {
			case fieldTitle:
				m.createPRField = fieldBase
				m.createPRTitle.Blur()
				m.createPRBase.Focus()
			case fieldBase:
				m.createPRField = fieldDraft
				m.createPRBase.Blur()
			case fieldDraft:
				m.createPRField = fieldTitle
				m.createPRTitle.Focus()
			}
			return m, textinput.Blink

		case " ":
			// Space toggles draft when focused on draft field.
			if m.createPRField == fieldDraft {
				m.createPRDraft = !m.createPRDraft
				return m, nil
			}

		case "enter":
			title := strings.TrimSpace(m.createPRTitle.Value())
			base := strings.TrimSpace(m.createPRBase.Value())
			if title == "" {
				m.inputErr = "title cannot be empty"
				return m, nil
			}
			if base == "" {
				m.inputErr = "base branch cannot be empty"
				return m, nil
			}
			s := m.selectedSession()
			if s == nil {
				m.inputErr = "no session selected"
				return m, nil
			}
			m.inputErr = ""
			m.createPRTitle.Blur()
			m.createPRBase.Blur()
			return m, createPRCmd(m.forge, s.Path, s.Branch, forge.CreateOpts{
				Title:      title,
				BaseBranch: base,
				Draft:      m.createPRDraft,
			})
		}
	}

	var cmd tea.Cmd
	switch m.createPRField {
	case fieldTitle:
		m.createPRTitle, cmd = m.createPRTitle.Update(msg)
	case fieldBase:
		m.createPRBase, cmd = m.createPRBase.Update(msg)
	}
	return m, cmd
}

func (m Model) updateManagePR(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.state = stateNormal
			return m, nil
		case "p":
			// publish: mark ready for review
			s := m.selectedSession()
			if s == nil || s.PR == nil {
				m.state = stateNormal
				return m, nil
			}
			draft := false
			return m, updatePRCmd(m.forge, s.PR.Number, forge.UpdateOpts{Draft: &draft})
		case "d":
			// convert to draft
			s := m.selectedSession()
			if s == nil || s.PR == nil {
				m.state = stateNormal
				return m, nil
			}
			draft := true
			return m, updatePRCmd(m.forge, s.PR.Number, forge.UpdateOpts{Draft: &draft})
		case "o":
			s := m.selectedSession()
			if s != nil && s.PR != nil && s.PR.WebURL != "" {
				m.state = stateNormal
				return m, openURLCmd(s.PR.WebURL)
			}
			m.state = stateNormal
			return m, nil
		}
	}
	return m, nil
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
	base := lipgloss.JoinVertical(lipgloss.Left, body, m.renderStatus(), m.renderHelp())

	switch m.state {
	case stateNewSession:
		return m.renderModalOver(base)
	case stateCommitType:
		return m.renderCommitTypeModalOver(base)
	case stateCommit:
		return m.renderCommitModalOver(base)
	case stateDeleteConfirm:
		return m.renderDeleteConfirmOver(base)
	case stateCreatePR:
		return m.renderCreatePRModalOver(base)
	case stateManagePR:
		return m.renderManagePRModalOver(base)
	}
	return base
}

// — layout helpers ——————————————————————————————————————————————————————————

func (m Model) listDimensions() (width, height int) {
	return m.width / 3, m.height - 3 // -3: status + help lines
}

func (m Model) renderDetail() string {
	lw, _ := m.listDimensions()
	dw := m.width - lw
	dh := m.height - 3

	style := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(lipgloss.Color("86")).
		PaddingLeft(3).
		PaddingRight(2).
		Width(dw - 1).
		Height(dh)

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
	b.WriteString(sectionSep("PR / MR", contentWidth) + "\n\n")

	if s.PR != nil {
		b.WriteString(renderPR(s.PR, contentWidth))
	} else {
		b.WriteString(dimStyle.Render("NO PR FOUND") + "\n")
		if m.forge != nil {
			b.WriteString(dimStyle.Render("press m to create one") + "\n")
		}
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

func renderPR(pr *model.PR, contentWidth int) string {
	var b strings.Builder

	row := func(lbl, val string) string {
		return labelStyle.Render(lbl) + val + "\n"
	}

	title := pr.Title
	maxTitleLen := contentWidth - 9
	if maxTitleLen > 0 && len([]rune(title)) > maxTitleLen {
		title = string([]rune(title)[:maxTitleLen-1]) + "…"
	}

	inactive := pr.State == "merged" || pr.State == "closed"

	// Prefix: GitLab uses "!", GitHub uses "#"
	prefix := "!"
	if pr.Forge == "github" {
		prefix = "#"
	}

	b.WriteString(row("PR       ", fmt.Sprintf("%s%d", prefix, pr.Number)))
	if inactive {
		b.WriteString(dimStyle.Render("         "+title) + "\n")
	} else {
		b.WriteString("         " + title + "\n")
	}

	var stateStr string
	switch pr.State {
	case "merged":
		stateStr = dimStyle.Render("MERGED")
	case "closed":
		stateStr = dimStyle.Render("CLOSED")
	default:
		if pr.Draft {
			stateStr = dimStyle.Render("DRAFT")
		} else {
			stateStr = okStyle.Render("OPEN")
		}
	}
	b.WriteString(row("STATE    ", stateStr))
	b.WriteString(row("PIPELINE ", pipelineLabel(pr.PipelineStatus)))

	if pr.HasUnresolved {
		b.WriteString(row("THREADS  ", warnStyle.Render("▲ UNRESOLVED")))
	} else if pr.PipelineStatus != "" {
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

func (m Model) renderStatus() string {
	if m.statusText == "" {
		return ""
	}
	switch m.statusLevel {
	case statusWarn:
		return statusWarnStyle.Render(m.statusText)
	case statusError:
		return statusErrStyle.Render(m.statusText)
	default:
		return statusInfoStyle.Render(m.statusText)
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
	case stateCreatePR:
		text = "Tab next field   Space toggle draft   Enter create   Esc cancel"
	case stateManagePR:
		text = "p publish   d draft   o open   Esc cancel"
	default:
		text = "↑/↓ navigate   Enter attach   n new   c commit   p push   m MR/PR   o open   d delete   r refresh   q quit"
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
		if s.PR != nil && s.PR.State == "merged" {
			b.WriteString(okStyle.Render("◆ PR merged — safe to clean up") + "\n\n")
		} else if s.PR != nil && s.PR.State == "open" {
			b.WriteString(warnStyle.Render("▲ PR is still open") + "\n\n")
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

func (m Model) renderCreatePRModalOver(base string) string {
	s := m.selectedSession()
	f := m.forge

	var b strings.Builder
	b.WriteString(detailHeadStyle.Render("CREATE PR / MR") + "\n\n")

	if f != nil {
		forgeLabel := "GitLab"
		if f.Kind() == "github" {
			forgeLabel = "GitHub"
		}
		b.WriteString(row("FORGE  ", dimStyle.Render(forgeLabel)))
	}
	if s != nil {
		b.WriteString(row("BRANCH ", dimStyle.Render(s.Branch)))
	}
	b.WriteString("\n")

	// Title field
	titleLabel := labelStyle.Render("TITLE")
	if m.createPRField == fieldTitle {
		titleLabel = boldStyle.Foreground(lipgloss.Color("86")).Render("TITLE")
	}
	b.WriteString(titleLabel + "\n")
	b.WriteString(m.createPRTitle.View() + "\n\n")

	// Base field
	baseLabel := labelStyle.Render("BASE")
	if m.createPRField == fieldBase {
		baseLabel = boldStyle.Foreground(lipgloss.Color("86")).Render("BASE")
	}
	b.WriteString(baseLabel + "\n")
	b.WriteString(m.createPRBase.View() + "\n\n")

	// Draft toggle
	draftLabel := labelStyle.Render("DRAFT")
	if m.createPRField == fieldDraft {
		draftLabel = boldStyle.Foreground(lipgloss.Color("86")).Render("DRAFT")
	}
	draftVal := "[ ] No"
	if m.createPRDraft {
		draftVal = okStyle.Render("[x] Yes")
	}
	b.WriteString(draftLabel + "  " + draftVal + "\n")

	if m.inputErr != "" {
		b.WriteString("\n" + errStyle.Render(m.inputErr) + "\n")
	}

	modal := modalStyle.Render(b.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal,
		lipgloss.WithWhitespaceBackground(lipgloss.Color("0")),
	)
}

func (m Model) renderManagePRModalOver(base string) string {
	s := m.selectedSession()
	var b strings.Builder
	b.WriteString(detailHeadStyle.Render("MANAGE PR / MR") + "\n\n")

	if s != nil && s.PR != nil {
		prefix := "!"
		if s.PR.Forge == "github" {
			prefix = "#"
		}
		b.WriteString(dimStyle.Render(fmt.Sprintf("%s%d · %s", prefix, s.PR.Number, s.PR.Title)) + "\n\n")

		if s.PR.Draft {
			b.WriteString(fmt.Sprintf("  %s  %s\n",
				okStyle.Render("p"),
				"publish  "+dimStyle.Render("mark ready for review"),
			))
		} else {
			b.WriteString(fmt.Sprintf("  %s  %s\n",
				warnStyle.Render("d"),
				"draft    "+dimStyle.Render("convert to draft"),
			))
		}
	}

	b.WriteString(fmt.Sprintf("  %s  %s\n",
		okStyle.Render("o"),
		"open     "+dimStyle.Render("open in browser"),
	))
	b.WriteString("\n" + dimStyle.Render("Esc cancel"))

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

// slugToTitle converts a branch name to a human-readable title.
// e.g. "feat/user-auth-flow" → "feat: user auth flow"
func slugToTitle(branch string) string {
	// strip common prefixes like "feat/", "fix/", etc.
	parts := strings.SplitN(branch, "/", 2)
	var slug string
	if len(parts) == 2 {
		slug = parts[0] + ": " + parts[1]
	} else {
		slug = parts[0]
	}
	return strings.ReplaceAll(slug, "-", " ")
}

// row is a local helper for the create PR modal.
func row(lbl, val string) string {
	return labelStyle.Render(lbl) + val + "\n"
}
