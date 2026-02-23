package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/anthropics/agent/agent"
	"github.com/anthropics/agent/api"
)

// ─── Lipgloss styles (TUI only) ─────────────────────────────────────────────

var (
	tuiDim = lipgloss.NewStyle().
		Foreground(lipgloss.Color("8"))

	tuiGreen = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2"))

	tuiRed = lipgloss.NewStyle().
		Foreground(lipgloss.Color("1"))

	tuiYellow = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("3"))

	tuiPrompt = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("2"))

	tuiStatus = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	tuiBorder = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	tuiStatusBar = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	tuiStatusKey = lipgloss.NewStyle().
			Foreground(lipgloss.Color("5")).
			Bold(true)

	tuiStatusValue = lipgloss.NewStyle().
				Foreground(lipgloss.Color("7"))

	tuiStatusGitClean = lipgloss.NewStyle().
				Foreground(lipgloss.Color("2"))

	tuiStatusGitDirty = lipgloss.NewStyle().
				Foreground(lipgloss.Color("3"))

	tuiModePlan = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("4")) // blue

	tuiModeConfirm = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("2")) // green

	tuiModeYOLO = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("1")) // red

	tuiModeTerminal = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("3")) // yellow

	// Tab bar styles
	tuiTabActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("8"))

	tuiTabActiveFocused = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("0")).
				Background(lipgloss.Color("15"))

	tuiTabInactive = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	tuiTabNew = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Italic(true)

	tuiTabRunning = lipgloss.NewStyle().
			Foreground(lipgloss.Color("3"))

	tuiTabNeedsAttention = lipgloss.NewStyle().
				Foreground(lipgloss.Color("1")).
				Bold(true)

	tuiTabSubAgent = lipgloss.NewStyle().
			Foreground(lipgloss.Color("4"))

	tuiTabDone = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2"))
)

// ─── TUI (public wrapper) ───────────────────────────────────────────────────

// TUI is the Bubble Tea full-screen interactive mode.
type TUI struct {
	client *api.Client
	cwd    string
	prompt string // optional initial prompt to send on startup
}

func NewTUI(client *api.Client, cwd string, prompt string) *TUI {
	return &TUI{client: client, cwd: cwd, prompt: prompt}
}

