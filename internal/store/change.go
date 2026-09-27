package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/toolsupply/ticket/internal/contract"
)

const changeMarkerMaxBytes int64 = 256
const repositoryIDBackfillMarker = "repository-id-backfill-pending"

var repositoryIDBackfillMarkerValue = []byte("pending\n")

var changeSequence atomic.Uint64

// SignalChange writes the advisory local wake-up marker. The marker carries
// no repository state and is excluded from SCM by .local/. A sequence suffix
// makes successive signals distinguishable even on coarse filesystems.
func (st *Store) SignalChange() error {
	value := fmt.Sprintf("%d-%d\n", time.Now().UnixNano(), changeSequence.Add(1))
	if st.root != nil {
		return writeLocalRoot(st.root, "change", []byte(value))
	}
	return writeLocalFile(filepath.Join(st.Root, ".local", "change"), []byte(value))
}

// RememberCurrent writes the advisory current-ticket marker.
func (st *Store) RememberCurrent(id string) error {
	if st.root != nil {
		return writeLocalRoot(st.root, "current", []byte(id+"\n"))
	}
	return writeLocalFile(filepath.Join(st.Root, ".local", "current"), []byte(id+"\n"))
}

// ClearCurrent removes the advisory current-ticket marker without following
// links or touching any other local state.
func (st *Store) ClearCurrent() error {
	if st.root != nil {
		return clearLocalRoot(st.root, "current")
	}
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

// RepositoryIDBackfillPending reports whether an SCM-backed repository ID
// backfill still needs to be committed and published.
func (st *Store) RepositoryIDBackfillPending() (bool, error) {
	if st == nil || st.Lock == nil || st.Lock.file == nil || st.root == nil {
		return false, contract.NewError(contract.ErrInvalidRepository,
			"Ticket repository lock is not active.", nil)
	}
	data, err := ReadLocalRoot(st.root, repositoryIDBackfillMarker, int64(len(repositoryIDBackfillMarkerValue)))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, contract.NewError(contract.ErrIOError,
			"Cannot read repository-ID migration marker: "+err.Error(), nil)
	}
	if string(data) != string(repositoryIDBackfillMarkerValue) {
		return false, contract.NewError(contract.ErrInvalidRepository,
			"Repository-ID migration marker is malformed.", nil)
	}
	return true, nil
}

// MarkRepositoryIDBackfillPending records a local retry marker before an SCM
// backed repository ID is generated, so a failed commit or publish is retried.
func (st *Store) MarkRepositoryIDBackfillPending() error {
	if st == nil || st.Lock == nil || st.Lock.file == nil || st.root == nil {
		return contract.NewError(contract.ErrInvalidRepository,
			"Ticket repository lock is not active.", nil)
	}
	if err := writeLocalRoot(st.root, repositoryIDBackfillMarker, repositoryIDBackfillMarkerValue); err != nil {
		return contract.NewError(contract.ErrIOError,
			"Cannot record repository-ID migration state: "+err.Error(), nil)
	}
	return nil
}

// ClearRepositoryIDBackfillPending removes the retry marker after the ID has
// been committed and published successfully.
func (st *Store) ClearRepositoryIDBackfillPending() error {
	if st == nil || st.Lock == nil || st.Lock.file == nil || st.root == nil {
		return contract.NewError(contract.ErrInvalidRepository,
			"Ticket repository lock is not active.", nil)
	}
	if err := clearLocalRoot(st.root, repositoryIDBackfillMarker); err != nil {
		return contract.NewError(contract.ErrIOError,
			"Cannot clear repository-ID migration state: "+err.Error(), nil)
	}
	return nil
}

func clearLocalRoot(root *os.Root, name string) error {
	path := filepath.Join(".local", name)
	info, err := root.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("local destination is not a regular file")
	}
	return root.Remove(path)
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

func writeLocalRoot(root *os.Root, name string, data []byte) error {
	path := filepath.Join(".local", name)
	info, err := root.Lstat(path)
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
	tmp, err := temporaryName(name)
	if err != nil {
		return err
	}
	tmp = filepath.Join(".local", tmp)
	if err := writeRootComplete(root, tmp, data, 0o600); err != nil {
		return err
	}
	if err := root.Chmod(tmp, mode); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	if exists {
		err = root.Rename(tmp, path)
	} else {
		if current, statErr := root.Lstat(path); statErr == nil {
			if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
				_ = root.Remove(tmp)
				return fmt.Errorf("local destination is not a regular file")
			}
			err = root.Rename(tmp, path)
		} else if os.IsNotExist(statErr) {
			err = root.Rename(tmp, path)
		} else {
			err = statErr
		}
	}
	if err != nil {
		_ = root.Remove(tmp)
	}
	return err
}

func writeRootComplete(root *os.Root, path string, data []byte, mode os.FileMode) error {
	f, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
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
		_ = root.Remove(path)
	}
	return writeErr
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
	data, err := ReadLocalFile(root, "change", changeMarkerMaxBytes)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ChangeState reads the advisory marker through Store's stable root handle.
func (st *Store) ChangeState() (string, error) {
	if st.root != nil {
		return changeStateRoot(st.root)
	}
	return ChangeState(st.Root)
}

func changeStateRoot(root *os.Root) (string, error) {
	data, err := ReadLocalRoot(root, "change", changeMarkerMaxBytes)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}
