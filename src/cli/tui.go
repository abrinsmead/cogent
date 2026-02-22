package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
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

	tuiStatusBar = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")).
			Background(lipgloss.Color("236"))

	tuiStatusKey = lipgloss.NewStyle().
			Foreground(lipgloss.Color("4")).
			Background(lipgloss.Color("236")).
			Bold(true)

	tuiStatusGitClean = lipgloss.NewStyle().
				Foreground(lipgloss.Color("2")).
				Background(lipgloss.Color("236"))

	tuiStatusGitDirty = lipgloss.NewStyle().
				Foreground(lipgloss.Color("3")).
				Background(lipgloss.Color("236"))
)

// ─── TUI (public wrapper) ───────────────────────────────────────────────────

// TUI is the Bubble Tea full-screen interactive mode.
type TUI struct {
	client *api.Client
	cwd    string
}

func NewTUI(client *api.Client, cwd string) *TUI {
	return &TUI{client: client, cwd: cwd}
}

func (t *TUI) Run() error {
	m := newTUIModel(t.client, t.cwd)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// ─── Bubble Tea messages ─────────────────────────────────────────────────────

type tuiAppendMsg struct{ text string }
type tuiDoneMsg struct{ err error }
type tuiUsageMsg struct{ usage api.Usage }
type tuiConfirmMsg struct {
	name  string
	input map[string]any
	reply chan bool
}

// ─── Bubble Tea model ────────────────────────────────────────────────────────

type tuiState int

const (
	tuiStateInput   tuiState = iota
	tuiStateRunning
	tuiStateConfirm
)

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
	quitting bool
	msgCh    chan tea.Msg

	// Status bar stats
	contextUsed int     // tokens used in last response (input + output)
	lastCost    float64 // cost of last API call
	totalCost   float64 // cumulative cost
}

func newTUIModel(client *api.Client, cwd string) tuiModel {
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

	msgCh := make(chan tea.Msg, 64)

	m := tuiModel{
		client: client,
		cwd:    cwd,
		input:  ta,
		output: vp,
		state:  tuiStateInput,
		msgCh:  msgCh,
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
		agent.WithConfirmCallback(func(name string, input map[string]any) bool {
			reply := make(chan bool)
			msgCh <- tuiConfirmMsg{name: name, input: input, reply: reply}
			return <-reply
		}),
		agent.WithUsageCallback(func(usage api.Usage) {
			msgCh <- tuiUsageMsg{usage: usage}
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
	return tea.Batch(textarea.Blink, m.waitForMsg())
}

func (m tuiModel) waitForMsg() tea.Cmd {
	return func() tea.Msg { return <-m.msgCh }
}

// ─── Update ──────────────────────────────────────────────────────────────────

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.state {
		case tuiStateConfirm:
			return m.handleConfirm(msg)
		default:
			return m.handleInput(msg)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()

	case tuiAppendMsg:
		m.appendLine(msg.text)
		cmds = append(cmds, m.waitForMsg())

	case tuiUsageMsg:
		m.contextUsed = msg.usage.ContextUsed()
		cost := m.client.CostForUsage(msg.usage)
		m.lastCost = cost
		m.totalCost += cost
		cmds = append(cmds, m.waitForMsg())

	case tuiConfirmMsg:
		m.confirm = &msg
		m.state = tuiStateConfirm
		m.appendLine(tuiRenderDiff(msg.name, msg.input))
		summary := SummarizeConfirm(msg.name, msg.input)
		m.appendLine(tuiYellow.Render(fmt.Sprintf("Allow %s %s? [Y/n] ", msg.name, summary)))
		cmds = append(cmds, m.waitForMsg())

	case tuiDoneMsg:
		m.state = tuiStateInput
		if msg.err != nil {
			m.appendLine(tuiYellow.Render("Error: " + msg.err.Error()))
		}
		m.appendLine("")
		m.input.Focus()
		cmds = append(cmds, textarea.Blink)
	}

	if m.state == tuiStateInput {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *tuiModel) handleInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.quitting = true
		return m, tea.Quit

	case tea.KeyEnter:
		value := strings.TrimSpace(m.input.Value())
		if value == "" {
			return m, nil
		}
		m.input.Reset()

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
			m.appendLine(tuiDim.Render("Env: ANTHROPIC_API_KEY, ANTHROPIC_MODEL, ANTHROPIC_BASE_URL"))
			return m, nil
		}

		m.appendLine(tuiPrompt.Render("❯ " + value))
		m.state = tuiStateRunning
		m.input.Blur()
		return m, m.sendToAgent(value)

	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
}

func (m *tuiModel) handleConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirm == nil {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyCtrlC:
		m.confirm.reply <- false
		m.confirm = nil
		m.quitting = true
		return m, tea.Quit

	case tea.KeyEnter:
		m.appendLine(tuiGreen.Render("  ✓ allowed"))
		m.confirm.reply <- true
		m.confirm = nil
		m.state = tuiStateRunning
		return m, nil

	case tea.KeyRunes:
		ch := strings.ToLower(string(msg.Runes))
		switch ch {
		case "y":
			m.appendLine(tuiGreen.Render("  ✓ allowed"))
			m.confirm.reply <- true
			m.confirm = nil
			m.state = tuiStateRunning
			return m, nil
		case "n":
			m.appendLine(tuiRed.Render("  ✗ denied"))
			m.confirm.reply <- false
			m.confirm = nil
			m.state = tuiStateRunning
			return m, nil
		}
	}
	return m, nil
}

