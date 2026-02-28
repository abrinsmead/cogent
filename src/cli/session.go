package cli

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"

	"github.com/anthropics/agent/agent"
	"github.com/anthropics/agent/api"
)

// session holds all per-conversation state. Each tab in the TUI is one session.
type session struct {
	id        int
	persistID string // unique persistent identifier for disk storage
	name      string
	nameSet   bool // true if user explicitly renamed via /rename
	createdAt time.Time

	agent   *agent.Agent
	output  viewport.Model
	input   textarea.Model
	slines  []line   // structured lines (persisted)
	rlines  []string // rendered lines (for viewport display)
	state   tuiState
	confirm *tuiConfirmMsg

	cancelFn    context.CancelFunc
	inputHeight int
	scrollback  bool

	// Task browser modal
	taskModal *taskModal

	// Status bar stats (per-session)
	contextUsed int
	totalCost   float64
}

// newSession creates a session with its own agent, textarea, and viewport.
// The unified msgCh is shared across all sessions — callbacks tag messages
// with the session ID via sessionMsg.
func newSession(id int, client *api.Client, cwd string, msgCh chan tea.Msg) *session {
	ta := textarea.New()
	ta.Placeholder = "Ask a question or press Shift+Tab to change modes"
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
		persistID:   generatePersistID(),
		name:        "Default",
		createdAt:   time.Now(),
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
			msgCh <- sessionMsg{sessionID: id, inner: tuiAppendLineMsg{line: line{Type: lineText, Data: text}}}
		}),
		agent.WithToolCallback(func(name, summary string) {
			msgCh <- sessionMsg{sessionID: id, inner: tuiAppendLineMsg{line: line{Type: lineTool, Data: name + "\x00" + summary}}}
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

	s.slines = []line{{}}
	s.rlines = []string{""}

	// Show active custom tools at startup
	if names := s.agent.Registry().CustomToolNames(); len(names) > 0 {
		s.appendLine(line{Type: lineToolsLoaded, Data: strings.Join(names, ", ")})
	}

	return s
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

// appendLine adds a structured line to the session's output.
func (s *session) appendLine(l line) {
	s.slines = append(s.slines, l)
	s.rlines = append(s.rlines, renderLine(l))
	s.refreshContent()
}

// rebuildRendered re-renders all structured lines into the rendered cache.
// Called after restoring a session from disk.
func (s *session) rebuildRendered() {
	s.rlines = make([]string, len(s.slines))
	for i, l := range s.slines {
		s.rlines[i] = renderLine(l)
	}
}

// refreshContent re-wraps all rendered lines and updates the viewport.
// Every non-empty line type gets a trailing blank line for visual spacing.
// Lines prefixed with noWrapMarker (from tables, etc.) are truncated
// instead of soft-wrapped so box-drawing structure is preserved.
func (s *session) refreshContent() {
	w := s.output.Width
	if w < 1 {
		w = 80
	}
	var wrapped []string
	for i, rl := range s.rlines {
		// Add a leading blank line before messages and confirmations
		// to separate them from compact tool call clusters.
		if i < len(s.slines) {
			t := s.slines[i].Type
			if t == lineText || t == linePrompt || t == lineShellPrompt || t == lineDiff || t == lineConfirmPrompt || t == linePlanConfirm {
				wrapped = append(wrapped, "")
			}
		}
		wrapped = append(wrapped, wrapLine(rl, w))
		// Add a blank line after most line types for spacing.
		// Tool calls and shell output lines are compact — no trailing blank.
		if i < len(s.slines) {
			t := s.slines[i].Type
			if t != lineEmpty && t != lineTool && t != lineShellOutput && t != lineShellError {
				wrapped = append(wrapped, "")
			}
		}
	}
	s.output.SetContent(strings.Join(wrapped, "\n"))
	if !s.scrollback {
		s.output.GotoBottom()
	}
}

// wrapLine soft-wraps a rendered line, but truncates (instead of wrapping)
// any sub-lines marked with noWrapMarker so tables and other structured
// output keep their alignment.
func wrapLine(rl string, w int) string {
	// Fast path: no marker present — wrap the whole thing.
	if !strings.Contains(rl, noWrapMarker) {
		return ansi.Wrap(rl, w, "")
	}
	// Process sub-lines individually.
	subLines := strings.Split(rl, "\n")
	for i, sl := range subLines {
		if strings.HasPrefix(sl, noWrapMarker) {
			sl = strings.TrimPrefix(sl, noWrapMarker)
			subLines[i] = ansi.Truncate(sl, w, "")
		} else {
			subLines[i] = ansi.Wrap(sl, w, "")
		}
	}
	return strings.Join(subLines, "\n")
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
			for _, ln := range strings.Split(output, "\n") {
				msgCh <- sessionMsg{sessionID: id, inner: tuiAppendLineMsg{line: line{Type: lineShellOutput, Data: ln}}}
			}
		}
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitMsg := fmt.Sprintf("(exit code %d)", exitErr.ExitCode())
				msgCh <- sessionMsg{sessionID: id, inner: tuiAppendLineMsg{line: line{Type: lineShellError, Data: exitMsg}}}
				fullOutput := output
				if fullOutput != "" {
					fullOutput += "\n"
				}
				fullOutput += exitMsg
				return sessionMsg{sessionID: id, inner: tuiShellDoneMsg{err: nil, command: command, output: fullOutput}}
			}
			return sessionMsg{sessionID: id, inner: tuiShellDoneMsg{err: err, command: command, output: output}}
		}
		return sessionMsg{sessionID: id, inner: tuiShellDoneMsg{err: nil, command: command, output: output}}
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

