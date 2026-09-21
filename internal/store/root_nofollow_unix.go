//go:build linux || darwin

package store

import (
	"os"
	"syscall"
)

func openRootNoFollow(root *os.Root, name string, flags int, perm os.FileMode) (*os.File, error) {
	return root.OpenFile(name, flags|syscall.O_NOFOLLOW, perm)
}
