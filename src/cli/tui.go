package cli

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"

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
			Foreground(lipgloss.Color("7"))

	tuiStatusKey = lipgloss.NewStyle().
			Foreground(lipgloss.Color("4")).
			Bold(true)

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
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
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

// ─── Bubble Tea model ────────────────────────────────────────────────────────

type tuiState int

const (
	tuiStateInput   tuiState = iota
	tuiStateRunning
	tuiStateConfirm
)

const maxInputHeight = 10 // max lines the input area can grow to

type tuiModel struct {
	agent    *agent.Agent
	client   *api.Client
	cwd      string
	width    int
	height   int
	state    tuiState
	output   viewport.Model
	input    textarea.Model
	lines    []string
	confirm  *tuiConfirmMsg
	quitting       bool
	scrollback     bool                // true when user has scrolled up from bottom
	cancelFn       context.CancelFunc  // cancels the in-flight agent call
	msgCh          chan tea.Msg
	inputHeight    int                 // current visual height of the input area
	initialPrompt  string              // if set, sent automatically on Init

	// Status bar stats
	contextUsed int     // tokens used in last response (input + output)
	cacheRead   int     // cache read tokens in last response
	cacheCreate int     // cache creation tokens in last response
	lastCost    float64 // cost of last API call
	totalCost   float64 // cumulative cost
}