func (t *TUI) Run() error {
	m := newTUIModel(t.client, t.cwd, t.prompt)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// ─── Bubble Tea messages ─────────────────────────────────────────────────────

type tuiAppendMsg struct{ text string }
type tuiDoneMsg struct{ err error }
type tuiShellDoneMsg struct{ err error }
type tuiUsageMsg struct{ usage api.Usage }
type tuiInitialPromptMsg struct{}
type tuiCompactionMsg struct{}
type tuiConfirmMsg struct {
	name  string
	input map[string]any
	reply chan agent.ConfirmResult
}

// sessionMsg wraps a message with the session ID it belongs to.
type sessionMsg struct {
	sessionID int
	inner     tea.Msg
}

// tuiSpawnMsg is sent by the dispatch tool to request a new sub-agent session.
type tuiSpawnMsg struct {
	task     string
	parentID int
	resultCh chan string
	errCh    chan error
}

// tuiSubAgentDoneMsg is sent when a sub-agent session completes.
type tuiSubAgentDoneMsg struct {
	sessionID int
}

// ─── Bubble Tea model ────────────────────────────────────────────────────────

type tuiState int

const (
	tuiStateInput   tuiState = iota
	tuiStateRunning
	tuiStateConfirm
)

const maxInputHeight = 10 // max lines the input area can grow to

type tuiModel struct {
	client *api.Client
	cwd    string
	width  int
	height int

	sessions []*session // all open sessions
	active   int        // index of the currently visible session
	nextID   int        // monotonically increasing ID for new sessions

	quitting       bool
	tabFocused     bool // true when the tab bar has focus (arrows navigate tabs)
	newTabFocused  bool // true when the "+ New Session" button is focused in tab bar
	msgCh          chan tea.Msg
	initialPrompt  string // if set, sent automatically on Init
}

// cur returns the currently active session.
func (m *tuiModel) cur() *session {
	return m.sessions[m.active]
}

// sessionByID finds a session by its ID. Returns nil if not found.
func (m *tuiModel) sessionByID(id int) *session {
	for _, s := range m.sessions {
		if s.id == id {
			return s
		}
	}
	return nil
}

// sessionIndexByID returns the index of a session by its ID, or -1.
func (m *tuiModel) sessionIndexByID(id int) int {
	for i, s := range m.sessions {
		if s.id == id {
			return i
		}
	}
	return -1
}

func newTUIModel(client *api.Client, cwd string, prompt string) tuiModel {
	msgCh := make(chan tea.Msg, 64)

	m := tuiModel{
		client:        client,
		cwd:           cwd,
		msgCh:         msgCh,
		initialPrompt: prompt,
		nextID:        0,
	}

	// Create the initial default session
	s := newSession(m.nextID, client, cwd, msgCh)
	m.nextID++
	m.sessions = []*session{s}
	m.active = 0

	// Wire up dispatch spawn for the initial session
	m.wireDispatch(s)

	return m
}

// wireDispatch sets up the dispatch tool's spawn function for a session.
func (m *tuiModel) wireDispatch(s *session) {
	msgCh := m.msgCh
	parentID := s.id
	s.setDispatchSpawn(func(task string) (string, error) {
		resultCh := make(chan string, 1)
		errCh := make(chan error, 1)
		msgCh <- tuiSpawnMsg{
			task:     task,
			parentID: parentID,
			resultCh: resultCh,
			errCh:    errCh,
		}
		// Block until the sub-agent completes
		select {
		case result := <-resultCh:
			return result, nil
		case err := <-errCh:
			return "", err
		}
	})
}

// createSession creates a new session and adds it to the model.
func (m *tuiModel) createSession() *session {
	s := newSession(m.nextID, m.client, m.cwd, m.msgCh)
	m.nextID++
	m.wireDispatch(s)
	m.sessions = append(m.sessions, s)
	return s
}

func (m tuiModel) Init() tea.Cmd {
	cmds := []tea.Cmd{textarea.Blink, m.waitForMsg()}
	if m.initialPrompt != "" {
		cmds = append(cmds, func() tea.Msg { return tuiInitialPromptMsg{} })
	}
	return tea.Batch(cmds...)
}

func (m tuiModel) waitForMsg() tea.Cmd {
	return func() tea.Msg { return <-m.msgCh }
}

// ─── Update ──────────────────────────────────────────────────────────────────

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeAll()
		for _, s := range m.sessions {
			s.refreshContent()
		}

	case tuiInitialPromptMsg:
		prompt := m.initialPrompt
		m.initialPrompt = ""
		s := m.cur()
		s.appendLine(tuiPrompt.Render("❯ " + prompt))
		s.autoName(prompt)
		s.state = tuiStateRunning
		s.input.Blur()
		return m, tea.Batch(s.sendToAgent(prompt, m.msgCh), m.waitForMsg())

	case sessionMsg:
		return m.handleSessionMsg(msg)

	case tuiSpawnMsg:
		return m.handleSpawn(msg)

	case tuiSubAgentDoneMsg:
		s := m.sessionByID(msg.sessionID)
		if s != nil && s.isSubAgent {
			s.name = "✓ " + s.name
		}
		cmds = append(cmds, m.waitForMsg())
	}

	// Update the active session's input if it's in input state
	s := m.cur()
	if s.state == tuiStateInput {
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(msg)
		if s.recalcInputHeight() {
			m.resizeAll()
		}
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// handleKey processes all key events.
func (m *tuiModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.cur()

	// Tab management keys — work in any state
	switch msg.String() {
	case "ctrl+t":
		m.tabFocused = false
		m.newTabFocused = false
		ns := m.createSession()
		m.active = len(m.sessions) - 1
		m.resizeAll()
		ns.input.Focus()
		return m, textarea.Blink

	case "ctrl+w":
		m.tabFocused = false
		m.newTabFocused = false
		return m.closeCurrentSession()
	}

	// Alt+1..9 — jump to tab by number
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
		r := msg.Runes[0]
		if msg.Alt && r >= '1' && r <= '9' {
			idx := int(r - '1')
			if idx < len(m.sessions) {
				m.switchToSession(idx)
			}
			return m, nil
		}
	}

	// Tab bar focused — arrow keys navigate tabs
	if m.tabFocused {
		switch msg.Type {
		case tea.KeyRight:
			if m.newTabFocused {
				// Already at the rightmost element
				return m, nil
			}
			if m.active < len(m.sessions)-1 {
				m.switchToSession(m.active + 1)
			} else {
				// Move focus to the "+ New Session" button
				m.newTabFocused = true
			}
			return m, nil
		case tea.KeyLeft:
			if m.newTabFocused {
				m.newTabFocused = false
				return m, nil
			}
			if m.active > 0 {
				m.switchToSession(m.active - 1)
			}
			return m, nil
		case tea.KeyEnter:
			if m.newTabFocused {
				// Activate the "+ New Session" button
				m.newTabFocused = false
				m.tabFocused = false
				ns := m.createSession()
				m.active = len(m.sessions) - 1
				m.resizeAll()
				ns.input.Focus()
				return m, textarea.Blink
			}
			// Return focus to input for existing tab
			m.tabFocused = false
			m.newTabFocused = false
			if s.state == tuiStateInput {
				s.input.Focus()
			}
			return m, textarea.Blink
		case tea.KeyTab, tea.KeyEsc:
			// Return focus to input
			m.tabFocused = false
			m.newTabFocused = false
			if s.state == tuiStateInput {
				s.input.Focus()
			}
			return m, textarea.Blink
		case tea.KeyCtrlC:
			if s.state == tuiStateRunning {
				if s.cancelFn != nil {
					s.cancelFn()
					s.cancelFn = nil
				}
				s.appendLine(tuiDim.Render("  ⏎ interrupted"))
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit
		default:
			// Any other key — return focus to input and process the key
			m.tabFocused = false
			m.newTabFocused = false
			if s.state == tuiStateInput {
				s.input.Focus()
			}
			// Fall through to normal handling below
		}
	}

	// Tab key toggles focus to the tab bar
	if msg.Type == tea.KeyTab && !m.tabFocused {
		m.tabFocused = true
		s.input.Blur()
		return m, nil
	}

	// Scrollback keys: PgUp/PgDn/Up/Down work in any state.
	switch msg.Type {
	case tea.KeyPgUp:
		s.output.PageUp()
		s.scrollback = !s.output.AtBottom()
		return m, nil
	case tea.KeyPgDown:
		s.output.PageDown()
		s.scrollback = !s.output.AtBottom()
		return m, nil
	case tea.KeyUp:
		if s.state != tuiStateInput {
			s.output.ScrollUp(3)
			s.scrollback = !s.output.AtBottom()
			return m, nil
		}
	case tea.KeyDown:
		if s.state != tuiStateInput {
			s.output.ScrollDown(3)
			s.scrollback = !s.output.AtBottom()
			return m, nil
		}
	case tea.KeyShiftTab:
		if s.state == tuiStateInput || s.state == tuiStateRunning {
			newMode := agent.CyclePermissionMode(s.agent.GetPermissionMode())
			s.agent.SetPermissionMode(newMode)
			var style lipgloss.Style
			switch newMode {
			case agent.ModePlan:
				style = tuiModePlan
			case agent.ModeYOLO:
				style = tuiModeYOLO
			case agent.ModeTerminal:
				style = tuiModeTerminal
			default:
				style = tuiModeConfirm
			}
			if newMode == agent.ModeTerminal {
				s.input.Prompt = "$ "
				s.input.Placeholder = "Run a command..."
			} else {
				s.input.Prompt = "❯ "
				s.input.Placeholder = "Ask anything..."
			}
			s.appendLine(tuiDim.Render("  mode → ") + style.Render(newMode.String()))
			return m, nil
		}
	}

	switch s.state {
	case tuiStateConfirm:
		return m.handleConfirm(msg)
	default:
		return m.handleInput(msg)
	}
}

func (m *tuiModel) handleInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.cur()

	switch msg.Type {
	case tea.KeyCtrlC:
		if s.state == tuiStateRunning {
			if s.cancelFn != nil {
				s.cancelFn()
				s.cancelFn = nil
			}
			s.appendLine(tuiDim.Render("  ⏎ interrupted"))
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit

	case tea.KeyEnter:
		value := strings.TrimSpace(s.input.Value())
		if value == "" {
			return m, nil
		}
		s.input.Reset()
		s.inputHeight = 1
		s.input.SetHeight(1)
		m.resizeAll()

		// Check for commands
		switch {
		case value == "/quit" || value == "/exit" || value == "/q":
			m.quitting = true
			return m, tea.Quit

		case value == "/clear":
			s.agent.Reset()
			s.lines = nil
			s.output.SetContent("")
			s.appendLine(tuiDim.Render("Conversation cleared."))
			return m, nil

		case value == "/close":
			tm, cmd := m.closeCurrentSession()
			return tm, cmd

		case strings.HasPrefix(value, "/rename "):
			newName := strings.TrimSpace(strings.TrimPrefix(value, "/rename "))
			if newName != "" {
				s.name = newName
				s.nameSet = true
				s.appendLine(tuiDim.Render(fmt.Sprintf("Session renamed to %q.", newName)))
			}
			return m, nil

		case value == "/sessions":
			for i, sess := range m.sessions {
				marker := "  "
				if i == m.active {
					marker = "→ "
				}
				status := "idle"
				if sess.state == tuiStateRunning {
					status = "running"
				} else if sess.state == tuiStateConfirm {
					status = "needs confirmation"
				}
				prefix := ""
				if sess.isSubAgent {
					prefix = "⤵ "
				}
				s.appendLine(tuiDim.Render(fmt.Sprintf("%s%d: %s%s (%s)", marker, i+1, prefix, sess.name, status)))
			}
			return m, nil

		case value == "/help":
			s.appendLine(tuiDim.Render("Commands: /help /clear /quit /close /rename <name> /sessions"))
			s.appendLine(tuiDim.Render("Shift+Tab: cycle permission mode (Confirm → Plan → YOLO → Terminal)"))
			s.appendLine(tuiDim.Render("Ctrl+T: new session  Ctrl+W: close session"))
			s.appendLine(tuiDim.Render("Tab: focus tab bar (←/→ to switch, enter to select, esc to return)"))
			s.appendLine(tuiDim.Render("Alt+1..9: jump to session by number"))
			s.appendLine(tuiDim.Render("Scroll: PgUp/PgDn, ↑/↓ arrows (while agent is running)"))
			s.appendLine(tuiDim.Render("Confirmations: y=allow, n=deny, a=always allow this tool for session"))
			s.appendLine(tuiDim.Render("Terminal mode: input runs as shell commands"))
			s.appendLine(tuiDim.Render("Env: ANTHROPIC_API_KEY, ANTHROPIC_MODEL, ANTHROPIC_BASE_URL"))
			return m, nil
		}

		// Terminal mode: run as shell command
		if s.agent.GetPermissionMode() == agent.ModeTerminal {
			s.appendLine(tuiYellow.Render("$ " + value))
			s.state = tuiStateRunning
			s.input.Blur()
			return m, tea.Batch(s.runShellCommand(value, m.cwd, m.msgCh), m.waitForMsg())
		}

		s.appendLine(tuiPrompt.Render("❯ " + value))
		s.autoName(value)
		s.state = tuiStateRunning
		s.input.Blur()
		return m, tea.Batch(s.sendToAgent(value, m.msgCh), m.waitForMsg())

	default:
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(msg)
		if s.recalcInputHeight() {
			m.resizeAll()
		}
		return m, cmd
	}
}

func (m *tuiModel) handleConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.cur()
	if s.confirm == nil {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyCtrlC:
		s.appendLine(tuiRed.Render("  ✗ denied (interrupted)"))
		s.confirm.reply <- agent.ConfirmDeny
		s.confirm = nil
		if s.cancelFn != nil {
			s.cancelFn()
			s.cancelFn = nil
		}
		s.state = tuiStateRunning
		return m, nil

	case tea.KeyEnter:
		s.appendLine(tuiGreen.Render("  ✓ allowed"))
		s.confirm.reply <- agent.ConfirmAllow
		s.confirm = nil
		s.state = tuiStateRunning
		return m, nil

	case tea.KeyRunes:
		ch := strings.ToLower(string(msg.Runes))
		switch ch {
		case "y":
			s.appendLine(tuiGreen.Render("  ✓ allowed"))
			s.confirm.reply <- agent.ConfirmAllow
			s.confirm = nil
			s.state = tuiStateRunning
			return m, nil
		case "n":
			s.appendLine(tuiRed.Render("  ✗ denied"))
			s.confirm.reply <- agent.ConfirmDeny
			s.confirm = nil
			s.state = tuiStateRunning
			return m, nil
		case "a":
			toolName := s.confirm.name
			s.appendLine(tuiGreen.Render(fmt.Sprintf("  ✓ always allow %s", toolName)))
			s.confirm.reply <- agent.ConfirmAlways
			s.confirm = nil
			s.state = tuiStateRunning
			return m, nil
		}
	}
	return m, nil
}

