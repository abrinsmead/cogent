package agent

import (
	"bufio"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// envDescription returns a concise description of the runtime environment
// for the system prompt, e.g.:
//
//	"macOS (arm64)"
//	"Linux/Alpine 3.19 (amd64, BusyBox ash)"
//	"Linux/Ubuntu 22.04 (arm64, bash)"
func envDescription() string {
	var parts []string

	switch runtime.GOOS {
	case "darwin":
		parts = append(parts, "macOS")
	case "linux":
		if distro := linuxDistro(); distro != "" {
			parts = append(parts, "Linux/"+distro)
		} else {
			parts = append(parts, "Linux")
		}
	default:
		parts = append(parts, runtime.GOOS)
	}

	detail := []string{runtime.GOARCH}
	if sh := detectShell(); sh != "" {
		detail = append(detail, sh)
	}
	parts = append(parts, "("+strings.Join(detail, ", ")+")")

	return strings.Join(parts, " ")
}

// shellGuidance returns an extra guideline for the system prompt when the
// default shell is not bash. Returns "" when no special guidance is needed.
func shellGuidance() string {
	sh := detectShell()
	lower := strings.ToLower(sh)
	switch {
	case strings.Contains(lower, "busybox"):
		return "- The default shell is BusyBox ash — use only POSIX-compatible shell syntax (no bash-isms like [[ ]], arrays, brace expansion, or process substitution)"
	case lower == "dash" || lower == "ash":
		return "- The default shell is " + sh + " — use only POSIX-compatible shell syntax (no bash-isms)"
	default:
		return ""
	}
}

// detectShell identifies the shell available for command execution.
// It checks for BusyBox first (common in Alpine/embedded), then $SHELL,
// falling back to probing "sh".
func detectShell() string {
	// Check if sh is BusyBox
	if out, err := exec.Command("sh", "--help").CombinedOutput(); err == nil {
		if strings.Contains(string(out), "BusyBox") {
			return "BusyBox ash"
		}
	}

	// Use $SHELL if set
	if sh := os.Getenv("SHELL"); sh != "" {
		return shellBaseName(sh)
	}

	// Probe sh --version for identification
	if out, err := exec.Command("sh", "--version").CombinedOutput(); err == nil {
		s := string(out)
		if strings.Contains(s, "bash") {
			return "bash"
		}
		if strings.Contains(s, "zsh") {
			return "zsh"
		}
	}

	return "sh"
}

// linuxDistro reads /etc/os-release and returns a distro string like
// "Alpine 3.19" or "Ubuntu 22.04". Returns "" if unreadable.
func linuxDistro() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return ""
	}
	defer f.Close()

	var name, version string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.Trim(val, `"`)
		switch key {
		case "NAME":
			name = val
		case "VERSION_ID":
			version = val
		}
	}

	if name == "" {
		return ""
	}
	if version != "" {
		return name + " " + version
	}
	return name
}

// shellBaseName extracts the base name from a shell path,
// e.g. "/bin/bash" → "bash".
func shellBaseName(path string) string {
	i := strings.LastIndex(path, "/")
	if i >= 0 {
		return path[i+1:]
	}
	return path
}