func newTUIModel(client *api.Client, cwd string, prompt string) tuiModel {
	ta := textarea.New()
	ta.Placeholder = "Ask anything..."
	ta.Prompt = "❯ "
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.Focus()

	vp := viewport.New(80, 20)
	vp.SetContent("")
	// Disable all default keybindings — we handle scrolling manually to avoid
	// conflicts with the textarea (arrow keys, j/k, etc.).
	vp.KeyMap = viewport.KeyMap{
		PageDown:     key.NewBinding(key.WithDisabled()),
		PageUp:       key.NewBinding(key.WithDisabled()),
		HalfPageUp:   key.NewBinding(key.WithDisabled()),
		HalfPageDown: key.NewBinding(key.WithDisabled()),
		Down:         key.NewBinding(key.WithDisabled()),
		Up:           key.NewBinding(key.WithDisabled()),
		Left:         key.NewBinding(key.WithDisabled()),
		Right:        key.NewBinding(key.WithDisabled()),
	}

	msgCh := make(chan tea.Msg, 64)

	m := tuiModel{
		client:        client,
		cwd:           cwd,
		input:         ta,
		output:        vp,
		state:         tuiStateInput,
		msgCh:         msgCh,
		inputHeight:   1,
		initialPrompt: prompt,
	}

	m.agent = agent.New(client, cwd,
		agent.WithTextCallback(func(text string) {
			msgCh <- tuiAppendMsg{text: tuiDim.Render(text)}
		}),
		agent.WithToolCallback(func(name, summary string) {
			style := tuiGreen
			switch name {
			case "bash", "write", "edit":
				style = tuiRed
			}
			line := tuiDim.Render(" "+style.Render(name)) + " " + tuiDim.Render(summary)
			msgCh <- tuiAppendMsg{text: line}
		}),
		agent.WithToolResultCallback(func(name, result string, isError bool) {
			if result == "" {
				return
			}
			// Truncate very long results to keep the viewport manageable
			const maxLines = 3
			lines := strings.Split(result, "\n")
			truncated := false
			if len(lines) > maxLines {
				lines = lines[:maxLines]
				truncated = true
			}
			style := tuiDim
			if isError {
				style = tuiRed
			}
			for _, line := range lines {
				msgCh <- tuiAppendMsg{text: style.Render("  " + line)}
			}
			if truncated {
				msgCh <- tuiAppendMsg{text: tuiYellow.Render(fmt.Sprintf("  ... output truncated (%d lines shown)", maxLines))}
			}
		}),
		agent.WithConfirmCallback(func(name string, input map[string]any) agent.ConfirmResult {
			reply := make(chan agent.ConfirmResult)
			msgCh <- tuiConfirmMsg{name: name, input: input, reply: reply}
			return <-reply
		}),
		agent.WithUsageCallback(func(usage api.Usage) {
			msgCh <- tuiUsageMsg{usage: usage}
		}),
		agent.WithCompactionCallback(func() {
			msgCh <- tuiCompactionMsg{}
		}),
	)

	// Welcome line
	m.lines = []string{
		tuiDim.Render(fmt.Sprintf("cogent — model: %s | cwd: %s", client.Model(), cwd)),
		"",
	}

	return m
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
		// Scrollback keys: PgUp/PgDn/Up/Down work in any state.
		switch msg.Type {
		case tea.KeyPgUp:
			m.output.PageUp()
			m.scrollback = !m.output.AtBottom()
			return m, nil
		case tea.KeyPgDown:
			m.output.PageDown()
			m.scrollback = !m.output.AtBottom()
			return m, nil
		case tea.KeyUp:
			// When not typing (running/confirm), arrow keys scroll the viewport.
			if m.state != tuiStateInput {
				m.output.ScrollUp(3)
				m.scrollback = !m.output.AtBottom()
				return m, nil
			}
		case tea.KeyDown:
			if m.state != tuiStateInput {
				m.output.ScrollDown(3)
				m.scrollback = !m.output.AtBottom()
				return m, nil
			}
		case tea.KeyShiftTab:
			// Cycle permission mode — works in input and running states so you
			// can switch e.g. from YOLO back to Confirm mid-execution.
			if m.state == tuiStateInput || m.state == tuiStateRunning {
				newMode := agent.CyclePermissionMode(m.agent.GetPermissionMode())
				m.agent.SetPermissionMode(newMode)
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
				// Update prompt character based on mode
				if newMode == agent.ModeTerminal {
					m.input.Prompt = "$ "
					m.input.Placeholder = "Run a command..."
				} else {
					m.input.Prompt = "❯ "
					m.input.Placeholder = "Ask anything..."
				}
				m.appendLine(tuiDim.Render("  mode → ") + style.Render(newMode.String()))
				return m, nil
			}
		}

		switch m.state {
		case tuiStateConfirm:
			return m.handleConfirm(msg)
		default:
			return m.handleInput(msg)
		}

	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			var cmd tea.Cmd
			m.output, cmd = m.output.Update(msg)
			m.scrollback = !m.output.AtBottom()
			return m, cmd
		}
		// Ignore all other mouse events so they don't reach the textarea
		// (which would insert control characters into the prompt).
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()             // update widths first
		m.recalcInputHeight()  // then recalc with new wrap width

	case tuiInitialPromptMsg:
		prompt := m.initialPrompt
		m.initialPrompt = "" // consume it
		m.appendLine(tuiPrompt.Render("❯ " + prompt))
		m.state = tuiStateRunning
		m.input.Blur()
		return m, m.sendToAgent(prompt)

	case tuiAppendMsg:
		m.appendLine(msg.text)
		cmds = append(cmds, m.waitForMsg())

	case tuiUsageMsg:
		m.contextUsed = msg.usage.ContextUsed()
		m.cacheRead = msg.usage.CacheReadInputTokens
		m.cacheCreate = msg.usage.CacheCreationInputTokens
		cost := m.client.CostForUsage(msg.usage)
		m.lastCost = cost
		m.totalCost += cost
		cmds = append(cmds, m.waitForMsg())

	case tuiCompactionMsg:
		m.appendLine(tuiDim.Render("  ⚡ context compacted"))
		cmds = append(cmds, m.waitForMsg())

	case tuiConfirmMsg:
		m.confirm = &msg
		m.state = tuiStateConfirm
		m.appendLine(tuiRenderDiff(msg.name, msg.input))
		summary := SummarizeConfirm(msg.name, msg.input)
		m.appendLine(tuiYellow.Render(fmt.Sprintf("Allow %s %s? [Y/n/a] ", msg.name, summary)))
		cmds = append(cmds, m.waitForMsg())

	case tuiDoneMsg:
		m.state = tuiStateInput
		if msg.err != nil {
			m.appendLine(tuiYellow.Render("Error: " + msg.err.Error()))
		}
		m.appendLine("")
		m.input.Focus()
		cmds = append(cmds, textarea.Blink)

	case tuiShellDoneMsg:
		m.state = tuiStateInput
		if msg.err != nil {
			m.appendLine(tuiRed.Render("Error: " + msg.err.Error()))
		}
		m.appendLine("")
		m.input.Focus()
		cmds = append(cmds, textarea.Blink)
	}

	if m.state == tuiStateInput {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.recalcInputHeight()
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *tuiModel) handleInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		if m.state == tuiStateRunning {
			// Interrupt the in-flight agent call instead of quitting.
			if m.cancelFn != nil {
				m.cancelFn()
				m.cancelFn = nil
			}
			m.appendLine(tuiDim.Render("  ⏎ interrupted"))
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit

	case tea.KeyEnter:
		value := strings.TrimSpace(m.input.Value())
		if value == "" {
			return m, nil
		}
		m.input.Reset()
		m.inputHeight = 1
		m.input.SetHeight(1)
		m.resize()

		switch value {
		case "/quit", "/exit", "/q":
			m.quitting = true
			return m, tea.Quit
		case "/clear":
			m.agent.Reset()
			m.lines = nil
			m.output.SetContent("")
			m.appendLine(tuiDim.Render("Conversation cleared."))
			return m, nil
		case "/help":
			m.appendLine(tuiDim.Render("Commands: /help /clear /quit"))
			m.appendLine(tuiDim.Render("Shift+Tab: cycle permission mode (Confirm → Plan → YOLO → Terminal)"))
			m.appendLine(tuiDim.Render("Scroll: PgUp/PgDn, ↑/↓ arrows (while agent is running), mouse wheel"))
			m.appendLine(tuiDim.Render("Confirmations: y=allow, n=deny, a=always allow this tool for session"))
			m.appendLine(tuiDim.Render("Terminal mode: input runs as shell commands"))
			m.appendLine(tuiDim.Render("Env: ANTHROPIC_API_KEY, ANTHROPIC_MODEL, ANTHROPIC_BASE_URL"))
			return m, nil
		}

		// Terminal mode: run as shell command
		if m.agent.GetPermissionMode() == agent.ModeTerminal {
			m.appendLine(tuiYellow.Render("$ " + value))
			m.state = tuiStateRunning
			m.input.Blur()
			return m, m.runShellCommand(value)
		}

		m.appendLine(tuiPrompt.Render("❯ " + value))
		m.state = tuiStateRunning
		m.input.Blur()
		return m, m.sendToAgent(value)

	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.recalcInputHeight()
		return m, cmd
	}
}

