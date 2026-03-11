package cli

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ─── Task browser — provider interface and generic types ────────────────────

// TaskItem is a universal issue/ticket/task across all providers.
type TaskItem struct {
	ID          string
	Title       string
	Status      string
	Priority    string
	Assignee    string
	Description string
	URL         string
	Labels      []string
}

// TaskGroup is a container of tasks (project, repo, board, sprint, etc.).
type TaskGroup struct {
	Key    string
	Name   string
	Status string // optional — shown in the group list if non-empty
	Count  int    // 0 = unknown / don't show
}

// TaskResult is returned by TaskProvider.Fetch. Exactly one of Items or Groups
// should be non-nil — the modal renders whichever is present.
type TaskResult struct {
	Items  []TaskItem
	Groups []TaskGroup
}

// TaskProvider is the interface every task backend implements.
type TaskProvider interface {
	Name() string                                      // e.g. "Linear", "GitHub", "Jira"
	Icon() string                                      // e.g. "◆", "", ""
	Tabs() []string                                    // tab labels
	Fetch(tab int, group string) (*TaskResult, error)  // single data method
}

// detectTaskProvider returns the appropriate provider based on environment.
// For now, always returns Linear.
func detectTaskProvider() TaskProvider {
	// TODO: check GITHUB_TOKEN, JIRA_API_TOKEN, etc.
	return newLinearProvider()
}

// ─── Styles ─────────────────────────────────────────────────────────────────

var (
	taskTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("5")) // magenta

	taskHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15"))

	taskSelected = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("15"))

	taskDim = lipgloss.NewStyle().
		Foreground(lipgloss.Color("8"))

	taskStatusInProgress = lipgloss.NewStyle().
				Foreground(lipgloss.Color("3")) // yellow

	taskStatusTodo = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")) // white

	taskStatusDone = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2")) // green

	taskStatusBacklog = lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")) // dim

	taskPriorityUrgent = lipgloss.NewStyle().
				Foreground(lipgloss.Color("1")).
				Bold(true)

	taskPriorityHigh = lipgloss.NewStyle().
				Foreground(lipgloss.Color("208")) // orange

	taskPriorityMedium = lipgloss.NewStyle().
				Foreground(lipgloss.Color("3")) // yellow

	taskPriorityLow = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	taskLabel = lipgloss.NewStyle().
			Foreground(lipgloss.Color("4"))

	taskKey = lipgloss.NewStyle().
		Foreground(lipgloss.Color("8"))

	taskAccent = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("5"))

	taskBorder = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	taskDetailKey = lipgloss.NewStyle().
			Foreground(lipgloss.Color("5")).
			Bold(true)

	taskDetailVal = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7"))
)

// ─── Task modal ─────────────────────────────────────────────────────────────

// taskModal holds all state for the generic task browser overlay.
type taskModal struct {
	provider TaskProvider
	tabs     []string

	tab       int         // active tab index
	items     []TaskItem  // current items (non-nil when showing items)
	groups    []TaskGroup // current groups (non-nil when showing groups)
	groupKey  string      // non-empty when drilled into a group
	groupName string      // for breadcrumb display
	errMsg    string      // non-empty when the last fetch failed
	loading   bool        // true while an async fetch is in flight

	cursor     int
	showDetail bool
	width      int
	height     int
}

func newTaskModal(provider TaskProvider, w, h int) *taskModal {
	tm := &taskModal{
		provider: provider,
		tabs:     provider.Tabs(),
		width:    w,
		height:   h,
		loading:  true,
	}
	return tm
}

// tuiTaskFetchDoneMsg delivers async fetch results to the modal.
type tuiTaskFetchDoneMsg struct {
	result *TaskResult
	err    error
}

// fetchCmd returns a tea.Cmd that runs the provider fetch in a goroutine
// and sends the result back as a sessionMsg wrapping tuiTaskFetchDoneMsg.
func (tm *taskModal) fetchCmd(sessionID int, msgCh chan tea.Msg) tea.Cmd {
	provider := tm.provider
	tab := tm.tab
	group := tm.groupKey
	return func() tea.Msg {
		result, err := provider.Fetch(tab, group)
		return sessionMsg{sessionID: sessionID, inner: tuiTaskFetchDoneMsg{result: result, err: err}}
	}
}

