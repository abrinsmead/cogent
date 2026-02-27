// Package config loads the global settings file (~/.cogent/settings).
//
// The settings file uses a simple KEY=VALUE format (one per line, # comments,
// optional quoting). Values are applied to the process environment only when
// the key is not already set — explicit env vars always take precedence.
//
// Load should be called early in main, before api.NewClient and tool
// registry construction, so that keys like ANTHROPIC_API_KEY are available.
package config

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// Load reads ~/.cogent/settings and sets any keys that aren't already
// present in the environment. It silently returns nil when the file
// doesn't exist.
func Load() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil // can't locate home — nothing to load
	}
	return loadFile(filepath.Join(home, ".cogent", "settings"))
}

func loadFile(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// Strip surrounding quotes
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		// Only set if not already in the environment
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
	return scanner.Err()
}