func (m *tuiModel) handleConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirm == nil {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyCtrlC:
		m.appendLine(tuiRed.Render("  ✗ denied (interrupted)"))
		m.confirm.reply <- agent.ConfirmDeny
		m.confirm = nil
		if m.cancelFn != nil {
			m.cancelFn()
			m.cancelFn = nil
		}
		m.state = tuiStateRunning
		return m, nil

	case tea.KeyEnter:
		m.appendLine(tuiGreen.Render("  ✓ allowed"))
		m.confirm.reply <- agent.ConfirmAllow
		m.confirm = nil
		m.state = tuiStateRunning
		return m, nil

	case tea.KeyRunes:
		ch := strings.ToLower(string(msg.Runes))
		switch ch {
		case "y":
			m.appendLine(tuiGreen.Render("  ✓ allowed"))
			m.confirm.reply <- agent.ConfirmAllow
			m.confirm = nil
			m.state = tuiStateRunning
			return m, nil
		case "n":
			m.appendLine(tuiRed.Render("  ✗ denied"))
			m.confirm.reply <- agent.ConfirmDeny
			m.confirm = nil
			m.state = tuiStateRunning
			return m, nil
		case "a":
			toolName := m.confirm.name
			m.appendLine(tuiGreen.Render(fmt.Sprintf("  ✓ always allow %s", toolName)))
			m.confirm.reply <- agent.ConfirmAlways
			m.confirm = nil
			m.state = tuiStateRunning
			return m, nil
		}
	}
	return m, nil
}

