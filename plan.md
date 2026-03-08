# Homebrew Distribution Plan for Cogent

## Overview

Set up Homebrew distribution for `cogent` using a Homebrew tap (`homebrew-tap` repo pattern) with Makefile targets for building release archives, generating the formula, and publishing.

---

## Step 1: Add version management to the build

**File: `src/main.go`**
- Add a `Version` variable set via `-ldflags` at build time (defaults to `"dev"`)
- Wire it into Cobra's `rootCmd.Version`

**File: `Makefile`**
- Add a `VERSION` variable (read from `version.txt` or git tag, with a manual fallback)
- Pass `-ldflags "-X main.Version=$(VERSION)"` during build

**Create: `version.txt`**
- Single line file containing the current version (e.g. `0.1.0`)
- Source of truth for the version number

---

## Step 2: Add cross-compilation and archive targets to Makefile

Add `make dist` targets that build release archives for common platforms:

| Target | OS | Arch | Archive |
|--------|------|-------|---------|
| `darwin-arm64` | macOS | Apple Silicon | `.tar.gz` |
| `darwin-amd64` | macOS | Intel | `.tar.gz` |
| `linux-amd64` | Linux | x86_64 | `.tar.gz` |
| `linux-arm64` | Linux | ARM64 | `.tar.gz` |

Each archive contains the `cogent` binary and a copy of `README.md`.

New Makefile targets:
- **`make dist`** — Build all platform archives into `dist/`
- **`make dist-darwin-arm64`** (etc.) — Build a single platform archive
- **`make checksum`** — Generate `sha256sums.txt` for all archives in `dist/`

---

## Step 3: Create the Homebrew formula template

**Create: `Formula/cogent.rb`**

A Ruby formula template that:
- Downloads the correct `.tar.gz` for the user's OS/arch from a GitHub release URL
- Uses SHA256 checksums for verification
- Installs the binary to `bin/`
- Includes a simple `test` block that runs `cogent --version`

The formula will use `Hardware::CPU.arm?` to select the right archive for macOS.

---

## Step 4: Add formula generation to Makefile

**New target: `make formula`**

Uses `sed` to substitute the current version and SHA256 checksums into the formula template, producing a ready-to-commit `cogent.rb`.

Flow:
1. `make dist` — build archives
2. `make checksum` — compute SHA256 sums
3. `make formula` — inject version + sums into `Formula/cogent.rb`

---

## Step 5: Add publish targets to Makefile

**New target: `make release`**

Orchestrates the full release:
1. Validates `VERSION` is set and git is clean
2. Creates a git tag `v$(VERSION)`
3. Runs `make dist` and `make checksum`
4. Creates a GitHub release with `gh release create` attaching all archives + checksums
5. Runs `make formula` to update the formula with new checksums
6. Commits and pushes the updated formula

**New target: `make publish`**

A convenience alias that runs the full `make release` pipeline.

---

## Summary of new Makefile targets

| Target | Description |
|--------|-------------|
| `make build` | Build binary for current platform (existing) |
| `make install` | Install to /usr/local/bin (existing) |
| `make clean` | Remove build artifacts (existing) |
| `make dist` | Cross-compile and archive for all platforms |
| `make checksum` | Generate SHA256 checksums for archives |
| `make formula` | Generate Homebrew formula with current version/checksums |
| `make release` | Tag, build, upload GitHub release, update formula |
| `make publish` | Alias for `make release` |

---

## File changes summary

| File | Action |
|------|--------|
| `version.txt` | **Create** — version source of truth |
| `src/main.go` | **Edit** — add `Version` var, wire to Cobra |
| `Makefile` | **Edit** — add dist, checksum, formula, release, publish targets |
| `Formula/cogent.rb` | **Create** — Homebrew formula |

---

## User workflow after implementation

```bash
# Bump version
echo "0.2.0" > version.txt

# Full release (tag + build + GitHub release + update formula)
make publish

# Users install via:
brew tap anthropics/cogent
brew install cogent
```
