package cli

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"

	"github.com/anthropics/agent/agent"
	"github.com/anthropics/agent/api"
	"github.com/anthropics/agent/tools"
)

// session holds all per-conversation state. Each tab in the TUI is one session.
type session struct {
	id      int
	name    string
	nameSet bool // true if user explicitly renamed via /rename

	agent   *agent.Agent
	output  viewport.Model
	input   textarea.Model
	lines   []string
	state   tuiState
	confirm *tuiConfirmMsg

	cancelFn    context.CancelFunc
	inputHeight int
	scrollback  bool

	// Status bar stats (per-session)
	contextUsed int
	cacheRead   int
	cacheCreate int
	lastCost    float64
	totalCost   float64

	// Sub-agent fields
	parentID   int         // 0 = user-created
	isSubAgent bool        // true if spawned by dispatch tool
	resultCh   chan string // sub-agent sends result here when done
}

// newSession creates a session with its own agent, textarea, and viewport.
// The unified msgCh is shared across all sessions — callbacks tag messages
// with the session ID via sessionMsg.
func newSession(id int, client *api.Client, cwd string, msgCh chan tea.Msg) *session {
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

	s := &session{
		id:          id,
		name:        "Default",
		input:       ta,
		output:      vp,
		state:       tuiStateInput,
		inputHeight: 1,
	}

	if id > 0 {
		s.name = fmt.Sprintf("Session %d", id+1)
	}

	s.agent = agent.New(client, cwd,
		agent.WithTextCallback(func(text string) {
			msgCh <- sessionMsg{sessionID: id, inner: tuiAppendMsg{text: tuiDim.Render(text)}}
		}),
		agent.WithToolCallback(func(name, summary string) {
			style := tuiGreen
			switch name {
			case "bash", "write", "edit":
				style = tuiRed
			}
			line := tuiDim.Render(" "+style.Render(name)) + " " + tuiDim.Render(summary)
			msgCh <- sessionMsg{sessionID: id, inner: tuiAppendMsg{text: line}}
		}),
		agent.WithToolResultCallback(func(name, result string, isError bool) {
			if result == "" {
				return
			}
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
				msgCh <- sessionMsg{sessionID: id, inner: tuiAppendMsg{text: style.Render("  " + line)}}
			}
			if truncated {
				msgCh <- sessionMsg{sessionID: id, inner: tuiAppendMsg{text: tuiYellow.Render(fmt.Sprintf("  ... output truncated (%d lines shown)", maxLines))}}
			}
		}),
		agent.WithConfirmCallback(func(name string, input map[string]any) agent.ConfirmResult {
			reply := make(chan agent.ConfirmResult)
			msgCh <- sessionMsg{sessionID: id, inner: tuiConfirmMsg{name: name, input: input, reply: reply}}
			return <-reply
		}),
		agent.WithUsageCallback(func(usage api.Usage) {
			msgCh <- sessionMsg{sessionID: id, inner: tuiUsageMsg{usage: usage}}
		}),
		agent.WithCompactionCallback(func() {
			msgCh <- sessionMsg{sessionID: id, inner: tuiCompactionMsg{}}
		}),
	)

	// Register the dispatch tool — the spawn function is set later by the TUI
	// once the model is fully initialized. The tool checks for nil at execution.
	s.agent.Registry().RegisterTool(&tools.DispatchTool{})

	s.lines = []string{
		tuiDim.Render(fmt.Sprintf("cogent — model: %s | cwd: %s", client.Model(), cwd)),
		"",
	}

	return s
}

// setDispatchSpawn wires up the dispatch tool's spawn function for this session.
func (s *session) setDispatchSpawn(fn tools.SpawnFunc) {
	if t, err := s.agent.Registry().Get("dispatch"); err == nil {
		if dt, ok := t.(*tools.DispatchTool); ok {
			dt.Spawn = fn
		}
	}
}

// autoName sets the tab name from the first user prompt, unless manually renamed.
func (s *session) autoName(prompt string) {
	if s.nameSet {
		return
	}
	name := strings.TrimSpace(prompt)
	if idx := strings.IndexByte(name, '\n'); idx > 0 {
		name = name[:idx]
	}
	if len(name) > 24 {
		name = name[:24] + "…"
	}
	if name != "" {
		s.name = name
	}
}

// appendLine adds a line to the session's output.
func (s *session) appendLine(text string) {
	s.lines = append(s.lines, text)
	s.refreshContent()
}

// refreshContent re-wraps all lines and updates the viewport.
func (s *session) refreshContent() {
	w := s.output.Width
	if w < 1 {
		w = 80
	}
	var wrapped []string
	for _, line := range s.lines {
		wrapped = append(wrapped, ansi.Wrap(line, w, ""))
	}
	s.output.SetContent(strings.Join(wrapped, "\n"))
	if !s.scrollback {
		s.output.GotoBottom()
	}
}