func (m *tuiModel) sendToAgent(prompt string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFn = cancel
	return func() tea.Msg {
		err := m.agent.SendCtx(ctx, prompt)
		if err != nil && ctx.Err() != nil {
			// Cancelled by user — not a real error.
			return tuiDoneMsg{err: nil}
		}
		return tuiDoneMsg{err: err}
	}
}

func (m *tuiModel) runShellCommand(command string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("sh", "-c", command)
		cmd.Dir = m.cwd
		// Scrub API key from subprocess environment.
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "ANTHROPIC_API_KEY=") {
				cmd.Env = append(cmd.Env, e)
			}
		}
		out, err := cmd.CombinedOutput()
		output := strings.TrimRight(string(out), "\n")
		if output != "" {
			for _, line := range strings.Split(output, "\n") {
				m.msgCh <- tuiAppendMsg{text: tuiDim.Render("  " + line)}
			}
		}
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				m.msgCh <- tuiAppendMsg{text: tuiRed.Render(fmt.Sprintf("  (exit code %d)", exitErr.ExitCode()))}
				return tuiShellDoneMsg{err: nil}
			}
			return tuiShellDoneMsg{err: err}
		}
		return tuiShellDoneMsg{err: nil}
	}
}

// ─── View ────────────────────────────────────────────────────────────────────

func (m tuiModel) View() string {
	if m.quitting {
		return tuiDim.Render("Goodbye!") + "\n"
	}

	innerWidth := m.width - 2 // account for left+right border chars
	if innerWidth < 0 {
		innerWidth = 0
	}

	var b strings.Builder
	b.WriteString(m.output.View())
	b.WriteString("\n")

	// Build the prompt content
	var promptContent string
	switch m.state {
	case tuiStateConfirm:
		promptContent = tuiStatus.Render(" y/n/a ")
	case tuiStateRunning:
		if m.agent.GetPermissionMode() == agent.ModeTerminal {
			promptContent = tuiStatus.Render(" running... ") + tuiDim.Render("(ctrl+c to interrupt)")
		} else {
			promptContent = tuiStatus.Render(" thinking... ") + tuiDim.Render("(ctrl+c to interrupt)")
		}
	default:
		promptContent = m.input.View()
	}

	// Build status bar content
	statusContent := m.renderStatusBar()

	// Draw box around prompt + status bar
	topBorder := tuiBorder.Render("╭" + strings.Repeat("─", innerWidth) + "╮")
	midBorder := tuiBorder.Render("├" + strings.Repeat("─", innerWidth) + "┤")
	botBorder := tuiBorder.Render("╰" + strings.Repeat("─", innerWidth) + "╯")
	leftEdge := tuiBorder.Render("│")
	rightEdge := tuiBorder.Render("│")

	b.WriteString(topBorder)
	b.WriteString("\n")

	// Render prompt lines — the textarea may span multiple visual lines.
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
	b.WriteString(botBorder)

	return b.String()
}

// ─── TUI helpers ─────────────────────────────────────────────────────────────

func (m *tuiModel) resize() {
	// Layout (lines below the viewport):
	//   1  newline after viewport
	//   1  top border    ┌───┐
	//   N  input lines   │...│  (dynamic, 1..maxInputHeight)
	//   1  mid border    ├───┤
	//   1  status line   │...│
	//   1  bottom border └───┘
	//   = 5 + N
	chrome := 5 + m.inputHeight
	m.output.Width = m.width
	vpHeight := m.height - chrome
	if vpHeight < 1 {
		vpHeight = 1
	}
	m.output.Height = vpHeight
	m.input.SetWidth(m.width - 2) // account for left+right border
}

