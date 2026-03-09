package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSkill(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")

	content := `---
name: pdf
description: Use this skill for PDF file operations
---

# PDF Processing Guide

Use pypdf for basic operations.

## Examples
- Merge PDFs
- Split PDFs
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	skill, err := LoadSkill(path)
	if err != nil {
		t.Fatalf("LoadSkill failed: %v", err)
	}

	if skill.Name != "pdf" {
		t.Errorf("name = %q, want %q", skill.Name, "pdf")
	}
	if skill.Description != "Use this skill for PDF file operations" {
		t.Errorf("description = %q, want %q", skill.Description, "Use this skill for PDF file operations")
	}
	if !strings.Contains(skill.Instructions, "PDF Processing Guide") {
		t.Errorf("instructions should contain 'PDF Processing Guide', got %q", skill.Instructions)
	}
	if !strings.Contains(skill.Instructions, "Merge PDFs") {
		t.Errorf("instructions should contain 'Merge PDFs'")
	}
	if skill.Dir != dir {
		t.Errorf("dir = %q, want %q", skill.Dir, dir)
	}
}

func TestLoadSkill_MissingFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")

	if err := os.WriteFile(path, []byte("# No frontmatter here"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadSkill(path)
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestLoadSkill_MissingName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")

	content := `---
description: some description
---

Instructions here.
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadSkill(path)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestLoadSkill_MissingDescription(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")

	content := `---
name: test-skill
---

Instructions here.
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadSkill(path)
	if err == nil {
		t.Fatal("expected error for missing description")
	}
}

func TestDiscoverSkills(t *testing.T) {
	// Create a skills directory with two skill subdirectories
	base := t.TempDir()

	// Skill 1
	skill1Dir := filepath.Join(base, "pdf")
	os.MkdirAll(skill1Dir, 0755)
	os.WriteFile(filepath.Join(skill1Dir, "SKILL.md"), []byte(`---
name: pdf
description: PDF operations
---

Use pypdf.
`), 0644)

	// Skill 2
	skill2Dir := filepath.Join(base, "docx")
	os.MkdirAll(skill2Dir, 0755)
	os.WriteFile(filepath.Join(skill2Dir, "SKILL.md"), []byte(`---
name: docx
description: Word document operations
---

Use python-docx.
`), 0644)

	// Directory without SKILL.md (should be silently skipped)
	os.MkdirAll(filepath.Join(base, "empty-dir"), 0755)

	skills, warnings := DiscoverSkills([]string{base})
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(skills) != 2 {
		t.Fatalf("got %d skills, want 2", len(skills))
	}

	names := map[string]bool{}
	for _, s := range skills {
		names[s.Name] = true
	}
	if !names["pdf"] || !names["docx"] {
		t.Errorf("expected pdf and docx skills, got %v", names)
	}
}

func TestDiscoverSkills_Dedup(t *testing.T) {
	// Create two directories with the same skill name.
	// First directory (project-local) should win.
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	skillDir1 := filepath.Join(dir1, "my-skill")
	os.MkdirAll(skillDir1, 0755)
	os.WriteFile(filepath.Join(skillDir1, "SKILL.md"), []byte(`---
name: my-skill
description: Local version
---

Local instructions.
`), 0644)

	skillDir2 := filepath.Join(dir2, "my-skill")
	os.MkdirAll(skillDir2, 0755)
	os.WriteFile(filepath.Join(skillDir2, "SKILL.md"), []byte(`---
name: my-skill
description: Global version
---

Global instructions.
`), 0644)

	skills, _ := DiscoverSkills([]string{dir1, dir2})
	if len(skills) != 1 {
		t.Fatalf("got %d skills, want 1 (dedup)", len(skills))
	}
	if skills[0].Description != "Local version" {
		t.Errorf("expected local version to win, got %q", skills[0].Description)
	}
}

func TestDiscoverSkills_NonexistentDir(t *testing.T) {
	skills, warnings := DiscoverSkills([]string{"/nonexistent/path"})
	if len(skills) != 0 {
		t.Errorf("expected no skills from nonexistent dir")
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings from nonexistent dir")
	}
}

func TestFormatSkillsPrompt(t *testing.T) {
	skills := []*Skill{
		{
			Name:         "pdf",
			Description:  "PDF file operations",
			Instructions: "Use pypdf for basic ops.",
		},
	}

	prompt := FormatSkillsPrompt(skills)
	if !strings.Contains(prompt, "# Agent Skills") {
		t.Error("prompt should contain header")
	}
	if !strings.Contains(prompt, "## Skill: pdf") {
		t.Error("prompt should contain skill name")
	}
	if !strings.Contains(prompt, "PDF file operations") {
		t.Error("prompt should contain description")
	}
	if !strings.Contains(prompt, "Use pypdf for basic ops.") {
		t.Error("prompt should contain instructions")
	}
}

func TestFormatSkillsPrompt_Empty(t *testing.T) {
	prompt := FormatSkillsPrompt(nil)
	if prompt != "" {
		t.Errorf("expected empty prompt for nil skills, got %q", prompt)
	}
}

func TestSkillsExist(t *testing.T) {
	base := t.TempDir()

	// No skills directory yet
	if SkillsExist(base) {
		t.Error("SkillsExist should be false with no .cogent/skills/ dir")
	}

	// Create skills dir but no skill subdirectories
	skillsDir := filepath.Join(base, ".cogent", "skills")
	os.MkdirAll(skillsDir, 0755)
	if SkillsExist(base) {
		t.Error("SkillsExist should be false with empty skills dir")
	}

	// Create a skill subdirectory with SKILL.md
	skillDir := filepath.Join(skillsDir, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: test
description: test skill
---

Instructions.
`), 0644)

	if !SkillsExist(base) {
		t.Error("SkillsExist should be true with a valid skill")
	}
}
