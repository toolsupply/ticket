package store

import (
	"fmt"
	"os"
	"path/filepath"

	"ticket/internal/contract"
)

// Discover resolves the ticket root by walking upward from cwd and checking
// <dir>/tickets/config.json. The nearest valid ticket repository wins;
// discovery is independent of any particular source-control layout.
func Discover(cwd string) (string, error) {
	notFound := func(format string, args ...any) *contract.Error {
		return contract.NewError(contract.ErrRepoNotFound,
			fmt.Sprintf(format, args...), nil)
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", notFound("cannot resolve current directory")
	}
	if configured := os.Getenv("TICKET_ROOT"); configured != "" {
		root := configured
		if !filepath.IsAbs(root) {
			root = filepath.Join(abs, root)
		}
		root = filepath.Clean(root)
		if _, err := LoadConfig(root); err != nil {
			return "", err
		}
		return root, nil
	}
	dir := abs
	for {
		candidate := filepath.Join(dir, "tickets")
		if isConfigMarker(candidate) {
			if _, err := LoadConfig(candidate); err != nil {
				return "", err
			}
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", notFound("no ticket repository found from %s", cwd)
}

// isConfigMarker reports whether dir/tickets/config.json exists. A symlinked
// tickets directory is recognized as a marker so LoadConfig can reject it
// explicitly; a directory named tickets without the marker is not a
// repository root.
func isConfigMarker(candidate string) bool {
	root, err := os.Lstat(candidate)
	if err != nil {
		return false
	}
	if root.Mode()&os.ModeSymlink != 0 {
		return true
	}
	if !root.IsDir() {
		return false
	}
	_, err = os.Lstat(filepath.Join(candidate, configFileName))
	return err == nil
}