// handleSessionMsg routes a session-tagged message to the correct session.
func (m *tuiModel) handleSessionMsg(msg sessionMsg) (tea.Model, tea.Cmd) {
	s := m.sessionByID(msg.sessionID)
	if s == nil {
		// Session was closed — drain the message
		return m, m.waitForMsg()
	}

	var cmds []tea.Cmd

	switch inner := msg.inner.(type) {
	case tuiAppendMsg:
		s.appendLine(inner.text)
		cmds = append(cmds, m.waitForMsg())

	case tuiUsageMsg:
		s.contextUsed = inner.usage.ContextUsed()
		s.cacheRead = inner.usage.CacheReadInputTokens
		s.cacheCreate = inner.usage.CacheCreationInputTokens
		cost := m.client.CostForUsage(inner.usage)
		s.lastCost = cost
		s.totalCost += cost
		cmds = append(cmds, m.waitForMsg())

	case tuiCompactionMsg:
		s.appendLine(tuiDim.Render("  ⚡ context compacted"))
		cmds = append(cmds, m.waitForMsg())

	case tuiConfirmMsg:
		s.confirm = &inner
		s.state = tuiStateConfirm
		s.appendLine(tuiRenderDiff(inner.name, inner.input))
		summary := SummarizeConfirm(inner.name, inner.input)
		s.appendLine(tuiYellow.Render(fmt.Sprintf("Allow %s %s? [Y/n/a] ", inner.name, summary)))
		cmds = append(cmds, m.waitForMsg())

	case tuiDoneMsg:
		s.state = tuiStateInput
		if inner.err != nil {
			s.appendLine(tuiYellow.Render("Error: " + inner.err.Error()))
		}
		s.appendLine("")
		// Only focus the input if this is the active session
		if s.id == m.cur().id {
			s.input.Focus()
			cmds = append(cmds, textarea.Blink)
		}
		// If this is a sub-agent, send result back to parent
		if s.isSubAgent && s.resultCh != nil {
			result := s.agent.LastResponse()
			s.resultCh <- result
			s.resultCh = nil
			m.msgCh <- tuiSubAgentDoneMsg{sessionID: s.id}
		}

	case tuiShellDoneMsg:
		s.state = tuiStateInput
		if inner.err != nil {
			s.appendLine(tuiRed.Render("Error: " + inner.err.Error()))
		}
		s.appendLine("")
		if s.id == m.cur().id {
			s.input.Focus()
			cmds = append(cmds, textarea.Blink)
		}
	}

	return m, tea.Batch(cmds...)
}

