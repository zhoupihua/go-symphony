package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SanitizeKey replaces characters not in [A-Za-z0-9._-] with underscore.
// Used to create safe directory names from issue identifiers.
func SanitizeKey(identifier string) string {
	var b strings.Builder
	for _, r := range identifier {
		if isSafeRune(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

// isSafeRune reports whether r is an allowed character in workspace directory names.
func isSafeRune(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
}

// Canonicalize resolves symlinks and returns the canonical absolute path.
// If the path does not exist, it resolves as much of the path as possible
// and falls back to filepath.Abs for the remainder.
func Canonicalize(path string) (string, error) {
	// Try full evaluation first (works if path exists)
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}

	// Path doesn't exist (or can't be resolved). Walk up until we find
	// an existing ancestor, resolve that, then append the rest.
	dir := path
	trailing := ""
	for {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			// Found an existing ancestor; combine with the non-existing trailing part.
			result := filepath.Join(resolved, trailing)
			return filepath.Clean(result), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the root without finding an existing path; use Abs as fallback.
			abs, absErr := filepath.Abs(path)
			if absErr != nil {
				return "", fmt.Errorf("canonicalize %q: %w (abs fallback: %w)", path, err, absErr)
			}
			return filepath.Clean(abs), nil
		}
		trailing = filepath.Join(filepath.Base(dir), trailing)
		dir = parent
	}
}

// IsUnderRoot checks that path stays under root after canonicalization.
// Returns error if path escapes root.
func IsUnderRoot(root, path string) error {
	canonicalRoot, err := Canonicalize(root)
	if err != nil {
		return fmt.Errorf("canonicalize root: %w", err)
	}
	canonicalPath, err := Canonicalize(path)
	if err != nil {
		return fmt.Errorf("canonicalize path: %w", err)
	}

	// Path must be exactly the root or a descendant of it.
	if canonicalPath == canonicalRoot {
		return nil
	}
	expectedPrefix := canonicalRoot + string(filepath.Separator)
	if !strings.HasPrefix(canonicalPath, expectedPrefix) {
		return fmt.Errorf("path %q escapes root %q", canonicalPath, canonicalRoot)
	}
	return nil
}