func (m *tuiModel) sendToAgent(prompt string) tea.Cmd {
	return func() tea.Msg {
		err := m.agent.Send(prompt)
		return tuiDoneMsg{err: err}
	}
}

// ─── View ────────────────────────────────────────────────────────────────────

func (m tuiModel) View() string {
	if m.quitting {
		return tuiDim.Render("Goodbye!") + "\n"
	}

	var b strings.Builder
	b.WriteString(m.output.View())
	b.WriteString("\n")

	switch m.state {
	case tuiStateConfirm:
		b.WriteString(tuiStatus.Render(" y/n "))
	case tuiStateRunning:
		b.WriteString(tuiStatus.Render(" thinking... "))
	default:
		b.WriteString(m.input.View())
	}

	b.WriteString("\n")
	b.WriteString(m.renderStatusBar())

	return b.String()
}

// ─── TUI helpers ─────────────────────────────────────────────────────────────

func (m *tuiModel) resize() {
	// Layout: viewport + 1 (newline) + input(1 visible line + 2 chrome) + 1 (newline) + status bar(1)
	inputHeight := 3
	statusHeight := 2 // newline + status bar
	m.output.Width = m.width
	m.output.Height = m.height - inputHeight - statusHeight
	m.input.SetWidth(m.width)
}

func (m *tuiModel) appendLine(text string) {
	m.lines = append(m.lines, text)
	m.output.SetContent(strings.Join(m.lines, "\n"))
	m.output.GotoBottom()
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

	// Context: used / total
	contextTotal := m.client.ContextWindow()
	contextStr := tuiStatusBar.Render(fmt.Sprintf("%s/%s",
		formatTokens(m.contextUsed), formatTokens(contextTotal)))

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
		tuiStatusBar.Render("ctx ") + contextStr + sep +
		tuiStatusBar.Render("last ") + lastCostStr +
		tuiStatusBar.Render(" total ") + totalCostStr + sep +
		pwdStr

	if gitStr != "" {
		left += sep + gitStr
	}

	left += tuiStatusBar.Render(" ")

	// Pad to full width
	// We need the visual width, not byte length
	leftWidth := lipgloss.Width(left)
	if leftWidth < m.width {
		left += tuiStatusBar.Render(strings.Repeat(" ", m.width-leftWidth))
	}

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