// handleSpawn creates a new sub-agent session from a dispatch tool call.
func (m *tuiModel) handleSpawn(msg tuiSpawnMsg) (tea.Model, tea.Cmd) {
	child := m.createSession()
	child.parentID = msg.parentID
	child.isSubAgent = true
	child.resultCh = msg.resultCh

	// Name from task
	name := strings.TrimSpace(msg.task)
	if idx := strings.IndexByte(name, '\n'); idx > 0 {
		name = name[:idx]
	}
	if len(name) > 24 {
		name = name[:24] + "…"
	}
	child.name = "⤵ " + name
	child.nameSet = true

	// Inherit permission mode from parent
	parent := m.sessionByID(msg.parentID)
	if parent != nil {
		child.agent.SetPermissionMode(parent.agent.GetPermissionMode())
	}

	m.resizeAll()

	// Start the sub-agent — runs in background
	child.appendLine(tuiPrompt.Render("❯ " + msg.task))
	child.state = tuiStateRunning
	child.input.Blur()

	return m, tea.Batch(child.sendToAgent(msg.task, m.msgCh), m.waitForMsg())
}

// switchToSession switches to the session at the given index.
func (m *tuiModel) switchToSession(idx int) {
	if idx < 0 || idx >= len(m.sessions) {
		return
	}
	// Blur the current session's input
	m.cur().input.Blur()
	m.active = idx
	s := m.cur()
	// Focus the new session's input if it's in input state and tab bar isn't focused
	if s.state == tuiStateInput && !m.tabFocused {
		s.input.Focus()
	}
	m.resizeAll()
}

