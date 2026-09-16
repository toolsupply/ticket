//go:build linux || darwin

package store

import "os"

// publishReplace atomically replaces target with tmp on POSIX
// filesystems: rename over an existing file is atomic; use
// platform replacement semantics.
func publishReplace(target, tmp string) error {
	return os.Rename(tmp, target)
}
