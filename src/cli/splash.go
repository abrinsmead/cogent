package cli

import (
	"math"
	"math/rand"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ─── Zalgo unicode combining characters ─────────────────────────────────────

// Combining marks above (U+0300 range)
var zalgoAbove = []rune{
	0x030D, 0x030E, 0x0304, 0x0305, 0x033F, 0x0311, 0x0306, 0x0310,
	0x0352, 0x0357, 0x0351, 0x0307, 0x0308, 0x030A, 0x0342, 0x0343,
	0x0344, 0x034A, 0x034B, 0x034C, 0x0303, 0x0302, 0x030C, 0x0350,
	0x0300, 0x0301, 0x030B, 0x030F, 0x0312, 0x0313, 0x0314, 0x033D,
	0x0309, 0x0363, 0x0364, 0x0365, 0x0366, 0x0367, 0x0368, 0x0369,
	0x036A, 0x036B, 0x036C, 0x036D, 0x036E, 0x036F, 0x033E, 0x035B,
}

// Combining marks below (U+0316 range)
var zalgoBelow = []rune{
	0x0316, 0x0317, 0x0318, 0x0319, 0x031C, 0x031D, 0x031E, 0x031F,
	0x0320, 0x0324, 0x0325, 0x0326, 0x0329, 0x032A, 0x032B, 0x032C,
	0x032D, 0x032E, 0x032F, 0x0330, 0x0331, 0x0332, 0x0333, 0x0339,
	0x033A, 0x033B, 0x033C, 0x0345, 0x0347, 0x0348, 0x0349, 0x034D,
	0x034E, 0x0353, 0x0354, 0x0355, 0x0356, 0x0359, 0x035A, 0x0323,
}

// Combining marks middle (overlays)
var zalgoMiddle = []rune{
	0x0315, 0x031B, 0x0340, 0x0341, 0x0358, 0x0321, 0x0322, 0x0327,
	0x0328, 0x0334, 0x0335, 0x0336, 0x0337, 0x0338,
}

// ─── Block letter font for COGENT ───────────────────────────────────────────

// Each letter is 6 lines tall. Using a chunky block style.
var blockFont = map[rune][]string{
	'C': {
		" ██████╗ ",
		"██╔════╝ ",
		"██║      ",
		"██║      ",
		"╚██████╗ ",
		" ╚═════╝ ",
	},
	'O': {
		" ██████╗ ",
		"██╔═══██╗",
		"██║   ██║",
		"██║   ██║",
		"╚██████╔╝",
		" ╚═════╝ ",
	},
	'G': {
		" ██████╗ ",
		"██╔════╝ ",
		"██║  ███╗",
		"██║   ██║",
		"╚██████╔╝",
		" ╚═════╝ ",
	},
	'E': {
		"███████╗",
		"██╔════╝",
		"█████╗  ",
		"██╔══╝  ",
		"███████╗",
		"╚══════╝",
	},
	'N': {
		"███╗   ██╗",
		"████╗  ██║",
		"██╔██╗ ██║",
		"██║╚██╗██║",
		"██║ ╚████║",
		"╚═╝  ╚═══╝",
	},
	'T': {
		"████████╗",
		"╚══██╔══╝",
		"   ██║   ",
		"   ██║   ",
		"   ██║   ",
		"   ╚═╝   ",
	},
}

const blockHeight = 6

// ─── Zalgo text helpers ─────────────────────────────────────────────────────

// zalgoChar adds random combining marks to a single rune.
func zalgoChar(r rune, intensity int, rng *rand.Rand) string {
	if r == ' ' || r == '\n' {
		return string(r)
	}
	var b strings.Builder
	b.WriteRune(r)

	// Add combining marks above
	nAbove := rng.Intn(intensity + 1)
	for i := 0; i < nAbove; i++ {
		b.WriteRune(zalgoAbove[rng.Intn(len(zalgoAbove))])
	}

	// Add combining marks below
	nBelow := rng.Intn(intensity/2 + 1)
	for i := 0; i < nBelow; i++ {
		b.WriteRune(zalgoBelow[rng.Intn(len(zalgoBelow))])
	}

	// Add combining marks middle (fewer)
	nMiddle := rng.Intn(intensity/3 + 1)
	for i := 0; i < nMiddle; i++ {
		b.WriteRune(zalgoMiddle[rng.Intn(len(zalgoMiddle))])
	}

	return b.String()
}

// zalgoString applies zalgo to every visible rune in a string.
func zalgoString(s string, intensity int, rng *rand.Rand) string {
	var b strings.Builder
	for _, r := range s {
		b.WriteString(zalgoChar(r, intensity, rng))
	}
	return b.String()
}

// ─── Splash screen model ───────────────────────────────────────────────────

type splashTickMsg time.Time
type splashDoneMsg struct{}

type splashModel struct {
	width     int
	height    int
	rng       *rand.Rand
	startTime time.Time
	frame     int
}

func newSplashModel() splashModel {
	return splashModel{
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
		startTime: time.Now(),
	}
}

func (s splashModel) Init() tea.Cmd {
	return tea.Batch(
		splashTick(),
	)
}

func splashTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return splashTickMsg(t)
	})
}

