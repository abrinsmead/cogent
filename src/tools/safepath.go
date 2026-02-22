package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidatePathUnder checks that path (after resolving symlinks on existing
// prefixes) falls under root. It prevents directory-traversal attacks such
// as writing to /etc via "../../etc/passwd".
func ValidatePathUnder(path, root string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	// Resolve symlinks on root itself.
	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		return fmt.Errorf("resolve root symlinks: %w", err)
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	// Walk up the path to find the deepest existing ancestor and resolve
	// its symlinks. This handles the case where the target file doesn't
	// exist yet but an intermediate directory is a symlink escaping root.
	resolved := absPath
	for {
		r, err := filepath.EvalSymlinks(resolved)
		if err == nil {
			// Reconstruct the full path with the resolved prefix.
			tail, _ := filepath.Rel(resolved, absPath)
			if tail == "." {
				resolved = r
			} else {
				resolved = filepath.Join(r, tail)
			}
			break
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("resolve path symlinks: %w", err)
		}
		parent := filepath.Dir(resolved)
		if parent == resolved {
			// Reached filesystem root without finding an existing dir.
			break
		}
		resolved = parent
	}

	resolved = filepath.Clean(resolved)
	absRoot = filepath.Clean(absRoot)

	if !strings.HasPrefix(resolved, absRoot+string(filepath.Separator)) && resolved != absRoot {
		return fmt.Errorf("path %s is outside allowed directory %s", path, root)
	}
	return nil
}
