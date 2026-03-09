// Package config loads settings files (~/.cogent/settings and .cogent/settings).
//
// The settings file uses a simple KEY=VALUE format (one per line, # comments,
// optional quoting). Values are applied to the process environment only when
// the key is not already set — explicit env vars always take precedence.
//
// Precedence (highest to lowest):
//  1. Explicit environment variables
//  2. Project-local .cogent/settings (in working directory)
//  3. Global ~/.cogent/settings
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

// Load reads project-local (.cogent/settings) and global (~/.cogent/settings)
// settings files, applying keys that aren't already present in the environment.
// Project-local settings take precedence over global because they load first;
// loadFile only sets keys not already present.
func Load() error {
	// Load project-local settings first (higher priority).
	if err := loadProjectSettings(); err != nil {
		return err
	}
	// Load global settings (lower priority — won't override project-local).
	home, err := os.UserHomeDir()
	if err == nil {
		return loadFile(filepath.Join(home, ".cogent", "settings"))
	}
	return nil
}

// loadProjectSettings walks up from the current directory looking for
// .cogent/settings, loading the first one found. This allows project-level
// config to override global defaults.
func loadProjectSettings() error {
	dir, err := os.Getwd()
	if err != nil {
		return nil
	}
	for {
		candidate := filepath.Join(dir, ".cogent", "settings")
		if _, err := os.Stat(candidate); err == nil {
			return loadFile(candidate)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil
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
