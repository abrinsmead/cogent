package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/anthropics/agent/agent"
	"github.com/anthropics/agent/api"
	"github.com/anthropics/agent/tools"
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
			Padding(0, 1).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("4")) // white on blue

	tuiModeConfirm = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1).
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("2")) // black on green

	tuiModeYOLO = lipgloss.NewStyle().
			Bold(true).
			Padding(0, 1).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("1")) // white on red

	tuiModeTerminal = lipgloss.NewStyle().
				Bold(true).
				Padding(0, 1).
				Foreground(lipgloss.Color("0")).
				Background(lipgloss.Color("3")) // black on yellow

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

	tuiPaste = lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("15"))
)

// formatUserPrompt returns the display string for a user prompt.
// Long or multi-line input is collapsed into a short "[Paste: N lines]" label.
func formatUserPrompt(prefix, value string) string {
	lines := strings.Count(value, "\n")
	if lines >= 2 || len(value) > 500 {
		label := fmt.Sprintf("[Pasted %d lines]", lines+1)
		return tuiPrompt.Render(prefix) + tuiPaste.Render(label)
	}
	return tuiPrompt.Render(prefix + value)
}

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
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

// ─── Bubble Tea messages ─────────────────────────────────────────────────────

type tuiAppendMsg struct{ text string }
type tuiAppendLineMsg struct{ line line } // structured line for persistence
type tuiDoneMsg struct{ err error }
type tuiShellDoneMsg struct {
	err     error
	command string // the command that was run
	output  string // combined stdout+stderr
}
type tuiUsageMsg struct{ usage api.Usage }
type tuiInitialPromptMsg struct{}
type tuiCompactionMsg struct{}
type tuiConfirmMsg struct {
	name     string
	input    map[string]any
	reply    chan agent.ConfirmResult
	subAgent bool // true if from a sub-agent (routed to parent)
}

// sessionMsg wraps a message with the session ID it belongs to.
type sessionMsg struct {
	sessionID int
	inner     tea.Msg
}

// dotTickMsg drives the animated dots in the prompt bar while the agent is running.
type dotTickMsg struct{}

// ─── Bubble Tea model ────────────────────────────────────────────────────────

type tuiState int

const (
	tuiStateInput       tuiState = iota
	tuiStateRunning
	tuiStateConfirm
	tuiStateLinear
	tuiStatePlanConfirm // "Switch to Confirm mode and execute?" prompt after planning
)

type hudMode int

const (
	hudStatusBar hudMode = iota // bottom status bar (default)
	hudOverlay                  // floating top-right overlay
	hudOff                      // no HUD
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
	tabScroll      int  // index of the first visible tab (for horizontal scrolling)
	hudMode        hudMode // cycles: StatusBar → Overlay → Off
	msgCh          chan tea.Msg
	initialPrompt  string // if set, sent automatically on Init

	splash     *splashModel // non-nil while splash screen is active
	dotFrame   int          // animation frame for prompt dots
	dotTicking bool         // true while the dot tick is active
}

// cur returns the currently active session.
func (m *tuiModel) cur() *session {
	return m.sessions[m.active]
}

// setWindowTitle updates the terminal window title to the active session's name.
func (m *tuiModel) setWindowTitle() {
	os.Stderr.Write([]byte("\x1b]2;cogent — " + m.cur().name + "\x1b\\"))
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

	splash := newSplashModel()
	m := tuiModel{
		client:        client,
		cwd:           cwd,
		msgCh:         msgCh,
		initialPrompt: prompt,
		nextID:        0,
		splash:        &splash,
		hudMode:        loadHUDMode(cwd),
	}

	// Auto-restore saved sessions that had open tabs.
	saved := listSavedSessions(cwd)
	var tabSessions []sessionData
	for _, sd := range saved {
		if sd.TabOrder > 0 {
			tabSessions = append(tabSessions, sd)
		}
	}
	// Sort by TabOrder to restore original tab positions.
	sort.Slice(tabSessions, func(i, j int) bool {
		return tabSessions[i].TabOrder < tabSessions[j].TabOrder
	})
	if len(tabSessions) > 0 {
		m.sessions = nil
		for i := range tabSessions {
			sd := tabSessions[i]
			m.resumeSession(&sd, true)
		}
		m.active = 0
	} else {
		// No saved tab sessions — create a fresh default session
		s := newSession(m.nextID, client, cwd, msgCh)
		m.nextID++
		m.sessions = []*session{s}
		m.active = 0
		m.wireDispatch(s)
	}

	return m
}

// wireDispatch sets up the dispatch tool's spawn function for a session.
// Sub-agents run as tab-less goroutines with all tools except dispatch.
// Confirmations are routed to the parent session's tab.
func (m *tuiModel) wireDispatch(s *session) {
	client := m.client
	cwd := m.cwd
	msgCh := m.msgCh
	parentID := s.id

	dt := &tools.DispatchTool{}
	s.agent.Registry().RegisterTool(dt)
	dt.Spawn = func(task string) (string, error) {
		reg := tools.NewRegistry(cwd)
		ag := agent.New(client, cwd,
			agent.WithRegistry(reg),
			agent.WithPermissionMode(s.agent.GetPermissionMode()),
			agent.WithConfirmCallback(func(name string, input map[string]any) agent.ConfirmResult {
				reply := make(chan agent.ConfirmResult)
				msgCh <- sessionMsg{sessionID: parentID, inner: tuiConfirmMsg{
					name: name, input: input, reply: reply, subAgent: true,
				}}
				return <-reply
			}),
			agent.WithUsageCallback(func(usage api.Usage) {
				msgCh <- sessionMsg{sessionID: parentID, inner: tuiUsageMsg{usage: usage}}
			}),
		)
		if err := ag.Send(task); err != nil {
			return "", err
		}
		return ag.LastResponse(), nil
	}
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
	m.setWindowTitle()
	cmds := []tea.Cmd{m.waitForMsg()}
	if m.splash != nil {
		cmds = append(cmds, m.splash.Init())
	} else {
		cmds = append(cmds, textarea.Blink)
		if m.initialPrompt != "" {
			cmds = append(cmds, func() tea.Msg { return tuiInitialPromptMsg{} })
		}
	}
	return tea.Batch(cmds...)
}

