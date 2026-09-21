//go:build linux || darwin

package store

import "os"

func publishReplaceRoot(root *os.Root, target, tmp string) error {
	return root.Rename(tmp, target)
}

func recoverFailedReplacement(target, tmp string) error {
	if _, err := os.Lstat(target); err == nil {
		if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tmp, target)
}

// publishReplace atomically replaces target with tmp on POSIX
// filesystems: rename over an existing file is atomic; use
// platform replacement semantics.
func publishReplace(target, tmp string) error {
	return os.Rename(tmp, target)
}