// renderModeBar returns the styled mode label for the mode bar section.
func (s *session) renderModeBar() string {
	mode := s.agent.GetPermissionMode()
	var style lipgloss.Style
	switch mode {
	case agent.ModePlan:
		style = tuiModePlan
	case agent.ModeYOLO:
		style = tuiModeYOLO
	case agent.ModeTerminal:
		style = tuiModeTerminal
	default:
		style = tuiModeConfirm
	}
	return style.Render(mode.String())
}

// renderStatusBar builds the status bar content for this session.
func (s *session) renderStatusBar(client *api.Client, cwd string) string {
	sep := tuiStatusBar.Render("  |  ")

	model := tuiStatusKey.Render(client.Model())

	contextTotal := client.ContextWindow()
	contextStr := tuiStatusValue.Render(fmt.Sprintf("%s/%s",
		formatTokens(s.contextUsed), formatTokens(contextTotal)))

	totalCostStr := tuiStatusValue.Render(formatCost(s.totalCost))

	pwd := shortenPath(cwd)
	pwdStr := tuiStatusValue.Render(pwd)

	gitStr := renderGitStatus(cwd)

	left := tuiStatusBar.Render(" ") +
		tuiStatusBar.Render("mod ") + model + sep +
		tuiStatusBar.Render("ctx ") + contextStr + sep +
		tuiStatusBar.Render("usd ") + totalCostStr + sep +
		tuiStatusBar.Render("pwd ") + pwdStr

	if gitStr != "" {
		left += sep + gitStr
		if stat := gitDiffStat(cwd); stat != "" {
			left += " " + stat
		}
	}

	left += tuiStatusBar.Render(" ")

	return left
}

// renderHUD builds the floating HUD lines for the top-right overlay.
// Returns styled lines (without the border) — the caller wraps them in the overlay.
func (s *session) renderHUD(client *api.Client, cwd string) []string {
	dim := tuiDim
	key := tuiStatusKey
	val := tuiStatusValue

	model := key.Render(client.Model())
	contextTotal := client.ContextWindow()
	contextPct := 0
	if contextTotal > 0 {
		contextPct = s.contextUsed * 100 / contextTotal
	}

	// Context stats: used/total (pct%)
	var ctxColor lipgloss.Style
	switch {
	case contextPct >= 80:
		ctxColor = tuiRed
	case contextPct >= 50:
		ctxColor = tuiYellow
	default:
		ctxColor = tuiGreen
	}
	contextLine := dim.Render("ctx ") + ctxColor.Render(fmt.Sprintf("%s/%s (%d%%)",
		formatTokens(s.contextUsed), formatTokens(contextTotal), contextPct))

	costLine := dim.Render("usd ") + val.Render(formatCost(s.totalCost))
	modelLine := dim.Render("mod ") + model

	pwd := shortenPath(cwd)
	pwdLine := dim.Render("pwd ") + val.Render(pwd)

	lines := []string{modelLine, contextLine, costLine, pwdLine}

	gitStr := renderGitStatus(cwd)
	if gitStr != "" {
		lines = append(lines, gitStr)
		if stat := gitDiffStat(cwd); stat != "" {
			lines = append(lines, tuiDim.Render("    dif ")+stat)
		}
	}

	return lines
}