func (m tuiModel) waitForMsg() tea.Cmd {
	return func() tea.Msg { return <-m.msgCh }
}

func dotTick() tea.Cmd {
	return tea.Tick(400*time.Millisecond, func(time.Time) tea.Msg {
		return dotTickMsg{}
	})
}

// anyRunning returns true if any session is in the running state.
func (m *tuiModel) anyRunning() bool {
	for _, s := range m.sessions {
		if s.state == tuiStateRunning {
			return true
		}
	}
	return false
}

// ensureDotTick starts the dot animation tick if not already running.
func (m *tuiModel) ensureDotTick() tea.Cmd {
	if !m.dotTicking {
		m.dotTicking = true
		return dotTick()
	}
	return nil
}

// ─── Update ──────────────────────────────────────────────────────────────────

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// ── Splash screen phase ─────────────────────────────────────────────
	if m.splash != nil {
		switch msg := msg.(type) {
		case tea.WindowSizeMsg:
			m.width = msg.Width
			m.height = msg.Height
			// Forward to splash
			updated, cmd := m.splash.Update(msg)
			m.splash = &updated
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)

		case splashDoneMsg:
			// Transition from splash to main UI
			m.splash = nil
			m.resizeAll()
			for _, s := range m.sessions {
				s.refreshContent()
			}
			initCmds := []tea.Cmd{textarea.Blink}
			if m.initialPrompt != "" {
				initCmds = append(initCmds, func() tea.Msg { return tuiInitialPromptMsg{} })
			}
			return m, tea.Batch(initCmds...)

		default:
			// All other messages go to splash (keys, ticks, etc.)
			updated, cmd := m.splash.Update(msg)
			m.splash = &updated
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
	}

	// ── Main UI phase ───────────────────────────────────────────────────
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			s := m.cur()
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				s.output.ScrollUp(3)
				s.scrollback = !s.output.AtBottom()
				return m, nil
			case tea.MouseButtonWheelDown:
				s.output.ScrollDown(3)
				s.scrollback = !s.output.AtBottom()
				return m, nil
			}
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeAll()
		m.scrollTabsToActive()
		for _, s := range m.sessions {
			s.refreshContent()
		}

	case tuiInitialPromptMsg:
		prompt := m.initialPrompt
		m.initialPrompt = ""
		s := m.cur()
		s.appendLine(line{Type: linePrompt, Data: prompt})
		s.autoName(prompt)
		m.setWindowTitle()
		s.state = tuiStateRunning
		s.input.Blur()
		return m, tea.Batch(s.sendToAgent(prompt, m.msgCh), m.waitForMsg(), m.ensureDotTick())

	case sessionMsg:
		return m.handleSessionMsg(msg)

	case dotTickMsg:
		m.dotFrame++
		if m.anyRunning() {
			cmds = append(cmds, dotTick())
		} else {
			m.dotTicking = false
		}
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

	// Ensure dot animation is running while any session is active
	if cmd := m.ensureDotTick(); cmd != nil {
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
		m.scrollTabsToActive()
		m.setWindowTitle()
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

	// Ctrl+H — cycle HUD display mode
	if msg.String() == "ctrl+h" {
		m.hudMode = (m.hudMode + 1) % 3
		labels := []string{"status bar", "overlay", "off"}
		s.appendLine(line{Type: lineInfo, Data: "  HUD → " + labels[m.hudMode]})
		m.resizeAll()
		saveHUDMode(m.cwd, m.hudMode)
		return m, nil
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
				m.scrollTabsToActive()
			}
			return m, nil
		case tea.KeyLeft:
			if m.newTabFocused {
				m.newTabFocused = false
				m.scrollTabsToActive()
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
				m.scrollTabsToActive()
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
				s.appendLine(line{Type: lineInfo, Data: "  ⏎ interrupted"})
				return m, nil
			}
			m.saveAllSessions()
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
	if msg.Type == tea.KeyTab && !m.tabFocused && s.state != tuiStateLinear {
		m.tabFocused = true
		s.input.Blur()
		return m, nil
	}

	// Shift+Arrow — switch tabs instantly
	switch msg.Type {
	case tea.KeyShiftRight:
		if m.newTabFocused {
			// Already at the rightmost element
			return m, nil
		}
		if m.active < len(m.sessions)-1 {
			m.switchToSession(m.active + 1)
		} else {
			// Move focus to the "+ New Session" button
			m.tabFocused = true
			m.newTabFocused = true
			s.input.Blur()
			m.scrollTabsToActive()
		}
		return m, nil
	case tea.KeyShiftLeft:
		if m.newTabFocused {
			m.newTabFocused = false
			m.tabFocused = false
			m.scrollTabsToActive()
			if s.state == tuiStateInput {
				s.input.Focus()
			}
			return m, textarea.Blink
		}
		if m.active > 0 {
			m.switchToSession(m.active - 1)
		}
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
		if s.state != tuiStateInput && s.state != tuiStateLinear {
			s.output.ScrollUp(3)
			s.scrollback = !s.output.AtBottom()
			return m, nil
		}
	case tea.KeyDown:
		if s.state != tuiStateInput && s.state != tuiStateLinear {
			s.output.ScrollDown(3)
			s.scrollback = !s.output.AtBottom()
			return m, nil
		}
	case tea.KeyShiftTab:
		if s.state == tuiStateInput || s.state == tuiStateRunning {
			newMode := agent.CyclePermissionMode(s.agent.GetPermissionMode())
			s.agent.SetPermissionMode(newMode)
			if newMode == agent.ModeTerminal {
				s.input.Prompt = "$ "
				s.input.Placeholder = "Run a command or press Shift+Tab to change modes"
			} else {
				s.input.Prompt = "❯ "
				s.input.Placeholder = "Ask a question or press Shift+Tab to change modes"
			}
			s.appendLine(line{Type: lineModeChange, Data: newMode.String()})
			return m, nil
		}
	}

	switch s.state {
	case tuiStateConfirm:
		return m.handleConfirm(msg)
	case tuiStatePlanConfirm:
		return m.handlePlanConfirm(msg)
	case tuiStateLinear:
		return m.handleLinear(msg)
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
			s.appendLine(line{Type: lineInfo, Data: "  ⏎ interrupted"})
			return m, nil
		}
		m.saveAllSessions()
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
			m.saveAllSessions()
			m.quitting = true
			return m, tea.Quit

		case value == "/clear":
			s.agent.Reset()
			s.slines = nil
			s.rlines = nil
			s.output.SetContent("")
			s.appendLine(line{Type: lineInfo, Data: "Conversation cleared."})
			deleteSessionFile(m.cwd, s.persistID)
			return m, nil

		case value == "/close":
			tm, cmd := m.closeCurrentSession()
			return tm, cmd

		case strings.HasPrefix(value, "/rename "):
			newName := strings.TrimSpace(strings.TrimPrefix(value, "/rename "))
			if newName != "" {
				s.name = newName
				s.nameSet = true
				m.setWindowTitle()
				s.appendLine(line{Type: lineInfo, Data: fmt.Sprintf("Session renamed to %q.", newName)})
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
				s.appendLine(line{Type: lineInfo, Data: fmt.Sprintf("%s%d: %s (%s)", marker, i+1, sess.name, status)})
			}
			return m, nil

		case value == "/help":
			s.appendLine(line{Type: lineInfo, Data: "Commands: /help /clear /quit /close /rename <name> /sessions /resume /linear (/lin)"})
			s.appendLine(line{Type: lineInfo, Data: "Shift+Tab: cycle permission mode (Plan → Confirm → YOLO → Terminal)"})
			s.appendLine(line{Type: lineInfo, Data: "Ctrl+T: new session  Ctrl+W: close session  Ctrl+H: cycle HUD"})
			s.appendLine(line{Type: lineInfo, Data: "Tab: focus tab bar (←/→ to switch, enter to select, esc to return)"})
			s.appendLine(line{Type: lineInfo, Data: "Shift+←/→: switch tabs  Alt+1..9: jump to tab by number"})
			s.appendLine(line{Type: lineInfo, Data: "Scroll: PgUp/PgDn, ↑/↓ arrows (while agent is running)"})
			s.appendLine(line{Type: lineInfo, Data: "Confirmations: y=allow, n=deny, a=always allow this tool for session"})
			s.appendLine(line{Type: lineInfo, Data: "Terminal mode: input runs as shell commands"})
			s.appendLine(line{Type: lineInfo, Data: "Env: ANTHROPIC_API_KEY, ANTHROPIC_MODEL, ANTHROPIC_BASE_URL"})
			return m, nil

		case value == "/linear" || value == "/lin":
			s.linear = newLinearModal(m.width, m.height)
			s.state = tuiStateLinear
			s.input.Blur()
			return m, nil

		case value == "/resume":
			return m.handleResume("")

		case strings.HasPrefix(value, "/resume "):
			arg := strings.TrimSpace(strings.TrimPrefix(value, "/resume "))
			return m.handleResume(arg)
		}

		// Terminal mode: run as shell command
		if s.agent.GetPermissionMode() == agent.ModeTerminal {
			s.appendLine(line{Type: lineShellPrompt, Data: value})
			s.state = tuiStateRunning
			s.input.Blur()
			return m, tea.Batch(s.runShellCommand(value, m.cwd, m.msgCh), m.waitForMsg(), m.ensureDotTick())
		}

		s.appendLine(line{Type: linePrompt, Data: value})
		s.autoName(value)
		m.setWindowTitle()
		s.state = tuiStateRunning
		s.input.Blur()
		return m, tea.Batch(s.sendToAgent(value, m.msgCh), m.waitForMsg(), m.ensureDotTick())

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
		s.appendLine(line{Type: lineConfirmDenyInt})
		s.confirm.reply <- agent.ConfirmDeny
		s.confirm = nil
		if s.cancelFn != nil {
			s.cancelFn()
			s.cancelFn = nil
		}
		s.state = tuiStateRunning
		return m, nil

	case tea.KeyEnter:
		s.appendLine(line{Type: lineConfirmAllow})
		s.confirm.reply <- agent.ConfirmAllow
		s.confirm = nil
		s.state = tuiStateRunning
		return m, nil

	case tea.KeyRunes:
		ch := strings.ToLower(string(msg.Runes))
		switch ch {
		case "y":
			s.appendLine(line{Type: lineConfirmAllow})
			s.confirm.reply <- agent.ConfirmAllow
			s.confirm = nil
			s.state = tuiStateRunning
			return m, nil
		case "n":
			s.appendLine(line{Type: lineConfirmDeny})
			s.confirm.reply <- agent.ConfirmDeny
			s.confirm = nil
			s.state = tuiStateRunning
			return m, nil
		case "a":
			toolName := s.confirm.name
			s.appendLine(line{Type: lineConfirmAlways, Data: toolName})
			s.confirm.reply <- agent.ConfirmAlways
			s.confirm = nil
			s.state = tuiStateRunning
			return m, nil
		}
	}
	return m, nil
}