func (s splashModel) Update(msg tea.Msg) (splashModel, tea.Cmd) {
	switch msg.(type) {
	case tea.WindowSizeMsg:
		wsm := msg.(tea.WindowSizeMsg)
		s.width = wsm.Width
		s.height = wsm.Height
		return s, nil

	case tea.KeyMsg:
		// Any key dismisses the splash
		return s, func() tea.Msg { return splashDoneMsg{} }

	case tea.MouseMsg:
		// Any click dismisses the splash
		return s, func() tea.Msg { return splashDoneMsg{} }

	case splashTickMsg:
		s.frame++
		elapsed := time.Since(s.startTime)
		if elapsed >= 2500*time.Millisecond {
			return s, func() tea.Msg { return splashDoneMsg{} }
		}
		return s, splashTick()
	}

	return s, nil
}

func (s splashModel) View() string {
	if s.width == 0 || s.height == 0 {
		return ""
	}

	// Calculate base intensity that pulses over time
	elapsed := time.Since(s.startTime).Seconds()

	// Build the block text lines for "COGENT"
	word := "COGENT"
	letters := make([][]string, len(word))
	for i, ch := range word {
		if font, ok := blockFont[ch]; ok {
			letters[i] = font
		}
	}

	// Compose each row of the block text
	var blockLines []string
	for row := 0; row < blockHeight; row++ {
		var line strings.Builder
		for _, letter := range letters {
			if row < len(letter) {
				line.WriteString(letter[row])
			}
		}
		blockLines = append(blockLines, line.String())
	}

	// Apply zalgo with varying intensity per frame
	var zalgoLines []string
	for _, line := range blockLines {
		// Vary intensity: base pulse + random per-line jitter
		intensity := int(2 + 4*((1+math.Sin(elapsed*3))/2)) // 2-6 range pulsing
		intensity += s.rng.Intn(3)                       // random jitter 0-2
		zalgoLines = append(zalgoLines, zalgoString(line, intensity, s.rng))
	}

	// Style: green/cyan cyberpunk feel
	colors := []lipgloss.Color{"2", "14", "2", "14", "2", "14"}

	var styledLines []string
	for i, line := range zalgoLines {
		color := colors[i%len(colors)]
		style := lipgloss.NewStyle().
			Foreground(color).
			Bold(true)
		styledLines = append(styledLines, style.Render(line))
	}

	// Add a subtitle
	subtitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Italic(true)

	// Animate subtitle dots
	dots := int(elapsed*2) % 4
	subtitle := "terminal agent" + strings.Repeat(".", dots) + strings.Repeat(" ", 3-dots)

	styledLines = append(styledLines, "")
	styledLines = append(styledLines, subtitleStyle.Render(subtitle))

	// Center everything
	content := strings.Join(styledLines, "\n")
	block := lipgloss.NewStyle().
		Width(s.width).
		Height(s.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(content)

	return block
}
