package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/toolsupply/ticket/internal/contract"
)

// ReplaceTask validates the current TASK.md, writes a complete temporary
// sibling, and atomically replaces it. The ticket-root lock protects cooperating
// ticket processes; external editors are outside this guarantee.
func (st *Store) ReplaceTask(id string, data []byte, maxBytes int) (bool, error) {
	loc, err := st.TicketLocation(id)
	if err != nil {
		return false, err
	}
	if loc.Archived() {
		return false, contract.NewError(contract.ErrArchived,
			"Archived tickets are read-only; unarchive the ticket before modifying it.", map[string]any{"id": id})
	}
	if st.root != nil {
		return st.replaceTaskRoot(id, data, maxBytes)
	}
	abs, err := st.taskPath(id)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return false, contract.NewError(contract.ErrNotFound, "Target does not exist.", nil)
		}
		return false, contract.NewError(contract.ErrIOError, "Cannot stat target: "+err.Error(), nil)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, contract.NewError(contract.ErrInvalidTicket, "Managed path is not a regular file (symlinks are not followed).", nil)
	}
	if info.Size() > int64(maxBytes) {
		return false, contract.NewError(contract.ErrFileTooLarge, "Object exceeds the managed size limit.", nil)
	}
	current, err := os.ReadFile(abs)
	if err != nil {
		return false, contract.NewError(contract.ErrIOError, "Cannot read target: "+err.Error(), nil)
	}
	if len(data) > maxBytes {
		return false, contract.NewError(contract.ErrFileTooLarge, "Replacement exceeds the managed size limit.", nil)
	}
	if string(current) == string(data) {
		return false, nil
	}
	tmp, err := temporaryPath(filepath.Dir(abs), filepath.Base(abs))
	if err != nil {
		return false, err
	}
	if err := writeComplete(tmp, data); err != nil {
		return false, contract.NewError(contract.ErrIOError, "Cannot write temporary replacement: "+err.Error(), nil)
	}
	if err := publishReplace(abs, tmp); err != nil {
		message := "Publish failed: " + err.Error()
		if recoveryErr := recoverFailedReplacement(abs, tmp); recoveryErr != nil {
			message += "; recovery failed: " + recoveryErr.Error()
		}
		return false, contract.NewError(contract.ErrIOError, message, nil)
	}
	return true, nil
}

func (st *Store) replaceTaskRoot(id string, data []byte, maxBytes int) (bool, error) {
	name, err := st.taskName(id)
	if err != nil {
		return false, err
	}
	info, err := st.root.Lstat(name)
	if err != nil {
		if os.IsNotExist(err) {
			return false, contract.NewError(contract.ErrNotFound, "Target does not exist.", nil)
		}
		return false, contract.NewError(contract.ErrIOError, "Cannot stat target: "+err.Error(), nil)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, contract.NewError(contract.ErrInvalidTicket, "Managed path is not a regular file (symlinks are not followed).", nil)
	}
	if info.Size() > int64(maxBytes) {
		return false, contract.NewError(contract.ErrFileTooLarge, "Object exceeds the managed size limit.", nil)
	}
	beforeManagedOpen()
	f, err := openRootNoFollow(st.root, name, os.O_RDONLY, 0)
	if err != nil {
		return false, contract.NewError(contract.ErrIOError, "Cannot read target: "+err.Error(), nil)
	}
	openedInfo, statErr := f.Stat()
	if statErr != nil || !os.SameFile(info, openedInfo) {
		f.Close()
		if statErr == nil {
			statErr = fmt.Errorf("managed path changed during open")
		}
		return false, contract.NewError(contract.ErrInvalidTicket, "Managed path changed during read (symlinks are not followed).", nil)
	}
	current, err := readBoundedOpenFile(f, int64(maxBytes))
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if errors.Is(err, ErrReadLimit) {
		return false, contract.NewError(contract.ErrFileTooLarge, "Object exceeds the managed size limit.", nil)
	}
	if err != nil {
		return false, contract.NewError(contract.ErrIOError, "Cannot read target: "+err.Error(), nil)
	}
	if len(data) > maxBytes {
		return false, contract.NewError(contract.ErrFileTooLarge, "Replacement exceeds the managed size limit.", nil)
	}
	if string(current) == string(data) {
		return false, nil
	}
	tmp, err := temporaryName(filepath.Base(name))
	if err != nil {
		return false, err
	}
	tmp = filepath.Join(filepath.Dir(name), tmp)
	if err := writeRootComplete(st.root, tmp, data, 0o644); err != nil {
		return false, contract.NewError(contract.ErrIOError, "Cannot write temporary replacement: "+err.Error(), nil)
	}
	if err := publishReplaceRoot(st.root, name, tmp); err != nil {
		_ = st.root.Remove(tmp)
		return false, contract.NewError(contract.ErrIOError, "Publish failed: "+err.Error(), nil)
	}
	return true, nil
}
