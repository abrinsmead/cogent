package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ─── Linear ticket browser modal ─────────────────────────────────────────────

// linearView represents which list of tickets is displayed.
type linearView int

const (
	linearViewMyIssues linearView = iota
	linearViewProject
)

// linearTicket holds fake ticket data for the browser.
type linearTicket struct {
	id          string
	title       string
	status      string
	priority    string
	assignee    string
	project     string
	description string
	labels      []string
}

// linearProject holds a project with a key and name.
type linearProject struct {
	key  string
	name string
}

// linearModal holds all state for the Linear ticket browser.
type linearModal struct {
	view     linearView
	tickets  []linearTicket
	projects []linearProject

	// Navigation
	cursor     int  // selected ticket index
	projCursor int  // selected project index
	inProject  bool // true when inside a project's ticket list

	// Detail pane
	showDetail bool // true when viewing a single ticket

	// Layout
	width  int
	height int
}

// ─── Fake data ───────────────────────────────────────────────────────────────

var fakeProjects = []linearProject{
	{key: "COG", name: "Cogent"},
	{key: "API", name: "API Platform"},
	{key: "INF", name: "Infrastructure"},
	{key: "DES", name: "Design System"},
}

var fakeTickets = []linearTicket{
	{
		id: "COG-42", title: "Add persistent shell for terminal mode",
		status: "In Progress", priority: "High", assignee: "Alex",
		project: "COG", labels: []string{"enhancement", "terminal"},
		description: "Terminal mode spawns a new subprocess for each command, so `cd` and env vars don't persist. Investigate using a persistent shell process with stdin/stdout pipes.",
	},
	{
		id: "COG-41", title: "Context compaction causes thinking blocks to be lost",
		status: "Todo", priority: "High", assignee: "Alex",
		project: "COG", labels: []string{"bug"},
		description: "When server-side compaction triggers, any thinking blocks from earlier in the conversation are dropped. The agent loses its chain of thought.",
	},
	{
		id: "COG-40", title: "Add /linear command for ticket browsing",
		status: "In Progress", priority: "Medium", assignee: "Alex",
		project: "COG", labels: []string{"enhancement", "tui"},
		description: "Add a modal ticket browser to the TUI that lets users browse Linear tickets and insert them into the prompt for context.",
	},
	{
		id: "COG-39", title: "Custom tool timeout should be configurable",
		status: "Backlog", priority: "Low", assignee: "Alex",
		project: "COG", labels: []string{"enhancement"},
		description: "Custom tools have a hardcoded 120s timeout. Allow @timeout directive in tool scripts.",
	},
	{
		id: "COG-38", title: "Glob results should show file sizes",
		status: "Done", priority: "Low", assignee: "Sam",
		project: "COG", labels: []string{"enhancement"},
		description: "The glob tool returns filenames sorted by modification time but doesn't show sizes. Add size column.",
	},
	{
		id: "API-15", title: "Rate limiter returns 503 instead of 429",
		status: "In Progress", priority: "Urgent", assignee: "Jordan",
		project: "API", labels: []string{"bug", "production"},
		description: "The rate limiter middleware is returning 503 Service Unavailable instead of 429 Too Many Requests. Clients that retry on 429 don't retry on 503.",
	},
	{
		id: "API-14", title: "Add request ID to all log lines",
		status: "Todo", priority: "Medium", assignee: "Sam",
		project: "API", labels: []string{"observability"},
		description: "Request IDs are generated but not consistently included in structured log output. Thread the request ID through the context.",
	},
	{
		id: "INF-22", title: "Terraform state lock timeout too aggressive",
		status: "In Progress", priority: "High", assignee: "Jordan",
		project: "INF", labels: []string{"bug", "terraform"},
		description: "The state lock timeout is set to 30s which causes failures on large plan operations. Increase to 5m.",
	},
	{
		id: "INF-21", title: "Migrate CI from CircleCI to GitHub Actions",
		status: "Backlog", priority: "Medium", assignee: "Alex",
		project: "INF", labels: []string{"migration"},
		description: "CircleCI costs are increasing. Migrate all pipelines to GitHub Actions. Need to handle secrets rotation and caching.",
	},
	{
		id: "DES-8", title: "Button component focus ring inconsistent",
		status: "Todo", priority: "Medium", assignee: "Sam",
		project: "DES", labels: []string{"bug", "a11y"},
		description: "The focus ring on the primary button variant doesn't match the design spec. It uses a 2px solid border instead of the 2px offset outline.",
	},
	{
		id: "DES-7", title: "Add dark mode tokens to color system",
		status: "In Progress", priority: "High", assignee: "Sam",
		project: "DES", labels: []string{"enhancement", "tokens"},
		description: "The design token system only supports light mode. Add semantic dark mode tokens and a theme switching mechanism.",
	},
}

