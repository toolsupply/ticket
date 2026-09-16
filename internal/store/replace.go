package store

import (
	"os"
	"path/filepath"

	"ticket/internal/contract"
)

// ReplaceTask validates the current TASK.md, writes a complete temporary
// sibling, and atomically replaces it. The ticket-root lock protects cooperating
// ticket processes; external editors are outside this guarantee.
func (st *Store) ReplaceTask(id string, data []byte, maxBytes int) (bool, error) {
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
		os.Remove(tmp)
		return false, contract.NewError(contract.ErrIOError, "Publish failed: "+err.Error(), nil)
	}
	return true, nil
}