// applyFetch applies an async fetch result to the modal.
func (tm *taskModal) applyFetch(result *TaskResult, err error) {
	tm.loading = false
	if err != nil {
		tm.errMsg = err.Error()
		tm.items = nil
		tm.groups = nil
		return
	}
	tm.errMsg = ""
	tm.items = result.Items
	tm.groups = result.Groups
}

// ─── Navigation ─────────────────────────────────────────────────────────────

func (tm *taskModal) selectedItem() *TaskItem {
	if tm.cursor >= 0 && tm.cursor < len(tm.items) {
		t := tm.items[tm.cursor]
		return &t
	}
	return nil
}

func (tm *taskModal) up() {
	if tm.showDetail {
		return
	}
	if tm.groups != nil && tm.groupKey == "" {
		// Navigating the group list
		if tm.cursor > 0 {
			tm.cursor--
		}
		return
	}
	if tm.cursor > 0 {
		tm.cursor--
	}
}

func (tm *taskModal) down() {
	if tm.showDetail {
		return
	}
	if tm.groups != nil && tm.groupKey == "" {
		// Navigating the group list
		if tm.cursor < len(tm.groups)-1 {
			tm.cursor++
		}
		return
	}
	if tm.cursor < len(tm.items)-1 {
		tm.cursor++
	}
}

func (tm *taskModal) enter() bool {
	if tm.showDetail {
		return false
	}
	if tm.groups != nil && tm.groupKey == "" {
		// Drill into selected group
		if tm.cursor >= 0 && tm.cursor < len(tm.groups) {
			g := tm.groups[tm.cursor]
			tm.groupKey = g.Key
			tm.groupName = g.Name
			tm.items = nil
			tm.cursor = 0
			tm.loading = true
			return true
		}
		return false
	}
	// Open detail view
	if tm.selectedItem() != nil {
		tm.showDetail = true
	}
	return false
}

func (tm *taskModal) back() bool {
	if tm.showDetail {
		tm.showDetail = false
		return false
	}
	if tm.groupKey != "" {
		// Return to group list
		tm.groupKey = ""
		tm.groupName = ""
		tm.items = nil
		tm.cursor = 0
		tm.loading = true
		return true
	}
	return false
}

func (tm *taskModal) switchView() bool {
	if tm.showDetail {
		return false
	}
	tm.tab = (tm.tab + 1) % len(tm.tabs)
	tm.cursor = 0
	tm.groupKey = ""
	tm.groupName = ""
	tm.items = nil
	tm.groups = nil
	tm.loading = true
	return true
}

// ─── Render ─────────────────────────────────────────────────────────────────

func (tm *taskModal) render() string {
	if tm.showDetail {
		return tm.renderDetail()
	}
	return tm.renderList()
}

func (tm *taskModal) renderList() string {
	innerWidth := tm.width - 4
	if innerWidth < 20 {
		innerWidth = 20
	}
	maxHeight := tm.height - 2
	if maxHeight < 10 {
		maxHeight = 10
	}

	var lines []string

	// Title bar
	titleText := " " + tm.provider.Icon() + " " + tm.provider.Name() + " "
	lines = append(lines, taskTitle.Render(titleText))
	lines = append(lines, "")

	// View tabs — dynamically built from provider
	var tabParts []string
	for i, label := range tm.tabs {
		padded := "  " + label + "  "
		if i == tm.tab {
			tabParts = append(tabParts, taskSelected.Render(padded))
		} else {
			tabParts = append(tabParts, taskDim.Render(padded))
		}
	}
	tabLine := strings.Join(tabParts, "  ") + "    " + taskKey.Render("tab: switch view")
	lines = append(lines, tabLine)
	lines = append(lines, taskBorder.Render(strings.Repeat("─", innerWidth)))

	// Content
	if tm.errMsg != "" {
		errLines := wordWrap(tm.errMsg, innerWidth-4)
		lines = append(lines, "")
		for _, el := range errLines {
			lines = append(lines, "  "+taskPriorityUrgent.Render("⚠ "+el))
		}
	} else if tm.loading {
		lines = append(lines, "")
		lines = append(lines, taskDim.Render("  Loading…"))
	} else if tm.groups != nil && tm.groupKey == "" {
		lines = append(lines, tm.renderGroupList(innerWidth)...)
	} else {
		lines = append(lines, tm.renderItemList(innerWidth)...)
	}

	// Bottom help
	lines = append(lines, "")
	if tm.groups != nil && tm.groupKey == "" {
		lines = append(lines, taskKey.Render("  ↑/↓: navigate  enter: open  tab: switch view  esc: close"))
	} else if tm.groupKey != "" {
		lines = append(lines, taskKey.Render("  ↑/↓: navigate  enter: view details  tab: switch view  esc: back/close"))
	} else {
		lines = append(lines, taskKey.Render("  ↑/↓: navigate  enter: view details  tab: switch view  esc: close"))
	}

	// Truncate or pad to exact height
	if len(lines) > maxHeight {
		lines = lines[:maxHeight]
	}
	for len(lines) < maxHeight {
		lines = append(lines, "")
	}

	return tm.wrapInBox(lines, innerWidth)
}

