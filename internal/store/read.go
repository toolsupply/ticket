package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrReadLimit reports a metadata file that exceeds its caller-provided
// conservative bound.
var ErrReadLimit = errors.New("metadata file exceeds configured limit")

// ReadBoundedFile reads at most limit+1 bytes, allowing callers to reject an
// oversized file before parsing or retaining an unbounded allocation.
func ReadBoundedFile(path string, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, ErrReadLimit
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > limit {
		return nil, ErrReadLimit
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrReadLimit
	}
	return data, nil
}

func readBoundedOpenFile(f *os.File, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, ErrReadLimit
	}
	if info, err := f.Stat(); err == nil && info.Size() > limit {
		return nil, ErrReadLimit
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrReadLimit
	}
	return data, nil
}

func ReadBoundedRoot(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := openRootNoFollow(root, name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readBoundedOpenFile(f, limit)
}

// ReadLocalFile reads one advisory marker without following a symlink or
// accepting a directory/device in its place.
func ReadLocalFile(root, name string, limit int64) ([]byte, error) {
	path := filepath.Join(root, ".local", name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("local marker is not a regular file")
	}
	return ReadBoundedFile(path, limit)
}

func ReadLocalRoot(root *os.Root, name string, limit int64) ([]byte, error) {
	path := filepath.Join(".local", name)
	info, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("local marker is not a regular file")
	}
	return ReadBoundedRoot(root, path, limit)
}
