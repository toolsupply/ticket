package store

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/toolsupply/ticket/internal/contract"
)

var ErrTargetExists = errors.New("publication target already exists")

// PublishTicket creates a complete ticket directory in a temporary sibling
// and publishes it with one directory rename.
func (st *Store) PublishTicket(id string, data []byte, maxBytes int) error {
	if _, _, ok := ParseID(id); !ok {
		return contract.NewError(contract.ErrInvalidArgument,
			"Reference is not a valid ticket ID.", map[string]any{"id": id})
	}
	if len(data) > maxBytes {
		return contract.NewError(contract.ErrFileTooLarge, "Object exceeds the managed size limit.", nil)
	}
	if err := os.MkdirAll(st.Root, 0o755); err != nil {
		return contract.NewError(contract.ErrIOError, "Cannot prepare managed directory: "+err.Error(), nil)
	}
	final := filepath.Join(st.Root, id)
	if _, err := os.Lstat(final); err == nil {
		return ErrTargetExists
	} else if !os.IsNotExist(err) {
		return contract.NewError(contract.ErrIOError, "Cannot inspect publication target: "+err.Error(), nil)
	}
	tmp, err := temporaryPath(st.Root, id)
	if err != nil {
		return err
	}
	if err := os.Mkdir(tmp, 0o755); err != nil {
		return contract.NewError(contract.ErrIOError, "Cannot create temporary object: "+err.Error(), nil)
	}
	defer os.RemoveAll(tmp)
	if err := writeComplete(filepath.Join(tmp, "TASK.md"), data); err != nil {
		return contract.NewError(contract.ErrIOError, "Cannot write temporary object: "+err.Error(), nil)
	}
	if err := os.Rename(tmp, final); err != nil {
		return contract.NewError(contract.ErrIOError, "Publication rename failed: "+err.Error(), nil)
	}
	return nil
}

func temporaryPath(dir, base string) (string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", contract.NewError(contract.ErrRandomnessUnavailable, "System entropy source unavailable.", nil)
	}
	return filepath.Join(dir, "."+base+".tmp-"+fmt.Sprintf("%x", suffix[:])), nil
}

func writeComplete(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	n, writeErr := f.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = errors.New("short write")
	}
	closeErr := f.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		os.Remove(path)
	}
	return writeErr
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := temporaryPath(dir, filepath.Base(path))
	if err != nil {
		return err
	}
	if err := writeComplete(tmp, data); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