func (tm *taskModal) renderGroupList(innerWidth int) []string {
	var lines []string
	for i, g := range tm.groups {
		countStr := ""
		if g.Count > 0 {
			countStr = taskDim.Render(fmt.Sprintf("(%d)", g.Count))
		}
		statusStr := ""
		statusPlain := ""
		if g.Status != "" {
			statusStr = statusStyled(g.Status) + "  "
			statusPlain = g.Status + "  "
		}
		entry := fmt.Sprintf("  %s%s  %s", statusStr, g.Name, countStr)
		if i == tm.cursor {
			plain := fmt.Sprintf("▸ %s%s", statusPlain, g.Name)
			if g.Count > 0 {
				plain += fmt.Sprintf("  (%d)", g.Count)
			}
			entry = taskSelected.Render(truncLine(plain, innerWidth))
		}
		lines = append(lines, entry)
	}
	return lines
}

func (tm *taskModal) renderItemList(innerWidth int) []string {
	if len(tm.items) == 0 {
		return []string{taskDim.Render("  No issues found.")}
	}

	var lines []string

	// Breadcrumb when inside a group
	if tm.groupKey != "" {
		tabLabel := ""
		if tm.tab < len(tm.tabs) {
			tabLabel = tm.tabs[tm.tab]
		}
		breadcrumb := taskDim.Render(tabLabel+" › ") +
			taskHeader.Render(tm.groupName) +
			taskDim.Render(fmt.Sprintf("  (%d issues)", len(tm.items)))
		lines = append(lines, "  "+breadcrumb)
		lines = append(lines, "")
	}

	// Item rows
	for i, t := range tm.items {
		sIcon := statusIcon(t.Status)
		pIcon := priorityIcon(t.Priority)

		id := taskDim.Render(t.ID)
		title := t.Title
		maxTitle := innerWidth - 25
		if maxTitle > 0 && len(title) > maxTitle {
			title = title[:maxTitle-3] + "…"
		}

		row := fmt.Sprintf("  %s %s  %s  %s", sIcon, pIcon, id, title)
		if i == tm.cursor {
			plain := fmt.Sprintf("▸ %s %s  %s  %s", statusIconPlain(t.Status), priorityIconPlain(t.Priority), t.ID, title)
			row = taskSelected.Render(truncLine(plain, innerWidth))
		}
		lines = append(lines, row)
	}
	return lines
}

func (tm *taskModal) renderDetail() string {
	t := tm.selectedItem()
	if t == nil {
		return ""
	}

	innerWidth := tm.width - 4
	if innerWidth < 20 {
		innerWidth = 20
	}

	var lines []string

	// Title bar
	titleText := " " + tm.provider.Icon() + " " + tm.provider.Name() + " "
	lines = append(lines, taskTitle.Render(titleText))
	lines = append(lines, "")

	// Item header
	lines = append(lines, "  "+taskAccent.Render(t.ID)+"  "+taskHeader.Render(t.Title))
	lines = append(lines, "")

	// Metadata grid
	lines = append(lines, "  "+taskDetailKey.Render("Status    ")+statusStyled(t.Status))
	lines = append(lines, "  "+taskDetailKey.Render("Priority  ")+priorityStyled(t.Priority))
	lines = append(lines, "  "+taskDetailKey.Render("Assignee  ")+taskDetailVal.Render(t.Assignee))
	if len(t.Labels) > 0 {
		labelStr := ""
		for i, l := range t.Labels {
			if i > 0 {
				labelStr += " "
			}
			labelStr += taskLabel.Render("[" + l + "]")
		}
		lines = append(lines, "  "+taskDetailKey.Render("Labels    ")+labelStr)
	}
	lines = append(lines, "")

	// Description
	lines = append(lines, "  "+taskDetailKey.Render("Description"))
	descWidth := innerWidth - 4
	if descWidth < 10 {
		descWidth = 10
	}
	wrapped := wordWrap(t.Description, descWidth)
	for _, dl := range wrapped {
		lines = append(lines, "  "+taskDim.Render("  "+dl))
	}

	// Bottom help
	lines = append(lines, "")
	lines = append(lines,
		"  "+taskAccent.Render("[ Insert into Prompt ]")+"    "+taskKey.Render("⏎ insert  esc/backspace: back"),
	)

	// Pad to consistent height
	maxHeight := tm.height - 2
	if maxHeight < 10 {
		maxHeight = 10
	}
	if len(lines) > maxHeight {
		lines = lines[:maxHeight]
	}
	for len(lines) < maxHeight {
		lines = append(lines, "")
	}

	return tm.wrapInBox(lines, innerWidth)
}