// closeCurrentSession closes the active session tab.
func (m *tuiModel) closeCurrentSession() (tea.Model, tea.Cmd) {
	if len(m.sessions) == 1 {
		m.quitting = true
		return m, tea.Quit
	}
	s := m.cur()
	if s.cancelFn != nil {
		s.cancelFn()
	}
	// If it's a sub-agent that hasn't sent its result, send an error
	if s.isSubAgent && s.resultCh != nil {
		s.resultCh <- "Error: sub-agent session was closed by user"
		s.resultCh = nil
	}
	m.sessions = append(m.sessions[:m.active], m.sessions[m.active+1:]...)
	if m.active >= len(m.sessions) {
		m.active = len(m.sessions) - 1
	}
	m.resizeAll()
	ns := m.cur()
	if ns.state == tuiStateInput {
		ns.input.Focus()
	}
	return m, textarea.Blink
}

// ─── View ────────────────────────────────────────────────────────────────────

func (m tuiModel) View() string {
	if m.quitting {
		return tuiDim.Render("Goodbye!") + "\n"
	}

	s := m.sessions[m.active]

	innerWidth := m.width - 2
	if innerWidth < 0 {
		innerWidth = 0
	}

	var b strings.Builder
	b.WriteString(s.output.View())
	b.WriteString("\n")

	// Build the prompt content
	var promptContent string
	switch s.state {
	case tuiStateConfirm:
		promptContent = tuiStatus.Render(" y/n/a ")
	case tuiStateRunning:
		if s.agent.GetPermissionMode() == agent.ModeTerminal {
			promptContent = tuiStatus.Render(" running... ") + tuiDim.Render("(ctrl+c to interrupt)")
		} else {
			promptContent = tuiStatus.Render(" thinking... ") + tuiDim.Render("(ctrl+c to interrupt)")
		}
	default:
		promptContent = s.input.View()
	}

	// Build status bar content
	statusContent := s.renderStatusBar(m.client, m.cwd)

	// Build tab bar content (merged border + label/bottom rows)
	mergedBorder, tabRows := m.renderTabBar(innerWidth)

	// Draw box around prompt + status bar
	topBorder := tuiBorder.Render("╭" + strings.Repeat("─", innerWidth) + "╮")
	midBorder := tuiBorder.Render("├" + strings.Repeat("─", innerWidth) + "┤")
	leftEdge := tuiBorder.Render("│")
	rightEdge := tuiBorder.Render("│")

	b.WriteString(topBorder)
	b.WriteString("\n")

	// Render prompt lines
	promptLines := strings.Split(promptContent, "\n")
	for _, pl := range promptLines {
		plWidth := lipgloss.Width(pl)
		if plWidth < innerWidth {
			pl += strings.Repeat(" ", innerWidth-plWidth)
		}
		b.WriteString(leftEdge + pl + rightEdge)
		b.WriteString("\n")
	}
	b.WriteString(midBorder)
	b.WriteString("\n")

	// Pad status bar to fill the box
	statusWidth := lipgloss.Width(statusContent)
	if statusWidth < innerWidth {
		statusContent += tuiStatusBar.Render(strings.Repeat(" ", innerWidth-statusWidth))
	}

	b.WriteString(leftEdge + statusContent + rightEdge)
	b.WriteString("\n")

	// Merged border: box bottom + tab tops combined
	b.WriteString(mergedBorder)
	b.WriteString("\n")

	// Tab label and bottom rows
	b.WriteString(tabRows)

	return b.String()
}

