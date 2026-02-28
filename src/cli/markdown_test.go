package cli

import (
	"strings"
	"testing"
)

func TestParseTableCells(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"| a | b | c |", []string{"a", "b", "c"}},
		{"|a|b|c|", []string{"a", "b", "c"}},
		{"| hello world | foo |", []string{"hello world", "foo"}},
		{"| single |", []string{"single"}},
		{"|  spaces  |  here  |", []string{"spaces", "here"}},
	}
	for _, tt := range tests {
		got := parseTableCells(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("parseTableCells(%q) = %v (len %d), want %v (len %d)",
				tt.input, got, len(got), tt.want, len(tt.want))
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("parseTableCells(%q)[%d] = %q, want %q",
					tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestAllSeparatorCells(t *testing.T) {
	tests := []struct {
		cells []string
		want  bool
	}{
		{[]string{"---", "---", "---"}, true},
		{[]string{":---", ":---:", "---:"}, true},
		{[]string{"----", ":----:", "---:"}, true},
		{[]string{"- -", "---"}, true},
		{[]string{"abc", "---"}, false},
		{[]string{"---", "hello"}, false},
		{[]string{""}, true},
	}
	for _, tt := range tests {
		got := allSeparatorCells(tt.cells)
		if got != tt.want {
			t.Errorf("allSeparatorCells(%v) = %v, want %v", tt.cells, got, tt.want)
		}
	}
}

func TestRenderTableBasic(t *testing.T) {
	rows := []string{
		"| Name | Age |",
		"|------|-----|",
		"| Alice | 30 |",
		"| Bob | 25 |",
	}
	out := renderTable(rows)
	if len(out) == 0 {
		t.Fatal("renderTable returned no output")
	}

	// Strip noWrapMarker for content assertions.
	var clean []string
	for _, l := range out {
		clean = append(clean, strings.TrimPrefix(l, noWrapMarker))
	}
	joined := strings.Join(clean, "\n")

	// Should contain box-drawing characters.
	for _, ch := range []string{"╭", "╯", "│", "├"} {
		if !strings.Contains(joined, ch) {
			t.Errorf("expected %q in output", ch)
		}
	}

	// Expected structure: top border, header row, mid border, 2 data rows, bottom border = 6 lines.
	if len(out) != 6 {
		t.Errorf("expected 6 output lines, got %d:\n%s", len(out), joined)
	}

	// Every line should carry the noWrapMarker.
	for i, l := range out {
		if !strings.HasPrefix(l, noWrapMarker) {
			t.Errorf("line %d missing noWrapMarker: %q", i, l)
		}
	}
}

func TestRenderTableNoSeparator(t *testing.T) {
	rows := []string{
		"| X | Y |",
		"| 1 | 2 |",
	}
	out := renderTable(rows)
	if len(out) == 0 {
		t.Fatal("renderTable returned no output")
	}
	// No separator → no mid border: top, header, data, bottom = 4 lines.
	if len(out) != 4 {
		var clean []string
		for _, l := range out {
			clean = append(clean, strings.TrimPrefix(l, noWrapMarker))
		}
		t.Errorf("expected 4 output lines, got %d:\n%s", len(out), strings.Join(clean, "\n"))
	}
}

func TestRenderTableSingleRow(t *testing.T) {
	rows := []string{"| just | one | row |"}
	out := renderTable(rows)
	if len(out) == 0 {
		t.Fatal("renderTable returned no output for single row")
	}
	// top + header + bottom = 3
	if len(out) != 3 {
		t.Errorf("expected 3 output lines, got %d", len(out))
	}
}

func TestRenderTableEmpty(t *testing.T) {
	out := renderTable(nil)
	if out != nil {
		t.Errorf("renderTable(nil) = %v, want nil", out)
	}
}

func TestRenderTableAlignment(t *testing.T) {
	rows := []string{
		"| Left | Center | Right |",
		"| :--- | :----: | ----: |",
		"| a | b | c |",
	}
	out := renderTable(rows)
	if len(out) == 0 {
		t.Fatal("renderTable returned no output")
	}
	// Just verify it doesn't panic and produces the right number of lines.
	// top + header + mid + data + bottom = 5
	if len(out) != 5 {
		t.Errorf("expected 5 output lines, got %d", len(out))
	}
}

func TestRenderMarkdownWithTable(t *testing.T) {
	input := "Here is a table:\n\n| Tool | Confirm |\n|------|---------|" +
		"\n| bash | Yes |\n| read | No |\n\nSome text after."

	result := renderMarkdown(input)
	// Should contain box-drawing borders from the table.
	if !strings.Contains(result, "╭") {
		t.Error("expected table top border in rendered markdown")
	}
	if !strings.Contains(result, "╰") {
		t.Error("expected table bottom border in rendered markdown")
	}
	// The surrounding text should still be present.
	if !strings.Contains(result, "table") {
		t.Error("expected surrounding text preserved")
	}
	if !strings.Contains(result, "text after") {
		t.Error("expected text after table preserved")
	}
}

func TestRenderMarkdownTableNotInCodeBlock(t *testing.T) {
	input := "```\n| a | b |\n|---|---|\n| 1 | 2 |\n```"
	result := renderMarkdown(input)
	if strings.Contains(result, "╭") {
		t.Error("table inside code block should not be rendered as a table")
	}
}

func TestWrapLineNoMarker(t *testing.T) {
	// Normal text should be wrapped.
	long := strings.Repeat("word ", 30) // ~150 chars
	result := wrapLine(long, 40)
	if !strings.Contains(result, "\n") {
		t.Error("expected long line to be wrapped")
	}
}

func TestWrapLineWithMarker(t *testing.T) {
	// A marked line should be truncated, not wrapped.
	long := noWrapMarker + strings.Repeat("x", 100)
	result := wrapLine(long, 40)
	if strings.Contains(result, "\n") {
		t.Error("marked line should be truncated, not wrapped")
	}
	if strings.Contains(result, noWrapMarker) {
		t.Error("noWrapMarker should be stripped from output")
	}
}

func TestWrapLineMixed(t *testing.T) {
	// A multi-line string with some marked and some unmarked sub-lines.
	lines := []string{
		"normal text that is quite long and should be wrapped at some point eventually",
		noWrapMarker + "│ this table row must not wrap even if it is very long and exceeds the width │",
		"more normal text",
	}
	input := strings.Join(lines, "\n")
	result := wrapLine(input, 40)

	parts := strings.Split(result, "\n")
	// The marked line should appear as a single line (truncated).
	foundTableRow := false
	for _, p := range parts {
		if strings.Contains(p, "table row") {
			foundTableRow = true
			// It should not be followed by a continuation on the next line
			// from wrapping — just verify the marker is gone.
			if strings.Contains(p, noWrapMarker) {
				t.Error("noWrapMarker should be stripped")
			}
		}
	}
	if !foundTableRow {
		t.Error("expected to find the table row content in output")
	}
	// The result should have more lines than 3, since the normal lines get wrapped.
	if len(parts) <= 3 {
		t.Errorf("expected normal lines to be wrapped, got %d total lines", len(parts))
	}
}