func (tm *taskModal) wrapInBox(lines []string, innerWidth int) string {
	var b strings.Builder

	topBorder := taskBorder.Render("╭" + strings.Repeat("─", innerWidth+2) + "╮")
	botBorder := taskBorder.Render("╰" + strings.Repeat("─", innerWidth+2) + "╯")
	leftEdge := taskBorder.Render("│") + " "
	rightEdge := " " + taskBorder.Render("│")

	b.WriteString(topBorder + "\n")
	for _, line := range lines {
		vis := lipgloss.Width(line)
		if vis < innerWidth {
			line += strings.Repeat(" ", innerWidth-vis)
		} else if vis > innerWidth {
			line = ansi.Truncate(line, innerWidth, "")
		}
		b.WriteString(leftEdge + line + rightEdge + "\n")
	}
	b.WriteString(botBorder)

	return b.String()
}

// ─── Format for prompt insertion ────────────────────────────────────────────

// formatTaskForPrompt produces a markdown-like summary to insert into the prompt.
func formatTaskForPrompt(t *TaskItem) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[%s] %s\n", t.ID, t.Title))
	sb.WriteString(fmt.Sprintf("Status: %s | Priority: %s | Assignee: %s\n", t.Status, t.Priority, t.Assignee))
	if len(t.Labels) > 0 {
		sb.WriteString("Labels: " + strings.Join(t.Labels, ", ") + "\n")
	}
	if t.Description != "" {
		sb.WriteString("\n" + t.Description)
	}
	return sb.String()
}

// ─── Shared helpers ─────────────────────────────────────────────────────────

func statusIcon(s string) string {
	switch s {
	case "In Progress":
		return taskStatusInProgress.Render("●")
	case "Todo":
		return taskStatusTodo.Render("○")
	case "Done":
		return taskStatusDone.Render("✓")
	case "Backlog":
		return taskStatusBacklog.Render("◌")
	default:
		return taskDim.Render("○")
	}
}

func statusIconPlain(s string) string {
	switch s {
	case "In Progress":
		return "●"
	case "Todo":
		return "○"
	case "Done":
		return "✓"
	case "Backlog":
		return "◌"
	default:
		return "○"
	}
}

func statusStyled(s string) string {
	switch s {
	case "In Progress":
		return taskStatusInProgress.Render(s)
	case "Todo":
		return taskStatusTodo.Render(s)
	case "Done":
		return taskStatusDone.Render(s)
	case "Backlog":
		return taskStatusBacklog.Render(s)
	default:
		return s
	}
}

func priorityIcon(p string) string {
	switch p {
	case "Urgent":
		return taskPriorityUrgent.Render("⚡")
	case "High":
		return taskPriorityHigh.Render("↑")
	case "Medium":
		return taskPriorityMedium.Render("→")
	case "Low":
		return taskPriorityLow.Render("↓")
	default:
		return " "
	}
}

func priorityIconPlain(p string) string {
	switch p {
	case "Urgent":
		return "⚡"
	case "High":
		return "↑"
	case "Medium":
		return "→"
	case "Low":
		return "↓"
	default:
		return " "
	}
}

func priorityStyled(p string) string {
	switch p {
	case "Urgent":
		return taskPriorityUrgent.Render(p)
	case "High":
		return taskPriorityHigh.Render(p)
	case "Medium":
		return taskPriorityMedium.Render(p)
	case "Low":
		return taskPriorityLow.Render(p)
	default:
		return p
	}
}

func truncLine(s string, maxWidth int) string {
	if len(s) > maxWidth {
		return s[:maxWidth-1] + "…"
	}
	return s
}

func wordWrap(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = w
		} else {
			line += " " + w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
