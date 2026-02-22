package tools

import (
	"path/filepath"
	"strings"
)

// shouldSkipDir returns filepath.SkipDir for directories that should be
// excluded from recursive walks (VCS dirs, dependency caches, hidden dirs).
func shouldSkipDir(path, root string) bool {
	base := filepath.Base(path)
	switch base {
	case ".git", "node_modules", "vendor", "__pycache__":
		return true
	}
	return strings.HasPrefix(base, ".") && path != root
}