// recalcInputHeight computes how many visual lines the input occupies.
func (s *session) recalcInputHeight() bool {
	needed := s.inputVisualLines()
	if needed < 1 {
		needed = 1
	}
	if needed > maxInputHeight {
		needed = maxInputHeight
	}
	if needed != s.inputHeight {
		s.inputHeight = needed
		s.input.SetHeight(needed)
		return true // changed — caller should resize
	}
	return false
}

func (s *session) inputVisualLines() int {
	value := s.input.Value()
	if value == "" {
		return 1
	}
	wrapWidth := s.input.Width()
	if wrapWidth < 1 {
		wrapWidth = 1
	}
	total := 0
	for _, line := range strings.Split(value, "\n") {
		lineWidth := uniseg.StringWidth(line)
		if lineWidth == 0 {
			total++
		} else {
			total += int(math.Ceil(float64(lineWidth) / float64(wrapWidth)))
		}
	}
	return total
}

// sendToAgent starts an agent call in a goroutine and returns the tea.Cmd.
func (s *session) sendToAgent(prompt string, msgCh chan tea.Msg) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelFn = cancel
	id := s.id
	return func() tea.Msg {
		err := s.agent.SendCtx(ctx, prompt)
		if err != nil && ctx.Err() != nil {
			return sessionMsg{sessionID: id, inner: tuiDoneMsg{err: nil}}
		}
		return sessionMsg{sessionID: id, inner: tuiDoneMsg{err: err}}
	}
}

// runShellCommand executes a shell command and sends output via msgCh.
func (s *session) runShellCommand(command, cwd string, msgCh chan tea.Msg) tea.Cmd {
	id := s.id
	return func() tea.Msg {
		cmd := exec.Command("sh", "-c", command)
		cmd.Dir = cwd
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "ANTHROPIC_API_KEY=") {
				cmd.Env = append(cmd.Env, e)
			}
		}
		out, err := cmd.CombinedOutput()
		output := strings.TrimRight(string(out), "\n")
		if output != "" {
			for _, line := range strings.Split(output, "\n") {
				msgCh <- sessionMsg{sessionID: id, inner: tuiAppendMsg{text: tuiDim.Render("  " + line)}}
			}
		}
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				msgCh <- sessionMsg{sessionID: id, inner: tuiAppendMsg{text: tuiRed.Render(fmt.Sprintf("  (exit code %d)", exitErr.ExitCode()))}}
				return sessionMsg{sessionID: id, inner: tuiShellDoneMsg{err: nil}}
			}
			return sessionMsg{sessionID: id, inner: tuiShellDoneMsg{err: err}}
		}
		return sessionMsg{sessionID: id, inner: tuiShellDoneMsg{err: nil}}
	}
}

// resize adjusts the session's viewport and input to the given dimensions.
func (s *session) resize(width, height, chrome int) {
	s.output.Width = width
	vpHeight := height - chrome
	if vpHeight < 1 {
		vpHeight = 1
	}
	s.output.Height = vpHeight
	s.input.SetWidth(width - 2)
}

// renderStatusBar builds the status bar content for this session.
func (s *session) renderStatusBar(client *api.Client, cwd string) string {
	sep := tuiStatusBar.Render("  |  ")

	model := tuiStatusKey.Render(client.Model())

	mode := s.agent.GetPermissionMode()
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

	contextTotal := client.ContextWindow()
	contextStr := tuiStatusValue.Render(fmt.Sprintf("%s/%s",
		formatTokens(s.contextUsed), formatTokens(contextTotal)))
	var cacheParts []string
	if s.cacheRead > 0 {
		cacheParts = append(cacheParts, tuiGreen.Render(fmt.Sprintf("⚡%s read", formatTokens(s.cacheRead))))
	}
	if s.cacheCreate > 0 {
		cacheParts = append(cacheParts, tuiYellow.Render(fmt.Sprintf("+%s write", formatTokens(s.cacheCreate))))
	}
	if len(cacheParts) > 0 {
		contextStr += tuiStatusBar.Render(" (") + strings.Join(cacheParts, tuiStatusBar.Render(", ")) + tuiStatusBar.Render(")")
	}

	lastCostStr := tuiStatusValue.Render(formatCost(s.lastCost))
	totalCostStr := tuiStatusValue.Render(formatCost(s.totalCost))

	pwd := shortenPath(cwd)
	pwdStr := tuiStatusValue.Render(pwd)

	gitStr := renderGitStatus(cwd)

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