// handlePlanConfirm processes the "Switch to Confirm mode and execute?" prompt
// shown after planning completes. y/Enter switches to Confirm mode and re-sends
// the agent's plan as a user instruction. n/Esc returns to normal input.
func (m *tuiModel) handlePlanConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.cur()

	accept := func() (tea.Model, tea.Cmd) {
		s.appendLine(line{Type: lineConfirmAllow})
		s.agent.SetPermissionMode(agent.ModeConfirm)
		s.input.Prompt = "❯ "
		s.input.Placeholder = "Ask a question or press Shift+Tab to change modes"
		s.appendLine(line{Type: lineModeChange, Data: agent.ModeConfirm.String()})
		s.state = tuiStateRunning
		s.input.Blur()
		return m, tea.Batch(
			s.sendToAgent("Execute the plan above.", m.msgCh),
			m.waitForMsg(),
			m.ensureDotTick(),
		)
	}

	decline := func() (tea.Model, tea.Cmd) {
		s.state = tuiStateInput
		if s.id == m.cur().id {
			s.input.Focus()
		}
		return m, textarea.Blink
	}

	switch msg.Type {
	case tea.KeyEnter:
		return accept()
	case tea.KeyEsc:
		return decline()
	case tea.KeyCtrlC:
		return decline()
	case tea.KeyRunes:
		ch := strings.ToLower(string(msg.Runes))
		switch ch {
		case "y":
			return accept()
		case "n":
			return decline()
		}
	}
	return m, nil
}

