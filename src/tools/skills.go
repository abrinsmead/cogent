package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Skill represents an Agent Skill loaded from a SKILL.md file.
// Skills are folders of instructions that extend the agent's capabilities.
// Format: YAML frontmatter (name, description) followed by markdown instructions.
type Skill struct {
	Name         string // unique identifier (lowercase, hyphens)
	Description  string // when to activate this skill
	Instructions string // markdown body with guidelines and examples
	Dir          string // directory containing the skill (for resolving relative references)
}

// LoadSkill reads and parses a SKILL.md file. Returns nil if the file
// cannot be parsed or is missing required frontmatter fields.
func LoadSkill(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)

	// Parse YAML frontmatter between --- delimiters
	if !strings.HasPrefix(content, "---") {
		return nil, fmt.Errorf("missing YAML frontmatter")
	}

	// Find closing ---
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return nil, fmt.Errorf("unclosed YAML frontmatter")
	}

	frontmatter := rest[:idx]
	body := strings.TrimSpace(rest[idx+4:]) // skip past \n---

	// Parse simple key: value pairs from frontmatter
	name, description := "", ""
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "name":
			name = value
		case "description":
			description = value
		}
	}

	if name == "" {
		return nil, fmt.Errorf("missing required 'name' field in frontmatter")
	}
	if description == "" {
		return nil, fmt.Errorf("missing required 'description' field in frontmatter")
	}

	return &Skill{
		Name:         name,
		Description:  description,
		Instructions: body,
		Dir:          filepath.Dir(path),
	}, nil
}

// DiscoverSkills scans directories for SKILL.md files.
// Each skill lives in its own subdirectory containing a SKILL.md file.
// Returns loaded skills and any warnings for skills that failed to load.
func DiscoverSkills(dirs []string) ([]*Skill, []string) {
	var skills []*Skill
	var warnings []string
	seen := make(map[string]bool)

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // directory doesn't exist — that's fine
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
			skill, err := LoadSkill(skillPath)
			if err != nil {
				if !os.IsNotExist(err) {
					warnings = append(warnings,
						fmt.Sprintf("skill %s: %s", entry.Name(), err))
				}
				continue
			}

			// Skip duplicates (project-local takes precedence)
			if seen[skill.Name] {
				continue
			}
			seen[skill.Name] = true
			skills = append(skills, skill)
		}
	}

	return skills, warnings
}

// SkillDirs returns the directories to scan for agent skills.
// Project-local (.cogent/skills/) is listed first so it takes precedence.
func SkillDirs(cwd string) []string {
	dirs := []string{filepath.Join(cwd, ".cogent", "skills")}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".cogent", "skills"))
	}
	return dirs
}

// SkillsExist returns true if any skills directory contains skill subdirectories.
func SkillsExist(cwd string) bool {
	for _, dir := range SkillDirs(cwd) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
				if _, err := os.Stat(skillPath); err == nil {
					return true
				}
			}
		}
	}
	return false
}

// FormatSkillsPrompt builds a system prompt section describing the available skills.
func FormatSkillsPrompt(skills []*Skill) string {
	if len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# Agent Skills\n\n")
	sb.WriteString("The following skills are available. Use the appropriate skill when the user's request matches its description.\n\n")

	for _, s := range skills {
		sb.WriteString(fmt.Sprintf("## Skill: %s\n", s.Name))
		sb.WriteString(fmt.Sprintf("**When to use:** %s\n\n", s.Description))
		if s.Instructions != "" {
			sb.WriteString(s.Instructions)
			sb.WriteString("\n\n")
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}