// ─── Styles ──────────────────────────────────────────────────────────────────

var (
	linearTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("5")) // magenta

	linearHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15"))

	linearSelected = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("15"))

	linearDim = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	linearStatusInProgress = lipgloss.NewStyle().
				Foreground(lipgloss.Color("3")) // yellow

	linearStatusTodo = lipgloss.NewStyle().
				Foreground(lipgloss.Color("7")) // white

	linearStatusDone = lipgloss.NewStyle().
				Foreground(lipgloss.Color("2")) // green

	linearStatusBacklog = lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")) // dim

	linearPriorityUrgent = lipgloss.NewStyle().
				Foreground(lipgloss.Color("1")).
				Bold(true)

	linearPriorityHigh = lipgloss.NewStyle().
				Foreground(lipgloss.Color("208")) // orange

	linearPriorityMedium = lipgloss.NewStyle().
				Foreground(lipgloss.Color("3")) // yellow

	linearPriorityLow = lipgloss.NewStyle().
				Foreground(lipgloss.Color("8"))

	linearLabel = lipgloss.NewStyle().
			Foreground(lipgloss.Color("4"))

	linearKey = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	linearAccent = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("5"))

	linearBorder = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	linearDetailKey = lipgloss.NewStyle().
			Foreground(lipgloss.Color("5")).
			Bold(true)

	linearDetailVal = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7"))
)

// ─── Constructor ─────────────────────────────────────────────────────────────

func newLinearModal(width, height int) *linearModal {
	return &linearModal{
		view:     linearViewMyIssues,
		tickets:  fakeTickets,
		projects: fakeProjects,
		width:    width,
		height:   height,
	}
}

// ─── Navigation ──────────────────────────────────────────────────────────────

// currentTickets returns the tickets for the current view.
func (lm *linearModal) currentTickets() []linearTicket {
	switch {
	case lm.view == linearViewMyIssues:
		// "My Issues" = tickets assigned to Alex
		var out []linearTicket
		for _, t := range lm.tickets {
			if t.assignee == "Alex" {
				out = append(out, t)
			}
		}
		return out
	case lm.view == linearViewProject && lm.inProject:
		proj := lm.projects[lm.projCursor]
		var out []linearTicket
		for _, t := range lm.tickets {
			if t.project == proj.key {
				out = append(out, t)
			}
		}
		return out
	default:
		return nil
	}
}

// selectedTicket returns the currently highlighted ticket, or nil.
func (lm *linearModal) selectedTicket() *linearTicket {
	tickets := lm.currentTickets()
	if lm.cursor >= 0 && lm.cursor < len(tickets) {
		t := tickets[lm.cursor]
		return &t
	}
	return nil
}

func (lm *linearModal) up() {
	if lm.showDetail {
		return
	}
	if lm.view == linearViewProject && !lm.inProject {
		if lm.projCursor > 0 {
			lm.projCursor--
		}
		return
	}
	if lm.cursor > 0 {
		lm.cursor--
	}
}

func (lm *linearModal) down() {
	if lm.showDetail {
		return
	}
	if lm.view == linearViewProject && !lm.inProject {
		if lm.projCursor < len(lm.projects)-1 {
			lm.projCursor++
		}
		return
	}
	tickets := lm.currentTickets()
	if lm.cursor < len(tickets)-1 {
		lm.cursor++
	}
}

func (lm *linearModal) enter() {
	if lm.showDetail {
		return
	}
	if lm.view == linearViewProject && !lm.inProject {
		lm.inProject = true
		lm.cursor = 0
		return
	}
	// Open detail view
	if lm.selectedTicket() != nil {
		lm.showDetail = true
	}
}

func (lm *linearModal) back() {
	if lm.showDetail {
		lm.showDetail = false
		return
	}
	if lm.view == linearViewProject && lm.inProject {
		lm.inProject = false
		lm.cursor = 0
		return
	}
}

func (lm *linearModal) switchView() {
	if lm.showDetail {
		return
	}
	if lm.view == linearViewMyIssues {
		lm.view = linearViewProject
	} else {
		lm.view = linearViewMyIssues
	}
	lm.cursor = 0
	lm.inProject = false
}