// handleLinear processes key events while the Linear ticket browser is open.
func (m *tuiModel) handleLinear(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.cur()
	if s.linear == nil {
		s.state = tuiStateInput
		s.input.Focus()
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEsc:
		// Check if we're already at the top level before calling back
		atTopLevel := !s.linear.showDetail && !(s.linear.view == linearViewProject && s.linear.inProject)
		if atTopLevel {
			// Close the modal
			s.linear = nil
			s.state = tuiStateInput
			s.input.Focus()
			return m, textarea.Blink
		}
		s.linear.back()
		return m, nil

	case tea.KeyUp:
		s.linear.up()
		return m, nil

	case tea.KeyDown:
		s.linear.down()
		return m, nil

	case tea.KeyTab:
		s.linear.switchView()
		return m, nil

	case tea.KeyBackspace:
		s.linear.back()
		return m, nil

	case tea.KeyEnter:
		// If viewing detail, insert into prompt
		if s.linear.showDetail {
			t := s.linear.selectedTicket()
			if t != nil {
				text := formatTicketForPrompt(t)
				s.linear = nil
				s.state = tuiStateInput
				s.input.Focus()
				s.input.SetValue(text)
				return m, textarea.Blink
			}
			return m, nil
		}
		s.linear.enter()
		return m, nil

	case tea.KeyCtrlC:
		s.linear = nil
		s.state = tuiStateInput
		s.input.Focus()
		return m, textarea.Blink
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
		s.appendLine(line{Type: lineInfo, Data: inner.text})
		cmds = append(cmds, m.waitForMsg())

	case tuiAppendLineMsg:
		s.appendLine(inner.line)
		cmds = append(cmds, m.waitForMsg())

	case tuiUsageMsg:
		s.contextUsed = inner.usage.ContextUsed()
		cost := m.client.CostForUsage(inner.usage)
		s.totalCost += cost
		cmds = append(cmds, m.waitForMsg())

	case tuiCompactionMsg:
		s.appendLine(line{Type: lineCompaction})
		cmds = append(cmds, m.waitForMsg())

	case tuiConfirmMsg:
		s.confirm = &inner
		s.state = tuiStateConfirm
		inputJSON, _ := json.Marshal(inner.input)
		s.appendLine(line{Type: lineDiff, Data: inner.name + "\x00" + string(inputJSON)})
		summary := SummarizeConfirm(inner.name, inner.input)
		prefix := ""
		if inner.subAgent {
			prefix = "(sub-agent) "
		}
		s.appendLine(line{Type: lineConfirmPrompt, Data: prefix + "\x00" + inner.name + "\x00" + summary})
		cmds = append(cmds, notifyCmd(s.name+" needs confirmation"), m.waitForMsg())

	case tuiDoneMsg:
		if inner.err != nil {
			s.state = tuiStateInput
			s.appendLine(line{Type: lineError, Data: inner.err.Error()})
		} else if s.agent.GetPermissionMode() == agent.ModePlan && s.agent.PlanReady() {
			// Planning finished with a ready signal — ask to switch to Confirm.
			s.state = tuiStatePlanConfirm
			s.appendLine(line{Type: linePlanConfirm})
		} else {
			s.state = tuiStateInput
		}
		// Only focus the input if this is the active session and we're in input state
		if s.id == m.cur().id && s.state == tuiStateInput {
			s.input.Focus()
			cmds = append(cmds, textarea.Blink)
		}
		cmds = append(cmds, notifyCmd(s.name+" is done"))
		saveSession(m.cwd, s, m.tabOrderOf(s))

	case tuiShellDoneMsg:
		s.state = tuiStateInput
		if inner.err != nil {
			s.appendLine(line{Type: lineShellError, Data: "Error: " + inner.err.Error()})
		}
		// Add terminal command and output to conversation history so the
		// agent can see what was run when the user switches back.
		if inner.command != "" {
			userText := fmt.Sprintf("[Terminal mode] $ %s", inner.command)
			assistantText := inner.output
			if assistantText == "" {
				assistantText = "(no output)"
			}
			if inner.err != nil {
				assistantText += "\nError: " + inner.err.Error()
			}
			s.agent.AppendHistory(userText, assistantText)
		}
		if s.id == m.cur().id {
			s.input.Focus()
			cmds = append(cmds, textarea.Blink)
		}
		saveSession(m.cwd, s, m.tabOrderOf(s))
	}

	return m, tea.Batch(cmds...)
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
	m.scrollTabsToActive()
	m.setWindowTitle()
}

// closeCurrentSession saves and closes the active session tab.
func (m *tuiModel) closeCurrentSession() (tea.Model, tea.Cmd) {
	if len(m.sessions) == 1 {
		saveSession(m.cwd, m.cur(), 0)
		m.quitting = true
		return m, tea.Quit
	}
	s := m.cur()
	if s.cancelFn != nil {
		s.cancelFn()
	}
	saveSession(m.cwd, s, 0)
	m.sessions = append(m.sessions[:m.active], m.sessions[m.active+1:]...)
	if m.active >= len(m.sessions) {
		m.active = len(m.sessions) - 1
	}
	m.resizeAll()
	m.scrollTabsToActive()
	m.setWindowTitle()
	ns := m.cur()
	if ns.state == tuiStateInput {
		ns.input.Focus()
	}
	return m, textarea.Blink
}

