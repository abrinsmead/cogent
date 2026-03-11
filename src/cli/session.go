package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

	provider api.Provider // per-session provider (each session can use a different model)
	agent    *agent.Agent
	output   viewport.Model
	input    textarea.Model
	slines   []line   // structured lines (persisted)
	rlines   []string // rendered lines (for viewport display)
	state    tuiState
	confirm  *tuiConfirmMsg // active tool confirmation (reply channel for agent)
	clarify  *tuiClarifyMsg // active clarifying question (reply channel for agent)
	prompt   *promptModel   // active user prompt (confirm, plan confirm, or choice)

	cancelFn     context.CancelFunc
	inputHeight  int
	modeTagWidth int // visual width of " Mode " prefix in prompt box
	scrollback   bool
	pastedText  string // stored paste content when input shows collapsed label
	pasteLabel  string // the "[Pasted N lines]" label inserted into the input

	// Task browser modal
	taskModal *taskModal

	// Input history (up/down arrow cycling, like a shell)
	inputHistory []string // previous commands, oldest first
	historyIndex int      // -1 = not browsing; 0..len-1 = current position
	historySaved string   // text that was in the input before browsing started

	// Ghost-text suggestions (LLM-powered, opt-in)
	suggestion    string           // current ghost text suggestion (empty = none)
	suggestEngine *suggestionEngine // nil if feature is disabled

	// Status bar stats (per-session)
	contextUsed int
	totalCost   float64
}

// inputPlaceholder returns the textarea placeholder text for the given permission mode.
func inputPlaceholder(mode agent.PermissionMode) string {
	if mode == agent.ModeTerminal {
		return "Run a command or press shift+tab to change permission mode"
	}
	return "Type a message or press shift+tab to change permission mode"
}

