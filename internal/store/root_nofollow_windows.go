//go:build windows

package store

import (
	"os"
	"syscall"
)

// os.Root enforces containment on Windows. The platform does not expose a
// portable no-follow flag through the standard library, so callers pair this
// open with an identity check against the validated descriptor.
func openRootNoFollow(root *os.Root, name string, flags int, perm os.FileMode) (*os.File, error) {
	return root.OpenFile(name, flags|syscall.FILE_FLAG_OPEN_REPARSE_POINT, perm)
}
