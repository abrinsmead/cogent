# Skills Implementation Plan

## Context

Cogent currently supports **custom tools** (executable scripts in `.cogent/tools/` with `@` directives) and **AGENTS.md** for context injection. Agent Skills are an [open standard](https://agentskills.io) developed by Anthropic and adopted by Claude Code, GitHub Copilot, Cursor, OpenAI Codex, OpenCode, and 18+ other agents.

Skills and tools solve different problems and are complementary:
- **Tools** = executable capabilities (bash, read, write, edit, dispatch, custom scripts). The agent's hands.
- **Skills** = prompt-injected instructions, workflows, and reference material. The agent's domain expertise.

Skills cannot replace tools — they depend on tools to execute. We add skills **alongside** the existing tools system.

## Goals

1. **Ecosystem compatibility** — read skills from `.cogent/skills/`, `.claude/skills/`, `.agents/skills/` (project + global).
2. **Progressive disclosure** — advertise skill names + descriptions in system prompt (~100 tokens each); load full content on demand via a `skill` tool.
3. **User invocation** — `/skill-name [args]` slash commands in TUI and REPL.
4. **Model invocation** — LLM can autonomously load skills via the `skill` tool when relevant.
5. **No new dependencies** — minimal frontmatter parser, no external YAML library.

## Architecture

### New Package: `src/skills/`

#### `skills.go` — Types, parsing, discovery

```go
type Skill struct {
    Name                   string
    Description            string
    ArgumentHint           string   // e.g. "[issue-number]"
    DisableModelInvocation bool     // true = only user can invoke via /command
    UserInvocable          bool     // false = hidden from slash menu (default: true)
    Content                string   // markdown body after frontmatter
    Dir                    string   // directory containing the skill (for relative file refs)
}
```

**`ParseSkillFile(path string) (*Skill, error)`** — reads SKILL.md, splits `---` delimited YAML frontmatter into `map[string]string`, maps to Skill fields. Derives name from parent directory if not in frontmatter.

**`Discover(cwd string) (*Catalog, []string)`** — scans directories in priority order (later wins on name collision):

1. `~/.cogent/skills/*/SKILL.md` (personal)
2. `~/.claude/skills/*/SKILL.md` (personal, cross-ecosystem)
3. `~/.agents/skills/*/SKILL.md` (personal, cross-ecosystem)
4. Walk up from cwd to repo root:
   - `.cogent/skills/*/SKILL.md`
   - `.claude/skills/*/SKILL.md`
   - `.agents/skills/*/SKILL.md`
5. `<cwd>/.cogent/commands/*.md` (flat-file compat, project)
6. `~/.cogent/commands/*.md` (flat-file compat, personal)

Project-local overrides personal on name collision. Returns warnings for parse errors.

#### `catalog.go` — Catalog type

```go
type Catalog struct { skills map[string]*Skill }
```

Methods:
- `Get(name string) *Skill`
- `UserInvocable() []*Skill` — skills the user can trigger via `/name`
- `ModelInvocable() []*Skill` — skills the LLM can load via the skill tool
- `Names() []string` — sorted list of all skill names
- `SystemPromptSection() string` — generates the skill catalog for the system prompt

`SystemPromptSection()` output example:
```
The following skills are available. Use the `skill` tool to load one when relevant:
- deploy: Deploy the application to production
- explain-code: Explains code with visual diagrams and analogies
- review: Code review following team standards
```

Only includes skills where `DisableModelInvocation` is false.

#### `expand.go` — Content expansion

`Expand(content, args string) (string, error)`:
1. `$ARGUMENTS` → full args string
2. `$ARGUMENTS[N]` / `$N` → Nth whitespace-delimited arg
3. `` !`command` `` → execute shell, replace with stdout (10s timeout)
4. If `$ARGUMENTS` not present in content and args non-empty, append `\nARGUMENTS: <args>`

#### `skills_test.go` — Tests

Unit tests for:
- Frontmatter parsing (valid, missing fields, malformed)
- Discovery with temp dirs and priority ordering
- Name collision resolution
- Expansion substitutions
- Cross-ecosystem directory scanning

### New File: `src/tools/skill.go` — Skill Tool

```go
type SkillTool struct {
    Lookup func(name string) *skills.Skill
}
```

- Tool name: `skill`
- Description: "Load a skill's instructions by name. Returns the skill's full content for you to follow."
- Input: `{"name": "skill-name"}`
- Execute: looks up skill, validates `DisableModelInvocation`, returns expanded content
- `RequiresConfirmation() bool` → `false` (read-only, just returns text)
- Not registered if catalog has zero model-invocable skills

### Modified: `src/agent/agent.go`

1. Add `skillCatalog *skills.Catalog` field to `Agent`
2. Add `WithSkillCatalog(c *skills.Catalog) Option`
3. Add `SkillCatalog() *skills.Catalog` getter (for TUI slash-command handling)
4. In `New()`, after options applied, inject skill catalog into system prompt:
   ```go
   if a.skillCatalog != nil {
       if section := a.skillCatalog.SystemPromptSection(); section != "" {
           a.system += "\n\n" + section
       }
   }
   ```
5. Register `SkillTool` in registry when catalog has model-invocable skills

### Modified: `src/cli/session.go`

- `newSession` accepts a `*skills.Catalog` parameter
- Passes catalog to agent via `WithSkillCatalog`
- Catalog discovered once per `tuiModel`, shared across tabs

### Modified: `src/cli/tui.go`

1. Discover skills during `tuiModel` init (alongside tool registry)
2. In `handleInput`: before the unknown-`/` catch-all, check skill catalog for matching slash command. If found, expand content, send via `sendToAgent`.
3. Update `/help` to list available user-invocable skills
4. Add `/skills` command to list all available skills with descriptions

### Modified: `src/cli/headless.go`

- Discover skills and pass catalog to agent via `WithSkillCatalog`
- No slash-command handling needed (headless takes a single prompt)

### Modified: `src/cli/line.go`

- Add `lineSkillsLoaded` type and render case (shows discovered skill count on startup, similar to `lineToolsLoaded`)

## Implementation Order

| Phase | Files | Description |
|---|---|---|
| 1 | `src/skills/skills.go` | Skill type, `parseFrontmatter`, `ParseSkillFile`, `Discover` |
| 2 | `src/skills/catalog.go` | Catalog with query methods and `SystemPromptSection` |
| 3 | `src/skills/expand.go` | `Expand` function with `$ARGUMENTS` and `` !`command` `` |
| 4 | `src/skills/skills_test.go` | Unit tests for parsing, discovery, expansion |
| 5 | `src/tools/skill.go` | SkillTool (read-only tool for LLM invocation) |
| 6 | `src/agent/agent.go` | `WithSkillCatalog`, system prompt injection, SkillTool registration |
| 7 | `src/cli/session.go` | Accept + pass catalog to agent |
| 8 | `src/cli/tui.go` | Discover skills, `/skill-name` interception, `/skills` + `/help` updates |
| 9 | `src/cli/headless.go` | Discover + pass catalog |
| 10 | `src/cli/line.go` | `lineSkillsLoaded` display |

## Discovery Paths (Cross-Ecosystem)

```
# Personal (global)
~/.cogent/skills/*/SKILL.md
~/.cogent/commands/*.md
~/.claude/skills/*/SKILL.md
~/.agents/skills/*/SKILL.md

# Project (walk up from cwd to repo root)
.cogent/skills/*/SKILL.md
.cogent/commands/*.md
.claude/skills/*/SKILL.md
.agents/skills/*/SKILL.md
```

## MVP Scope

**In scope:**
- Skill discovery from all standard paths
- Minimal YAML frontmatter parser (no external dep)
- `$ARGUMENTS` substitution and `` !`command` `` expansion
- `skill` tool for LLM-driven activation
- `/skill-name` user invocation in TUI/REPL
- System prompt injection (name + description catalog)
- `.cogent/commands/` flat-file compatibility
- Startup display of loaded skills

**Deferred:**
- `context: fork` (sub-agent execution) — skills run inline in MVP
- `model` override per skill
- `allowed-tools` enforcement
- Tab completion / autocomplete for skill names
- Remote / installable skills (package manager)
- Nested directory discovery
- `.well-known/skills/` web discovery

## Verification

1. Create test skill: `mkdir -p .cogent/skills/hello && echo '---\nname: hello\ndescription: Say hello\n---\nGreet the user warmly.' > .cogent/skills/hello/SKILL.md`
2. Run cogent — verify skill shows in startup output
3. Type `/hello world` — verify expanded content sent to agent
4. Verify LLM can invoke the `skill` tool autonomously when relevant
5. Place a skill in `~/.claude/skills/` — verify cross-ecosystem discovery
6. Run `go test ./skills/...` for unit tests
