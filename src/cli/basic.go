package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/anthropics/agent/agent"
	"github.com/anthropics/agent/api"
)

// Basic is the original line-based REPL with raw ANSI output.
type Basic struct {
	client *api.Client
	cwd    string
	prompt string // optional single-shot prompt; empty = interactive
}

func NewBasic(client *api.Client, cwd, prompt string) *Basic {
	return &Basic{client: client, cwd: cwd, prompt: prompt}
}

func (b *Basic) Run() error {
	reader := bufio.NewReader(os.Stdin)

	ag := agent.New(b.client, b.cwd,
		agent.WithTextCallback(func(text string) {
			fmt.Printf("%s%s%s\n", Dim, text, Reset)
		}),
		agent.WithToolCallback(func(name, summary string) {
			color := Green
			switch name {
			case "bash", "write", "edit":
				color = Red
			}
			fmt.Printf("%s%s %s%s %s%s\n", Dim, color, name, Reset, Dim, summary+Reset)
		}),
		agent.WithConfirmCallback(func(name string, input map[string]any) bool {
			summary := SummarizeConfirm(name, input)
			ShowDiff(name, input)
			fmt.Printf("%s%sAllow %s %s? [Y/n]%s ", Bold, Yellow, name, summary, Reset)
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(strings.ToLower(line))
			return line == "" || line == "y" || line == "yes"
		}),
	)

	fmt.Printf("%s%s cogent %s— coding agent%s\n", Bold, Cyan, Reset+Dim, Reset)
	fmt.Printf("%smodel: %s | cwd: %s%s\n\n", Dim, b.client.Model(), b.cwd, Reset)

	// Single-shot mode
	if b.prompt != "" {
		fmt.Printf("%s> %s%s\n", Green, b.prompt, Reset)
		return ag.Send(b.prompt)
	}

	// Interactive REPL
	for {
		fmt.Printf("%s%s> %s", Bold, Green, Reset)
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}
		switch input {
		case "/quit", "/exit", "/q":
			fmt.Println("Goodbye!")
			return nil
		case "/restart", "/update":
			if err := selfUpdate(); err != nil {
				fmt.Fprintf(os.Stderr, "%sRestart failed: %s%s\n", Yellow, err, Reset)
			}
			continue
		case "/clear":
			ag.Reset()
			fmt.Println("Conversation cleared.")
			continue
		case "/help":
			fmt.Println("Commands: /help /clear /restart /quit")
			fmt.Println("Env: ANTHROPIC_API_KEY, ANTHROPIC_MODEL, ANTHROPIC_BASE_URL")
			continue
		}
		if err := ag.Send(input); err != nil {
			fmt.Fprintf(os.Stderr, "%sError: %s%s\n", Yellow, err, Reset)
		}
		fmt.Println()
	}
	return nil
}

// ─── self-update (basic mode only) ──────────────────────────────────────────

// projectRoot walks up from the executable's real path to find the directory
// containing the Makefile. Returns "" if not found.
func projectRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}
	// The binary lives at <project>/bin/cogent — walk up looking for Makefile.
	dir := filepath.Dir(exe)
	for {
		if _, err := os.Stat(filepath.Join(dir, "Makefile")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func selfUpdate() error {
	root := projectRoot()
	if root == "" {
		return fmt.Errorf("cannot locate project root (no Makefile found above binary)")
	}

	fmt.Printf("\n%s%s⟳ rebuilding...%s\n", Bold, Cyan, Reset)

	cmd := exec.Command("make", "-C", root, "build")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	binary := filepath.Join(root, "bin", "cogent")
	if _, err := os.Stat(binary); err != nil {
		return fmt.Errorf("binary not found after build: %w", err)
	}

	fmt.Printf("%s%s⟳ restarting...%s\n\n", Bold, Cyan, Reset)
	return syscall.Exec(binary, os.Args, os.Environ())
}