// handleResume lists saved sessions or resumes one by number/name.
func (m *tuiModel) handleResume(arg string) (tea.Model, tea.Cmd) {
	s := m.cur()
	saved := listSavedSessions(m.cwd)

	// Filter to sessions not in a tab (TabOrder == 0).
	var available []sessionData
	for _, sd := range saved {
		if sd.TabOrder == 0 {
			available = append(available, sd)
		}
	}

	if len(available) == 0 {
		s.appendLine(line{Type: lineInfo, Data: "No saved sessions to resume."})
		return m, nil
	}

	if arg == "" {
		// List available sessions
		s.appendLine(line{Type: lineInfo, Data: "Saved sessions:"})
		for i, sd := range available {
			age := formatAge(sd.UpdatedAt)
			preview := sessionPreview(sd)
			s.appendLine(line{Type: lineInfo, Data: fmt.Sprintf("  %d: %s (%s) %s", i+1, sd.Name, age, preview)})
		}
		s.appendLine(line{Type: lineInfo, Data: "Use /resume <number> or /resume <name> to restore."})
		return m, nil
	}

	// Try to match by number first
	var target *sessionData
	if n := parseResumeNumber(arg); n > 0 && n <= len(available) {
		target = &available[n-1]
	} else {
		// Match by name (case-insensitive prefix)
		argLower := strings.ToLower(arg)
		for i := range available {
			if strings.HasPrefix(strings.ToLower(available[i].Name), argLower) {
				target = &available[i]
				break
			}
		}
	}

	if target == nil {
		s.appendLine(line{Type: lineInfo, Data: fmt.Sprintf("No saved session matching %q.", arg)})
		return m, nil
	}

	// Resume the session
	resumed := m.resumeSession(target, false)
	m.active = len(m.sessions) - 1
	m.resizeAll()
	m.scrollTabsToActive()
	m.setWindowTitle()
	resumed.input.Focus()
	return m, textarea.Blink
}

// resumeSession creates a new tab from saved session data, restoring
// conversation history, display lines, and metadata.
// If quiet is true, the "↩ session resumed" info line is suppressed.
func (m *tuiModel) resumeSession(data *sessionData, quiet bool) *session {
	s := newSession(m.nextID, m.client, m.cwd, m.msgCh)
	m.nextID++
	m.wireDispatch(s)
	m.sessions = append(m.sessions, s)

	// Restore persistent identity and metadata
	s.persistID = data.ID
	s.name = data.Name
	s.nameSet = data.NameSet
	s.createdAt = data.CreatedAt
	s.totalCost = data.TotalCost
	s.contextUsed = data.ContextUsed

	// Restore agent state
	s.agent.SetMessages(data.Messages)
	s.agent.SetPermissionMode(parseModeString(data.PermissionMode))
	if len(data.AllowedTools) > 0 {
		allowed := make(map[string]bool)
		for _, name := range data.AllowedTools {
			allowed[name] = true
		}
		s.agent.SetAllowedTools(allowed)
	}

	// Restore input prompt style for terminal mode
	if s.agent.GetPermissionMode() == agent.ModeTerminal {
		s.input.Prompt = "$ "
		s.input.Placeholder = "Run a command or press Shift+Tab to change modes"
	}

	// Restore display lines
	s.slines = data.Lines
	s.rebuildRendered()
	if !quiet {
		s.appendLine(line{Type: lineInfo, Data: "  ↩ session resumed"})
	}

	// Save with updated tab order (now has a tab)
	saveSession(m.cwd, s, len(m.sessions))

	return s
}