// ─── Render ──────────────────────────────────────────────────────────────────

func (lm *linearModal) render() string {
	if lm.showDetail {
		return lm.renderDetail()
	}
	return lm.renderList()
}

func (lm *linearModal) renderList() string {
	innerWidth := lm.width - 4
	if innerWidth < 20 {
		innerWidth = 20
	}
	maxHeight := lm.height - 2
	if maxHeight < 10 {
		maxHeight = 10
	}

	var lines []string

	// Title bar
	titleText := " ◆ Linear "
	lines = append(lines, linearTitle.Render(titleText))
	lines = append(lines, "")

	// View tabs
	myTab := "  My Issues  "
	projTab := "  Projects  "
	if lm.view == linearViewMyIssues {
		myTab = linearSelected.Render(myTab)
		projTab = linearDim.Render(projTab)
	} else {
		myTab = linearDim.Render(myTab)
		projTab = linearSelected.Render(projTab)
	}
	tabLine := myTab + "  " + projTab + "    " + linearKey.Render("tab: switch view")
	lines = append(lines, tabLine)
	lines = append(lines, linearBorder.Render(strings.Repeat("─", innerWidth)))

	// Content
	switch {
	case lm.view == linearViewProject && !lm.inProject:
		lines = append(lines, lm.renderProjectList(innerWidth)...)
	default:
		lines = append(lines, lm.renderTicketList(innerWidth)...)
	}

	// Bottom help
	lines = append(lines, "")
	if lm.view == linearViewProject && !lm.inProject {
		lines = append(lines, linearKey.Render("  ↑/↓: navigate  enter: open  tab: switch view  esc: close"))
	} else {
		lines = append(lines, linearKey.Render("  ↑/↓: navigate  enter: view details  tab: switch view  esc: back/close"))
	}

	// Truncate or pad to exact height
	if len(lines) > maxHeight {
		lines = lines[:maxHeight]
	}
	for len(lines) < maxHeight {
		lines = append(lines, "")
	}

	// Build the box
	return lm.wrapInBox(lines, innerWidth)
}

func (lm *linearModal) renderProjectList(innerWidth int) []string {
	var lines []string
	for i, p := range lm.projects {
		// Count tickets for this project
		count := 0
		for _, t := range lm.tickets {
			if t.project == p.key {
				count++
			}
		}
		entry := fmt.Sprintf("  %s  %s  %s", p.key, p.name, linearDim.Render(fmt.Sprintf("(%d issues)", count)))
		if i == lm.projCursor {
			// Highlight the whole line
			entry = fmt.Sprintf("▸ %s  %s  %s", p.key, p.name, fmt.Sprintf("(%d issues)", count))
			entry = linearSelected.Render(truncLine(entry, innerWidth))
		}
		lines = append(lines, entry)
	}
	return lines
}

func (lm *linearModal) renderTicketList(innerWidth int) []string {
	tickets := lm.currentTickets()
	if len(tickets) == 0 {
		return []string{linearDim.Render("  No issues found.")}
	}

	// Header with breadcrumb for project view
	var lines []string
	if lm.view == linearViewProject && lm.inProject {
		proj := lm.projects[lm.projCursor]
		breadcrumb := linearDim.Render("Projects › ") + linearHeader.Render(proj.name) + linearDim.Render(fmt.Sprintf("  (%d issues)", len(tickets)))
		lines = append(lines, "  "+breadcrumb)
		lines = append(lines, "")
	}

	// Ticket rows
	for i, t := range tickets {
		statusIcon := statusIcon(t.status)
		priIcon := priorityIcon(t.priority)

		id := linearDim.Render(t.id)
		title := t.title
		maxTitle := innerWidth - 25
		if maxTitle > 0 && len(title) > maxTitle {
			title = title[:maxTitle-3] + "…"
		}

		row := fmt.Sprintf("  %s %s  %s  %s", statusIcon, priIcon, id, title)
		if i == lm.cursor {
			plain := fmt.Sprintf("▸ %s %s  %s  %s", statusIconPlain(t.status), priorityIconPlain(t.priority), t.id, title)
			row = linearSelected.Render(truncLine(plain, innerWidth))
		}
		lines = append(lines, row)
	}
	return lines
}

