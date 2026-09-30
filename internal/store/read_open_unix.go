//go:build !windows

package store

import "os"

func openBoundedReadFile(path string) (*os.File, error) {
	return os.Open(path)
}

func openBoundedReadRoot(root *os.Root, name string) (*os.File, error) {
	return openRootNoFollow(root, name, os.O_RDONLY, 0)
}