// recalcInputHeight computes how many visual lines the current input text
// occupies (accounting for soft-wrapping at the terminal width) and resizes
// the textarea + viewport accordingly.
func (m *tuiModel) recalcInputHeight() {
	needed := m.inputVisualLines()
	if needed < 1 {
		needed = 1
	}
	if needed > maxInputHeight {
		needed = maxInputHeight
	}
	if needed != m.inputHeight {
		m.inputHeight = needed
		m.input.SetHeight(needed)
		m.resize()
	}
}

// inputVisualLines returns the number of visual (rendered) lines the current
// textarea content occupies, accounting for soft wrapping.
func (m *tuiModel) inputVisualLines() int {
	value := m.input.Value()
	if value == "" {
		return 1
	}

	// Width() returns the pure text wrapping width (already excludes prompt
	// width, line numbers, and base style frame).
	wrapWidth := m.input.Width()
	if wrapWidth < 1 {
		wrapWidth = 1
	}

	total := 0
	for _, line := range strings.Split(value, "\n") {
		lineWidth := uniseg.StringWidth(line)
		if lineWidth == 0 {
			total++ // empty line still occupies one visual row
		} else {
			total += int(math.Ceil(float64(lineWidth) / float64(wrapWidth)))
		}
	}
	return total
}

func (m *tuiModel) appendLine(text string) {
	m.lines = append(m.lines, text)
	m.output.SetContent(strings.Join(m.lines, "\n"))
	if !m.scrollback {
		m.output.GotoBottom()
	}
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

// ─── Status bar ─────────────────────────────────────────────────────────────

func (m tuiModel) renderStatusBar() string {
	if m.width == 0 {
		return ""
	}

	sep := tuiStatusBar.Render("  │  ")

	// Model
	model := tuiStatusKey.Render(m.client.Model())

	// Permission mode
	mode := m.agent.GetPermissionMode()
	var modeStr string
	switch mode {
	case agent.ModePlan:
		modeStr = tuiModePlan.Render("Plan")
	case agent.ModeYOLO:
		modeStr = tuiModeYOLO.Render("YOLO")
	case agent.ModeTerminal:
		modeStr = tuiModeTerminal.Render("Terminal")
	default:
		modeStr = tuiModeConfirm.Render("Confirm")
	}

	// Context: used / total + cache info
	contextTotal := m.client.ContextWindow()
	contextStr := tuiStatusBar.Render(fmt.Sprintf("%s/%s",
		formatTokens(m.contextUsed), formatTokens(contextTotal)))
	if m.cacheRead > 0 {
		contextStr += tuiGreen.Render(fmt.Sprintf(" ⚡%s read", formatTokens(m.cacheRead)))
	}
	if m.cacheCreate > 0 {
		contextStr += tuiYellow.Render(fmt.Sprintf(" +%s write", formatTokens(m.cacheCreate)))
	}

	// Cost
	lastCostStr := tuiStatusBar.Render(formatCost(m.lastCost))
	totalCostStr := tuiStatusBar.Render(formatCost(m.totalCost))

	// PWD (shortened)
	pwd := shortenPath(m.cwd)
	pwdStr := tuiStatusBar.Render(pwd)

	// Git
	gitStr := m.renderGitStatus()

	// Assemble left side
	left := tuiStatusBar.Render(" ") +
		model + sep +
		modeStr + tuiStatusBar.Render(" (shift+tab)") + sep +
		tuiStatusBar.Render("ctx ") + contextStr + sep +
		tuiStatusBar.Render("last ") + lastCostStr +
		tuiStatusBar.Render(" total ") + totalCostStr + sep +
		pwdStr

	if gitStr != "" {
		left += sep + gitStr
	}

	left += tuiStatusBar.Render(" ")

	return left
}

func (m tuiModel) renderGitStatus() string {
	branch := gitBranch(m.cwd)
	if branch == "" {
		return ""
	}
	dirty := gitDirty(m.cwd)
	if dirty {
		return tuiStatusGitDirty.Render(" " + branch + "*")
	}
	return tuiStatusGitClean.Render(" " + branch)
}

// ─── Git helpers ────────────────────────────────────────────────────────────

func gitBranch(dir string) string {
	// Fast path: read .git/HEAD directly
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
	// Detached HEAD — return short hash
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