func (lm *linearModal) renderDetail() string {
	t := lm.selectedTicket()
	if t == nil {
		return ""
	}

	innerWidth := lm.width - 4
	if innerWidth < 20 {
		innerWidth = 20
	}

	var lines []string

	// Title bar
	lines = append(lines, linearTitle.Render(" ◆ Linear "))
	lines = append(lines, "")

	// Ticket header
	lines = append(lines, "  "+linearAccent.Render(t.id)+"  "+linearHeader.Render(t.title))
	lines = append(lines, "")

	// Metadata grid
	lines = append(lines, "  "+linearDetailKey.Render("Status    ")+statusStyled(t.status))
	lines = append(lines, "  "+linearDetailKey.Render("Priority  ")+priorityStyled(t.priority))
	lines = append(lines, "  "+linearDetailKey.Render("Assignee  ")+linearDetailVal.Render(t.assignee))
	lines = append(lines, "  "+linearDetailKey.Render("Project   ")+linearDetailVal.Render(t.project))
	if len(t.labels) > 0 {
		labelStr := ""
		for i, l := range t.labels {
			if i > 0 {
				labelStr += " "
			}
			labelStr += linearLabel.Render("[" + l + "]")
		}
		lines = append(lines, "  "+linearDetailKey.Render("Labels    ")+labelStr)
	}
	lines = append(lines, "")

	// Description
	lines = append(lines, "  "+linearDetailKey.Render("Description"))
	// Word-wrap description
	descWidth := innerWidth - 4
	if descWidth < 10 {
		descWidth = 10
	}
	wrapped := wordWrap(t.description, descWidth)
	for _, dl := range wrapped {
		lines = append(lines, "  "+linearDim.Render("  "+dl))
	}

	// Bottom help
	lines = append(lines, "")
	lines = append(lines,
		"  "+linearAccent.Render("[ Insert into Prompt ]")+"    "+linearKey.Render("⏎ insert  esc/backspace: back"),
	)

	// Pad to consistent height
	maxHeight := lm.height - 2
	if maxHeight < 10 {
		maxHeight = 10
	}
	if len(lines) > maxHeight {
		lines = lines[:maxHeight]
	}
	for len(lines) < maxHeight {
		lines = append(lines, "")
	}

	return lm.wrapInBox(lines, innerWidth)
}

func (lm *linearModal) wrapInBox(lines []string, innerWidth int) string {
	var b strings.Builder

	topBorder := linearBorder.Render("╭" + strings.Repeat("─", innerWidth+2) + "╮")
	botBorder := linearBorder.Render("╰" + strings.Repeat("─", innerWidth+2) + "╯")
	leftEdge := linearBorder.Render("│") + " "
	rightEdge := " " + linearBorder.Render("│")

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

// formatTicketForPrompt produces a markdown-like summary to insert into the prompt.
func formatTicketForPrompt(t *linearTicket) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[%s] %s\n", t.id, t.title))
	sb.WriteString(fmt.Sprintf("Status: %s | Priority: %s | Assignee: %s\n", t.status, t.priority, t.assignee))
	if len(t.labels) > 0 {
		sb.WriteString("Labels: " + strings.Join(t.labels, ", ") + "\n")
	}
	if t.description != "" {
		sb.WriteString("\n" + t.description)
	}
	return sb.String()
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func statusIcon(s string) string {
	switch s {
	case "In Progress":
		return linearStatusInProgress.Render("●")
	case "Todo":
		return linearStatusTodo.Render("○")
	case "Done":
		return linearStatusDone.Render("✓")
	case "Backlog":
		return linearStatusBacklog.Render("◌")
	default:
		return linearDim.Render("○")
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
		return linearStatusInProgress.Render(s)
	case "Todo":
		return linearStatusTodo.Render(s)
	case "Done":
		return linearStatusDone.Render(s)
	case "Backlog":
		return linearStatusBacklog.Render(s)
	default:
		return s
	}
}

func priorityIcon(p string) string {
	switch p {
	case "Urgent":
		return linearPriorityUrgent.Render("⚡")
	case "High":
		return linearPriorityHigh.Render("↑")
	case "Medium":
		return linearPriorityMedium.Render("→")
	case "Low":
		return linearPriorityLow.Render("↓")
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
		return linearPriorityUrgent.Render(p)
	case "High":
		return linearPriorityHigh.Render(p)
	case "Medium":
		return linearPriorityMedium.Render(p)
	case "Low":
		return linearPriorityLow.Render(p)
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