// renderTabBar builds the tab bar that connects to the status box bottom border.
// Returns (mergedBorder, tabRows) where mergedBorder replaces the box's bottom
// border and incorporates the tab top edges, and tabRows has the label + bottom rows.
func (m tuiModel) renderTabBar(boxWidth int) (string, string) {
	type tabInfo struct {
		label string
		style lipgloss.Style
		width int // rendered cell width of " label "
	}

	var tabs []tabInfo

	for i, s := range m.sessions {
		label := s.name

		// Add status suffixes
		if s.state == tuiStateRunning {
			label += " ⟳"
		} else if s.state == tuiStateConfirm {
			label += " ⚠"
		}

		var style lipgloss.Style
		if i == m.active {
			if m.tabFocused && !m.newTabFocused {
				style = tuiTabActiveFocused
			} else {
				style = tuiTabActive
			}
		} else if s.isSubAgent && s.state == tuiStateInput && strings.HasPrefix(s.name, "✓") {
			style = tuiTabDone
		} else if s.state == tuiStateConfirm {
			style = tuiTabNeedsAttention
		} else if s.isSubAgent {
			style = tuiTabSubAgent
		} else if s.state == tuiStateRunning {
			style = tuiTabRunning
		} else {
			style = tuiTabInactive
		}

		w := lipgloss.Width(style.Render(" " + label + " "))
		tabs = append(tabs, tabInfo{label: label, style: style, width: w})
	}

	// New session button
	var newLabel string
	var newStyle lipgloss.Style
	if m.newTabFocused {
		if len(m.sessions) == 1 {
			newLabel = "+ New Session"
		} else {
			newLabel = "+"
		}
		newStyle = tuiTabActiveFocused
	} else if len(m.sessions) == 1 {
		newLabel = "+ New Session"
		newStyle = tuiTabNew
	} else {
		newLabel = "+"
		newStyle = tuiTabNew
	}
	nw := lipgloss.Width(newStyle.Render(" " + newLabel + " "))
	tabs = append(tabs, tabInfo{label: newLabel, style: newStyle, width: nw})

	// Build the merged border line: combines box bottom with tab tops.
	// Layout: ╰─┬───────┬───┬─...─╯
	// The tabs start at offset 1 (matching the " " prefix in tab rows).
	// Adjacent tabs share their border character (│ between tabs).
	// Each tab occupies width + 1 cells (content + shared right border),
	// except the first which also has its own left border (+1 more).

	// Position 0 = ╰ (left corner of box)
	// Then boxWidth chars of ─ (positions 1..boxWidth)
	// Position boxWidth+1 = ╯ (right corner of box)
	// Total merged line width = boxWidth + 2

	totalWidth := boxWidth + 2
	border := make([]rune, totalWidth)
	for i := range border {
		border[i] = '─'
	}
	border[0] = '╰'
	border[totalWidth-1] = '╯'

	// Place tab wall positions on the border using ┬.
	// Adjacent tabs share their border: right edge of tab N = left edge of tab N+1.
	pos := 1 // leading space offset
	for i, t := range tabs {
		leftEdge := pos
		rightEdge := pos + t.width + 1 // +1 for the right border char
		if leftEdge > 0 && leftEdge < totalWidth-1 {
			border[leftEdge] = '┬'
		}
		if rightEdge > 0 && rightEdge < totalWidth-1 {
			border[rightEdge] = '┬'
		}
		if i < len(tabs)-1 {
			pos = rightEdge // shared border: next tab's left edge = this tab's right edge
		}
	}

	// Fix: if the last tab's right edge is beyond box, don't corrupt
	// Also handle the special corner cases:
	// - If a tab edge lands on position 0, use ╰ still (it won't, pos starts at 1)
	// - If a tab edge lands on the last position, use ╯ still
	border[0] = '╰'
	border[totalWidth-1] = '╯'

	mergedBorder := tuiBorder.Render(string(border))

	// Build label row and bottom row
	// Adjacent tabs share their border character (│ between, ╰╯ merged to ╨).
	var midBuf, botBuf strings.Builder
	midBuf.WriteString(" ")
	botBuf.WriteString(" ")
	for i, t := range tabs {
		if i == 0 {
			midBuf.WriteString(tuiBorder.Render("│"))
		} else {
			// shared border with previous tab
			midBuf.WriteString(tuiBorder.Render("│"))
		}
		midBuf.WriteString(t.style.Render(" " + t.label + " "))
		if i == len(tabs)-1 {
			midBuf.WriteString(tuiBorder.Render("│"))
		}

		if i == 0 {
			botBuf.WriteString(tuiBorder.Render("╰"))
		} else {
			botBuf.WriteString(tuiBorder.Render("╨"))
		}
		botBuf.WriteString(tuiBorder.Render(strings.Repeat("─", t.width)))
		if i == len(tabs)-1 {
			botBuf.WriteString(tuiBorder.Render("╯"))
		}
	}

	midRow := midBuf.String()
	botRow := botBuf.String()

	if m.tabFocused {
		hint := tuiDim.Render("  ←/→ navigate  enter select  esc return")
		midRow += hint
	}

	return mergedBorder, midRow + "\n" + botRow
}