// newSession creates a session with its own agent, textarea, and viewport.
// The unified msgCh is shared across all sessions — callbacks tag messages
// with the session ID via sessionMsg.
func newSession(id int, provider api.Provider, cwd string, msgCh chan tea.Msg) *session {
	ta := textarea.New()
	ta.Placeholder = inputPlaceholder(agent.ModeConfirm)
	ta.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return "❯ "
		}
		return "  "
	})
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter"), key.WithHelp("shift+enter", "insert newline"))
	styles := ta.Styles()
	styles.Focused.CursorLine = lipgloss.NewStyle()
	styles.Blurred.CursorLine = lipgloss.NewStyle()
	ta.SetStyles(styles)
	ta.Focus()

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
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
		id:           id,
		persistID:    generatePersistID(),
		name:         "Default",
		createdAt:    time.Now(),
		provider:     provider,
		input:        ta,
		output:       vp,
		state:        tuiStateInput,
		inputHeight:  1,
		historyIndex: -1,
	}
	if id > 0 {
		s.name = fmt.Sprintf("Session %d", id+1)
	}

	s.agent = agent.New(provider, cwd,
		agent.WithTextCallback(func(text string) {
			msgCh <- sessionMsg{sessionID: id, inner: tuiAppendLineMsg{line: line{Type: lineText, Data: text}}}
		}),
		agent.WithToolCallback(func(name, summary string, input map[string]any) {
			if name == "edit" || name == "write" {
				inputJSON, _ := json.Marshal(input)
				msgCh <- sessionMsg{sessionID: id, inner: tuiAppendLineMsg{line: line{Type: lineDiff, Data: name + "\x00" + string(inputJSON)}}}
			}
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

	s.updateModeTagWidth()

	// Session start marker with timestamp
	ts := time.Now().UTC().Format("Jan 2, 2006 15:04 UTC")
	s.slines = []line{{Type: lineSessionStart, Data: "new\x00" + ts}}
	s.rlines = []string{renderLine(s.slines[0])}

	// Show active custom tools at startup
	if entries := s.agent.Registry().CustomToolInfo(); len(entries) > 0 {
		s.appendLine(line{Type: lineToolsLoaded, Data: formatToolEntries(entries)})
	}

	return s
}

// pushHistory adds an entry to the input history and resets browsing state.
func (s *session) pushHistory(text string) {
	if text == "" {
		return
	}
	// Avoid consecutive duplicates
	if len(s.inputHistory) > 0 && s.inputHistory[len(s.inputHistory)-1] == text {
		s.historyIndex = -1
		s.historySaved = ""
		return
	}
	s.inputHistory = append(s.inputHistory, text)
	s.historyIndex = -1
	s.historySaved = ""
}

// historyUp moves to the previous (older) history entry.
// Returns true if the input should be updated.
func (s *session) historyUp() (string, bool) {
	if len(s.inputHistory) == 0 {
		return "", false
	}
	if s.historyIndex == -1 {
		// Start browsing — save the current input
		s.historySaved = s.input.Value()
		s.historyIndex = len(s.inputHistory) - 1
	} else if s.historyIndex > 0 {
		s.historyIndex--
	} else {
		// Already at the oldest entry
		return "", false
	}
	return s.inputHistory[s.historyIndex], true
}

// historyDown moves to the next (newer) history entry.
// Returns true if the input should be updated.
func (s *session) historyDown() (string, bool) {
	if s.historyIndex == -1 {
		return "", false
	}
	if s.historyIndex < len(s.inputHistory)-1 {
		s.historyIndex++
		return s.inputHistory[s.historyIndex], true
	}
	// Past the newest entry — restore the saved text
	s.historyIndex = -1
	saved := s.historySaved
	s.historySaved = ""
	return saved, true
}

// rebuildHistory reconstructs the input history from structured lines.
// Called after restoring a session from disk.
func (s *session) rebuildHistory() {
	s.inputHistory = nil
	for _, l := range s.slines {
		if l.Type == linePrompt || l.Type == lineShellPrompt {
			text := strings.TrimSpace(l.Data)
			if text != "" {
				// Avoid consecutive duplicates
				if len(s.inputHistory) == 0 || s.inputHistory[len(s.inputHistory)-1] != text {
					s.inputHistory = append(s.inputHistory, text)
				}
			}
		}
	}
	s.historyIndex = -1
	s.historySaved = ""
}

// clearSuggestion removes the current ghost-text suggestion and cancels any
// in-flight suggestion request.
func (s *session) clearSuggestion() {
	s.suggestion = ""
	if s.suggestEngine != nil {
		s.suggestEngine.cancel()
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

// appendLine adds a structured line to the session's output.
func (s *session) appendLine(l line) {
	s.slines = append(s.slines, l)
	s.rlines = append(s.rlines, renderLine(l))
	s.refreshContent()
}

// updatePromptLine re-renders the last line in the viewport. Used when the
// user navigates choices in a promptChoice so the highlighting updates live.
func (s *session) updatePromptLine() {
	if s.prompt == nil || len(s.slines) == 0 {
		return
	}
	last := len(s.rlines) - 1
	if last < 0 {
		return
	}
	s.rlines[last] = s.prompt.renderPromptLine()
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
	w := s.output.Width()
	if w < 1 {
		w = 80
	}
	var wrapped []string
	for i, rl := range s.rlines {
		// Add a leading blank line before messages and confirmations
		// to separate them from compact tool call clusters.
		// Skip when the previous line is the same type (e.g. consecutive
		// text blocks from web search) — the trailing blank already provides spacing.
		if i < len(s.slines) {
			t := s.slines[i].Type
			if t == lineText || t == linePrompt || t == lineShellPrompt || t == lineDiff || t == lineConfirmPrompt || t == linePlanConfirm || t == lineChoice {
				prevSameType := i > 0 && i-1 < len(s.slines) && s.slines[i-1].Type == t
				if !prevSameType {
					wrapped = append(wrapped, "")
				}
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
	// The textarea width is already reduced by modeTagWidth in resize(),
	// so Width() reflects the actual text area. Subtract the prompt (2 chars).
	wrapWidth := s.input.Width() - 2
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
// The done message is sent through msgCh (not returned directly) to guarantee
// FIFO ordering with all other callback messages (text, tool, confirm, etc.)
// that are also sent through the channel during SendCtx.
func (s *session) sendToAgent(prompt string, msgCh chan tea.Msg) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelFn = cancel
	id := s.id
	return func() tea.Msg {
		err := s.agent.SendCtx(ctx, prompt)
		if err != nil && ctx.Err() != nil {
			msgCh <- sessionMsg{sessionID: id, inner: tuiDoneMsg{err: nil}}
		} else {
			msgCh <- sessionMsg{sessionID: id, inner: tuiDoneMsg{err: err}}
		}
		return nil
	}
}

// runShellCommand executes a shell command and sends output via msgCh.
// Like sendToAgent, the done message is routed through msgCh to preserve
// ordering with the shell output lines sent during execution.
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
				msgCh <- sessionMsg{sessionID: id, inner: tuiShellDoneMsg{err: nil, command: command, output: fullOutput}}
				return nil
			}
			msgCh <- sessionMsg{sessionID: id, inner: tuiShellDoneMsg{err: err, command: command, output: output}}
			return nil
		}
		msgCh <- sessionMsg{sessionID: id, inner: tuiShellDoneMsg{err: nil, command: command, output: output}}
		return nil
	}
}

// resize adjusts the session's viewport and input to the given dimensions.
func (s *session) resize(width, height, chrome int) {
	s.output.SetWidth(width)
	vpHeight := height - chrome
	if vpHeight < 1 {
		vpHeight = 1
	}
	s.output.SetHeight(vpHeight)
	s.input.SetWidth(width - 2 - s.modeTagWidth)
}

// updateModeTagWidth recomputes the visual width of the mode prefix
// (" Plan ", " Confirm ", etc.) and adjusts the textarea width accordingly.
func (s *session) updateModeTagWidth() {
	s.modeTagWidth = lipgloss.Width(" " + s.renderModeBar() + " ")
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
func (s *session) renderStatusBar(cwd string) string {
	sep := tuiStatusBar.Render("  |  ")

	info := s.provider.Info()
	model := tuiStatusKey.Render(info.ProviderID + "/" + info.Model)

	contextTotal := info.ContextWindow
	contextStr := tuiStatusValue.Render(fmt.Sprintf("%s/%s",
		formatTokens(s.contextUsed), formatTokens(contextTotal)))

	totalCostStr := tuiStatusValue.Render(formatCost(s.totalCost))

	pwd := shortenPath(cwd)
	pwdStr := tuiStatusValue.Render(pwd)

	gitStr, gitDiff := cachedGitStatus(cwd)

	left := tuiStatusBar.Render(" ") +
		tuiStatusBar.Render("mod ") + model + sep +
		tuiStatusBar.Render("ctx ") + contextStr + sep +
		tuiStatusBar.Render("usd ") + totalCostStr + sep +
		tuiStatusBar.Render("pwd ") + pwdStr

	if gitStr != "" {
		left += sep + gitStr
		if gitDiff != "" {
			left += " " + gitDiff
		}
	}

	left += tuiStatusBar.Render(" ")

	return left
}

// renderHUD builds the floating HUD lines for the top-right overlay.
// Returns styled lines (without the border) — the caller wraps them in the overlay.
func (s *session) renderHUD(cwd string) []string {
	dim := tuiDim
	key := tuiStatusKey
	val := tuiStatusValue

	info := s.provider.Info()
	model := key.Render(info.ProviderID + "/" + info.Model)
	contextTotal := info.ContextWindow
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

	gitStr, gitDiff := cachedGitStatus(cwd)
	if gitStr != "" {
		lines = append(lines, gitStr)
		if gitDiff != "" {
			lines = append(lines, tuiDim.Render("dif ")+gitDiff)
		}
	}

	return lines
}
