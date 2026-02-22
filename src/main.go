package main

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

const (
	reset  = "\033[0m"
	cyan   = "\033[36m"
	yellow = "\033[33m"
	green  = "\033[32m"
	red    = "\033[31m"
	dim    = "\033[2m"
	bold   = "\033[1m"
)

// srcDir returns the directory containing the Go source (where Makefile lives).
func srcDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

// selfUpdate rebuilds the binary and re-execs into it, preserving cwd and env.
// On success this function never returns — the process is replaced.
func selfUpdate() error {
	src := srcDir()
	if src == "" {
		return fmt.Errorf("cannot locate source directory")
	}
	makefile := filepath.Join(src, "Makefile")
	if _, err := os.Stat(makefile); err != nil {
		return fmt.Errorf("Makefile not found at %s", makefile)
	}

	fmt.Printf("\n%s%s⟳ rebuilding...%s\n", bold, cyan, reset)

	cmd := exec.Command("make", "-C", src, "build")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	binary := filepath.Join(src, "go-agent")
	if _, err := os.Stat(binary); err != nil {
		return fmt.Errorf("binary not found after build: %w", err)
	}

	fmt.Printf("%s%s⟳ restarting...%s\n\n", bold, cyan, reset)

	// Replace current process with the new binary.
	return syscall.Exec(binary, os.Args, os.Environ())
}

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine working directory: %s\n", err)
		os.Exit(1)
	}
	client, err := api.NewClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\nSet ANTHROPIC_API_KEY.\n", err)
		os.Exit(1)
	}

	reader := bufio.NewReader(os.Stdin)

	ag := agent.New(client, cwd,
		agent.WithTextCallback(func(text string) {
			fmt.Printf("%s%s%s\n", dim, text, reset)
		}),
		agent.WithToolCallback(func(name, summary string) {
			color := green
			switch name {
			case "bash", "write", "edit":
				color = red
			}
			fmt.Printf("%s%s %s%s %s%s\n", dim, color, name, reset, dim, summary+reset)
		}),
		agent.WithConfirmCallback(func(name, summary string) bool {
			fmt.Printf("%s%sAllow %s %s? [Y/n]%s ", bold, yellow, name, summary, reset)
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(strings.ToLower(line))
			return line == "" || line == "y" || line == "yes"
		}),
	)
	fmt.Printf("%s%s agent %s— busybox coding assistant%s\n", bold, cyan, reset+dim, reset)
	fmt.Printf("%smodel: %s | cwd: %s%s\n\n", dim, client.Model(), cwd, reset)
	if len(os.Args) > 1 {
		prompt := strings.Join(os.Args[1:], " ")
		fmt.Printf("%s> %s%s\n", green, prompt, reset)
		if err := ag.Send(prompt); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
			os.Exit(1)
		}
		return
	}
	for {
		fmt.Printf("%s%s> %s", bold, green, reset)
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
			return
		case "/restart", "/update":
			if err := selfUpdate(); err != nil {
				fmt.Fprintf(os.Stderr, "%sRestart failed: %s%s\n", yellow, err, reset)
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
			fmt.Fprintf(os.Stderr, "%sError: %s%s\n", yellow, err, reset)
		}
		fmt.Println()
	}
}