// ─── TUI helpers ─────────────────────────────────────────────────────────────

// resizeAll updates the layout for the active session based on current terminal size.
func (m *tuiModel) resizeAll() {
	s := m.cur()
	// Layout (rows below the viewport):
	//   1  top border       ╭───╮
	//   N  input lines      │...│  (dynamic, 1..maxInputHeight)
	//   1  mid border       ├───┤
	//   1  status line      │...│
	//   1  merged border    ╰─┤..├─╯  (box bottom + tab tops)
	//   2  tab rows         │..│ │..│
	//                       ╰──╯ ╰──╯
	//   = 6 + N
	chrome := 6 + s.inputHeight
	s.resize(m.width, m.height, chrome)
}

// tuiRenderDiff renders a diff using lipgloss styles for the viewport.
func tuiRenderDiff(name string, input map[string]any) string {
	str := func(key string) string { s, _ := input[key].(string); return s }
	var lines []string

	switch name {
	case "edit":
		for _, line := range strings.Split(str("old_string"), "\n") {
			lines = append(lines, tuiRed.Render("  - "+line))
		}
		for _, line := range strings.Split(str("new_string"), "\n") {
			lines = append(lines, tuiGreen.Render("  + "+line))
		}
	case "write":
		raw := RenderDiff(name, input) // reuse shared helper (ANSI)
		lines = append(lines, raw)
	case "bash":
		lines = append(lines, tuiDim.Render("  $ "+str("command")))
	}

	return strings.Join(lines, "\n")
}

// ─── Git helpers ────────────────────────────────────────────────────────────

func renderGitStatus(cwd string) string {
	branch := gitBranch(cwd)
	if branch == "" {
		return ""
	}
	dirty := gitDirty(cwd)
	if dirty {
		return tuiStatusGitDirty.Render(" " + branch + "*")
	}
	return tuiStatusGitClean.Render(" " + branch)
}

func gitBranch(dir string) string {
	gitDir := findGitDir(dir)
	if gitDir == "" {
		return ""
	}
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(head))
	if strings.HasPrefix(s, "ref: refs/heads/") {
		return strings.TrimPrefix(s, "ref: refs/heads/")
	}
	if len(s) >= 8 {
		return s[:8]
	}
	return s
}

func gitDirty(dir string) bool {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

func findGitDir(dir string) string {
	for {
		candidate := filepath.Join(dir, ".git")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// ─── Formatting helpers ────────────────────────────────────────────────────

func formatTokens(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

func formatCost(c float64) string {
	if c < 0.01 {
		return fmt.Sprintf("$%.4f", c)
	}
	return fmt.Sprintf("$%.2f", c)
}

func shortenPath(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(p, home) {
		p = "~" + p[len(home):]
	}
	return p
}
