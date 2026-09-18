package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

var changeSequence atomic.Uint64

// SignalChange writes the advisory local wake-up marker. The marker carries
// no repository state and is excluded from SCM by .local/. A sequence suffix
// makes successive signals distinguishable even on coarse filesystems.
func (st *Store) SignalChange() error {
	value := fmt.Sprintf("%d-%d\n", time.Now().UnixNano(), changeSequence.Add(1))
	return writeLocalFile(filepath.Join(st.Root, ".local", "change"), []byte(value))
}

// RememberCurrent writes the advisory current-ticket marker.
func (st *Store) RememberCurrent(id string) error {
	return writeLocalFile(filepath.Join(st.Root, ".local", "current"), []byte(id+"\n"))
}

// ClearCurrent removes the advisory current-ticket marker without following
// links or touching any other local state.
func (st *Store) ClearCurrent() error {
	path := filepath.Join(st.Root, ".local", "current")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("local destination is not a regular file")
	}
	return os.Remove(path)
}

func writeLocalFile(path string, data []byte) error {
	info, err := os.Lstat(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if exists && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return fmt.Errorf("local destination is not a regular file")
	}
	mode := os.FileMode(0o600)
	if exists {
		mode = info.Mode().Perm()
	}
	tmp, err := temporaryPath(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return err
	}
	if err := writeLocalComplete(tmp, data); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if exists {
		err = publishReplace(path, tmp)
	} else {
		if current, statErr := os.Lstat(path); statErr == nil {
			if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
				_ = os.Remove(tmp)
				return fmt.Errorf("local destination is not a regular file")
			}
			err = publishReplace(path, tmp)
		} else if os.IsNotExist(statErr) {
			err = os.Rename(tmp, path)
		} else {
			err = statErr
		}
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

func writeLocalComplete(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, writeErr := f.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = fmt.Errorf("short write")
	}
	closeErr := f.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(path)
	}
	return writeErr
}

// ChangeState returns the marker contents, treating a missing marker as the
// initial state.
func ChangeState(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, ".local", "change"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}