// parseResumeNumber tries to parse a 1-based session number.
func parseResumeNumber(s string) int {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

// formatAge returns a human-readable relative time string.
func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%d mins ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// sessionPreview returns a short preview of the session's last user message.
func sessionPreview(sd sessionData) string {
	// Find the last user text
	for i := len(sd.Lines) - 1; i >= 0; i-- {
		if sd.Lines[i].Type == linePrompt {
			text := sd.Lines[i].Data
			if len(text) > 40 {
				text = text[:40] + "…"
			}
			return tuiDim.Render("— " + text)
		}
	}
	return ""
}

// ─── View ────────────────────────────────────────────────────────────────────

func (m tuiModel) View() string {
	if m.quitting {
		return tuiDim.Render("Goodbye!") + "\n"
	}

	// Splash screen
	if m.splash != nil {
		return m.splash.View()
	}

	s := m.sessions[m.active]

	innerWidth := m.width - 2
	if innerWidth < 0 {
		innerWidth = 0
	}

	var b strings.Builder

	// Overlay HUD on the viewport output (hidden while a modal is open)
	viewportContent := s.output.View()
	if m.hudMode == hudOverlay && !hasModal(s) {
		hudLines := s.renderHUD(m.client, m.cwd)
		if len(hudLines) > 0 {
			viewportContent = overlayHUD(viewportContent, hudLines, m.width)
		}
	}
	b.WriteString(viewportContent)
	b.WriteString("\n\n")

	// Build the prompt content
	var promptContent string
	switch s.state {
	case tuiStateConfirm:
		promptContent = tuiStatus.Render(" y/n/a ")
	case tuiStatePlanConfirm:
		promptContent = tuiStatus.Render(" y/n ")
	case tuiStateRunning:
		dots := strings.Repeat(".", m.dotFrame%4) + strings.Repeat(" ", 3-m.dotFrame%4)
		if s.agent.GetPermissionMode() == agent.ModeTerminal {
			promptContent = tuiStatus.Render(" running"+dots+" ") + tuiDim.Render("(ctrl+c to interrupt)")
		} else if s.agent.GetPermissionMode() == agent.ModePlan {
			promptContent = tuiStatus.Render(" planning"+dots+" ") + tuiDim.Render("(extended thinking · ctrl+c to interrupt)")
		} else {
			promptContent = tuiStatus.Render(" thinking"+dots+" ") + tuiDim.Render("(ctrl+c to interrupt)")
		}
	default:
		promptContent = s.input.View()
	}

	// Build status bar content
	statusContent := s.renderStatusBar(m.client, m.cwd)

	// Build tab bar content (merged border + label/bottom rows)
	mergedBorder, tabRows := m.renderTabBar(innerWidth)

	// Draw prompt box (full width) with mode prefix and right-justified hint
	topBorder := tuiBorder.Render("╭" + strings.Repeat("─", innerWidth) + "╮")
	midBorder := tuiBorder.Render("├" + strings.Repeat("─", innerWidth) + "┤")
	leftEdge := tuiBorder.Render("│")
	rightEdge := tuiBorder.Render("│")

	// Mode tag as prompt prefix, hint right-justified
	modeTag := " " + s.renderModeBar() + " "
	modeTagWidth := lipgloss.Width(modeTag)
	hint := tuiDim.Render("(shift + tab to change mode) ")
	hintWidth := lipgloss.Width(hint)

	b.WriteString(topBorder)
	b.WriteString("\n")

	// Render prompt lines with mode prefix on first line, hint right-justified
	promptLines := strings.Split(promptContent, "\n")
	for i, pl := range promptLines {
		plWidth := lipgloss.Width(pl)
		if i == 0 {
			// Prefix the mode tag, then prompt content, then right-justify hint
			prefixed := modeTag + pl
			prefixedWidth := modeTagWidth + plWidth
			availForHint := innerWidth - prefixedWidth
			if availForHint >= hintWidth {
				gap := availForHint - hintWidth
				pl = prefixed + strings.Repeat(" ", gap) + hint
			} else {
				// Not enough room for hint — just pad
				if prefixedWidth < innerWidth {
					pl = prefixed + strings.Repeat(" ", innerWidth-prefixedWidth)
				} else {
					pl = ansi.Truncate(prefixed, innerWidth, "")
				}
			}
		} else if plWidth < innerWidth {
			// Indent continuation lines to align with content after mode tag
			indent := strings.Repeat(" ", modeTagWidth)
			pl = indent + pl
			plWidth = lipgloss.Width(pl)
			if plWidth < innerWidth {
				pl += strings.Repeat(" ", innerWidth-plWidth)
			}
		}
		b.WriteString(leftEdge + pl + rightEdge)
		b.WriteString("\n")
	}

	// Status bar: mid border + status line (only in StatusBar mode)
	if m.hudMode == hudStatusBar {
		b.WriteString(midBorder)
		b.WriteString("\n")

		// Pad status bar to fill the box
		statusWidth := lipgloss.Width(statusContent)
		if statusWidth < innerWidth {
			statusContent += tuiStatusBar.Render(strings.Repeat(" ", innerWidth-statusWidth))
		}

		b.WriteString(leftEdge + statusContent + rightEdge)
		b.WriteString("\n")
	}

	// Merged border: box bottom + tab tops combined
	b.WriteString(mergedBorder)
	b.WriteString("\n")

	// Tab label and bottom rows
	b.WriteString(tabRows)

	view := b.String()

	// Modal overlay — centered in the viewport area
	if hasModal(s) {
		if s.linear != nil {
			mw := m.width * 80 / 100
			if mw > 100 {
				mw = 100
			}
			mh := m.height * 70 / 100
			if mh > 30 {
				mh = 30
			}
			s.linear.width = mw
			s.linear.height = mh
			modalStr := s.linear.render()
			view = overlayModal(dimView(view), modalStr, m.width, m.height)
		}
	}

	return view
}

// ensureActiveTabVisible adjusts tabScroll so the active tab (or the new-tab
// button when newTabFocused) is within the visible range for the given width.
func (m *tuiModel) ensureActiveTabVisible(boxWidth int) {
	allTabs := m.buildTabInfos()

	// Which index should be visible?
	target := m.active
	if m.newTabFocused {
		target = len(allTabs) - 1 // the "+ New" button is last
	}

	// Clamp tabScroll lower bound
	if m.tabScroll > target {
		m.tabScroll = target
	}
	if m.tabScroll < 0 {
		m.tabScroll = 0
	}

	// Available width for tab content (1 char leading space, and we reserve
	// 3 chars per arrow indicator: " ◀ " / " ▶ ").
	avail := boxWidth
	hasLeft := m.tabScroll > 0

	// Grow the visible window from tabScroll until we run out of space.
	// Each tab costs: width (content) + 1 (shared border), except the first
	// visible tab which also has its own left border (+1 more = width + 2).
	for {
		used := 0
		if hasLeft {
			used += 3 // " ◀ " left arrow
		}
		visEnd := m.tabScroll
		for i := m.tabScroll; i < len(allTabs); i++ {
			extra := allTabs[i].width + 1 // content + shared right border
			if i == m.tabScroll {
				extra++ // left border for first visible tab
			}
			if used+extra > avail {
				break
			}
			used += extra
			visEnd = i + 1
		}
		// If there are hidden tabs after the visible range, the right arrow
		// indicator (" ▶ ") needs space. Drop the last visible tab if needed.
		if visEnd < len(allTabs) {
			for used+3 > avail && visEnd > m.tabScroll+1 {
				visEnd--
				extra := allTabs[visEnd].width + 1
				used -= extra
			}
		}

		if target < visEnd {
			break // target is visible
		}
		// Scroll right to reveal the target
		m.tabScroll++
		hasLeft = true
		if m.tabScroll >= len(allTabs) {
			m.tabScroll = len(allTabs) - 1
			break
		}
	}
}

// tabInfo holds pre-computed data for a single tab in the bar.
type tabInfo struct {
	label string
	dot   string // pre-rendered status dot (foreground only, no background)
	style lipgloss.Style
	width int // rendered cell width of " dot label "
}

// buildTabInfos creates the full list of tabInfo (sessions + new-tab button).
func (m tuiModel) buildTabInfos() []tabInfo {
	var tabs []tabInfo

	for i, s := range m.sessions {
		label := s.name

		var style lipgloss.Style
		if i == m.active {
			if m.tabFocused && !m.newTabFocused {
				style = tuiTabActiveFocused
			} else {
				style = tuiTabActive
			}
		} else if s.state == tuiStateConfirm || s.state == tuiStatePlanConfirm {
			style = tuiTabNeedsAttention
		} else if s.state == tuiStateRunning {
			style = tuiTabRunning
		} else {
			style = tuiTabInactive
		}

		var dotColor lipgloss.TerminalColor
		if s.state == tuiStateConfirm || s.state == tuiStatePlanConfirm {
			dotColor = lipgloss.Color("1")
		} else if s.state == tuiStateRunning {
			dotColor = lipgloss.Color("3")
		}
		dot := ""
		if dotColor != nil {
			dotStyle := lipgloss.NewStyle().Foreground(dotColor)
			dot = dotStyle.Render("●") + " "
		}

		// Width measures content between │ borders: " " + dot + style(" label ") + " "
		w := lipgloss.Width(" " + dot + style.Render(" "+label+" ") + " ")
		tabs = append(tabs, tabInfo{label: label, dot: dot, style: style, width: w})
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
	nw := lipgloss.Width(" " + newStyle.Render(" "+newLabel+" ") + " ")
	tabs = append(tabs, tabInfo{label: newLabel, style: newStyle, width: nw})

	return tabs
}

// renderTabBar builds the tab bar that connects to the status box bottom border.
// Returns (mergedBorder, tabRows) where mergedBorder replaces the box's bottom
// border and incorporates the tab top edges, and tabRows has the label + bottom rows.
// Tabs are scrollable — only a visible window is rendered, with ◀/▶ arrows.
func (m tuiModel) renderTabBar(boxWidth int) (string, string) {
	allTabs := m.buildTabInfos()

	// Determine visible range based on tabScroll and available width.
	avail := boxWidth
	hasLeftArrow := m.tabScroll > 0
	if hasLeftArrow {
		avail -= 3 // " ◀ "
	}

	// Walk from tabScroll forward, fitting as many tabs as possible.
	visStart := m.tabScroll
	visEnd := visStart
	used := 0
	for i := visStart; i < len(allTabs); i++ {
		extra := allTabs[i].width + 1 // content + shared right border
		if i == visStart {
			extra++ // left border for first visible tab
		}
		if used+extra > avail {
			break
		}
		used += extra
		visEnd = i + 1
	}

	hasRightArrow := visEnd < len(allTabs)

	// If adding the right arrow steals space and we need to drop the last tab, do so.
	if hasRightArrow {
		for used+3 > avail && visEnd > visStart+1 {
			visEnd--
			extra := allTabs[visEnd].width + 1
			used -= extra
		}
	}

	// Always show at least one tab, even if it overflows.
	if visEnd <= visStart && visStart < len(allTabs) {
		visEnd = visStart + 1
	}

	// If left arrow appeared after we initially didn't account for it, recheck.
	// (tabScroll > 0 was handled above, so this is just a safety net.)

	visTabs := allTabs[visStart:visEnd]

	// ── Build the merged border line ──────────────────────────────────────
	// Position 0 = ╰, positions 1..boxWidth = ─, position boxWidth+1 = ╯
	totalWidth := boxWidth + 2
	border := make([]rune, totalWidth)
	for i := range border {
		border[i] = '─'
	}
	border[0] = '╰'
	border[totalWidth-1] = '╯'

	// Calculate starting position for tabs on the border
	pos := 1 // leading space offset
	if hasLeftArrow {
		pos += 3 // " ◀ "
	}

	for i, t := range visTabs {
		leftEdge := pos
		rightEdge := pos + t.width + 1
		if leftEdge > 0 && leftEdge < totalWidth-1 {
			border[leftEdge] = '┬'
		}
		if rightEdge > 0 && rightEdge < totalWidth-1 {
			border[rightEdge] = '┬'
		}
		if i < len(visTabs)-1 {
			pos = rightEdge
		}
	}

	border[0] = '╰'
	border[totalWidth-1] = '╯'
	mergedBorder := tuiBorder.Render(string(border))

	// ── Build label row and bottom row ────────────────────────────────────
	var midBuf, botBuf strings.Builder

	// Left arrow indicator
	if hasLeftArrow {
		midBuf.WriteString(tuiDim.Render(" ◀ "))
		botBuf.WriteString(tuiDim.Render("   "))
	}

	midBuf.WriteString(" ")
	botBuf.WriteString(" ")

	for i, t := range visTabs {
		midBuf.WriteString(tuiBorder.Render("│"))
		midBuf.WriteString(" " + t.dot + t.style.Render(" "+t.label+" ") + " ")
		if i == len(visTabs)-1 {
			midBuf.WriteString(tuiBorder.Render("│"))
		}

		if i == 0 {
			botBuf.WriteString(tuiBorder.Render("╰"))
		} else {
			botBuf.WriteString(tuiBorder.Render("┴"))
		}
		botBuf.WriteString(tuiBorder.Render(strings.Repeat("─", t.width)))
		if i == len(visTabs)-1 {
			botBuf.WriteString(tuiBorder.Render("╯"))
		}
	}

	// Right arrow indicator
	if hasRightArrow {
		midBuf.WriteString(tuiDim.Render(" ▶ "))
		botBuf.WriteString(tuiDim.Render("   "))
	}

	midRow := midBuf.String()
	botRow := botBuf.String()

	if m.tabFocused {
		hint := "  ←/→ navigate  enter select  esc return"
		midRowWidth := lipgloss.Width(midRow)
		remaining := boxWidth + 2 - midRowWidth // +2 for outer box edges
		if remaining >= 4 {
			if len(hint) > remaining {
				hint = hint[:remaining]
			}
			midRow += tuiDim.Render(hint)
		}
	}

	return mergedBorder, midRow + "\n" + botRow
}

// ─── TUI helpers ─────────────────────────────────────────────────────────────

// saveAllSessions persists every open session to disk with its tab position.
func (m *tuiModel) saveAllSessions() {
	for i, s := range m.sessions {
		saveSession(m.cwd, s, i+1) // 1-based tab order
	}
}

// tabOrderOf returns the 1-based tab position of a session, or 0 if not found.
func (m *tuiModel) tabOrderOf(s *session) int {
	for i, sess := range m.sessions {
		if sess.id == s.id {
			return i + 1
		}
	}
	return 0
}

// scrollTabsToActive ensures the active tab (or new-tab button) is visible.
func (m *tuiModel) scrollTabsToActive() {
	innerWidth := m.width - 2
	if innerWidth < 1 {
		innerWidth = 1
	}
	m.ensureActiveTabVisible(innerWidth)
}

// resizeAll updates the layout for the active session based on current terminal size.
func (m *tuiModel) resizeAll() {
	s := m.cur()
	// Layout (rows below the viewport):
	//   1  blank line
	//   1  top border         ╭───╮
	//   N  input lines        │...│  (dynamic, 1..maxInputHeight) — mode right-justified
	//   1  mid border         ├───┤    (only if hudMode == hudStatusBar)
	//   1  status line        │...│    (only if hudMode == hudStatusBar)
	//   1  merged border      ╰─┤..├─╯  (box bottom + tab tops)
	//   2  tab rows           │..│ │..│
	//                         ╰──╯ ╰──╯
	//   = 7 + N (with status bar) or 5 + N (without)
	chrome := 7 + s.inputHeight
	if m.hudMode != hudStatusBar {
		chrome -= 2 // no mid border + status row
	}
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
		return tuiStatusBar.Render("git ") + tuiStatusGitDirty.Render(branch + "*")
	}
	return tuiStatusBar.Render("git ") + tuiStatusGitClean.Render(branch)
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

// overlayHUD composites a small HUD box onto the top-right of the viewport output.
// viewportStr is the full viewport render, hudLines are the content lines,
// and totalWidth is the terminal width.
func overlayHUD(viewportStr string, hudLines []string, totalWidth int) string {
	if len(hudLines) == 0 || totalWidth < 20 {
		return viewportStr
	}

	// Compute max content width of HUD lines
	maxW := 0
	for _, l := range hudLines {
		w := lipgloss.Width(l)
		if w > maxW {
			maxW = w
		}
	}

	boxInner := maxW + 2 // 1 padding each side
	boxOuter := boxInner + 2 // +2 for border chars │ │

	if boxOuter > totalWidth-4 {
		return viewportStr // not enough room
	}

	// Build the HUD box lines (border + content)
	var box []string
	box = append(box, tuiDim.Render("╭"+strings.Repeat("─", boxInner)+"╮"))
	for _, l := range hudLines {
		w := lipgloss.Width(l)
		pad := boxInner - 1 - w // 1 char left pad already
		if pad < 0 {
			pad = 0
		}
		box = append(box, tuiDim.Render("│")+" "+l+strings.Repeat(" ", pad)+tuiDim.Render("│"))
	}
	box = append(box, tuiDim.Render("╰"+strings.Repeat("─", boxInner)+"╯"))

	// Split viewport into lines and overlay box onto top-right
	vpLines := strings.Split(viewportStr, "\n")

	// Start at line 0, right-aligned with 1 char margin from right edge
	for i, boxLine := range box {
		if i >= len(vpLines) {
			break
		}
		vpLines[i] = overlayLine(vpLines[i], boxLine, totalWidth)
	}

	return strings.Join(vpLines, "\n")
}

// overlayLine places overlayStr at the right side of baseLine, replacing characters.
func overlayLine(baseLine, overlayStr string, totalWidth int) string {
	overlayW := lipgloss.Width(overlayStr)
	baseW := lipgloss.Width(baseLine)

	// Position: right-aligned with 1 char margin
	startCol := totalWidth - overlayW - 1
	if startCol < 0 {
		startCol = 0
	}

	// Pad base line to full width if needed
	if baseW < totalWidth {
		baseLine += strings.Repeat(" ", totalWidth-baseW)
	}

	// We need to do a character-level splice. Since ANSI sequences complicate
	// things, use ansi.Truncate to get the left portion, then append overlay + right pad.
	left := ansi.Truncate(baseLine, startCol, "")
	leftW := lipgloss.Width(left)

	// Pad left to exact start column if truncation fell short
	if leftW < startCol {
		left += strings.Repeat(" ", startCol-leftW)
	}

	// After the overlay, pad to total width
	rightStart := startCol + overlayW
	rightPad := ""
	if rightStart < totalWidth {
		rightPad = strings.Repeat(" ", totalWidth-rightStart)
	}

	return left + overlayStr + rightPad
}

// notifyCmd sends a desktop notification via OSC 9 and a terminal bell.
// OSC 9 shows a native notification (supported by iTerm2, Ghostty, etc.);
// the bell triggers fallback alerts (e.g. dock bounce, tab flash).
// Both written directly to stderr to work in alt-screen mode.
func notifyCmd(title string) tea.Cmd {
	return func() tea.Msg {
		os.Stderr.Write([]byte("\x1b]9;" + title + "\x1b\\\a"))
		return nil
	}
}

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
	return fmt.Sprintf("$%.2f", c)
}

func shortenPath(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(p, home) {
		p = "~" + p[len(home):]
	}
	return p
}

// ─── Settings persistence ───────────────────────────────────────────────────

// settingsPath returns the path to .cogent/settings in the given directory.
func settingsPath(cwd string) string {
	return filepath.Join(cwd, ".cogent", "settings")
}

// loadSettings reads the .cogent/settings file and returns a key→value map.
func loadSettings(cwd string) map[string]string {
	m := make(map[string]string)
	data, err := os.ReadFile(settingsPath(cwd))
	if err != nil {
		return m
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return m
}

// saveSetting writes a single key=value into .cogent/settings, updating an
// existing key or appending a new one.
func saveSetting(cwd, key, value string) {
	path := settingsPath(cwd)
	settings := loadSettings(cwd)
	settings[key] = value

	var lines []string
	for k, v := range settings {
		lines = append(lines, k+"="+v)
	}
	// sort for deterministic output
	sort.Strings(lines)

	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

// loadHUDMode reads the saved HUD mode from .cogent/settings.
func loadHUDMode(cwd string) hudMode {
	s := loadSettings(cwd)
	switch s["hud"] {
	case "overlay":
		return hudOverlay
	case "off":
		return hudOff
	default:
		return hudStatusBar
	}
}

// saveHUDMode persists the current HUD mode to .cogent/settings.
func saveHUDMode(cwd string, mode hudMode) {
	labels := map[hudMode]string{
		hudStatusBar: "status_bar",
		hudOverlay:   "overlay",
		hudOff:       "off",
	}
	saveSetting(cwd, "hud", labels[mode])
}
