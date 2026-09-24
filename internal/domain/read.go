package domain

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/store"
)

// ReadTicket reads and parses one managed ticket, preserving its relative
// storage path and archive namespace.
func ReadTicket(st *store.Store, id string) (*Ticket, error) {
	data, err := readTaskFile(st, id)
	if err != nil {
		return nil, err
	}
	t, err := ParseTicketFile(id, data)
	if err != nil {
		return nil, err
	}
	path, err := st.TaskRelPath(id)
	if err != nil {
		return nil, err
	}
	t.TaskRelPath = path
	t.Archived = strings.HasPrefix(path, store.ArchiveDirName+"/")
	return t, nil
}

func taskPath(st *store.Store, id string) (string, error) {
	if _, _, ok := store.ParseID(id); !ok {
		return "", contract.NewError(contract.ErrInvalidArgument, "Reference is not a valid ticket ID.", map[string]any{"id": id})
	}
	rel, err := st.TaskRelPath(id)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(st.Root, filepath.Dir(filepath.FromSlash(rel)))
	info, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", contract.NewError(contract.ErrNotFound, "Ticket not found.", map[string]any{"id": id})
		}
		return "", contract.NewError(contract.ErrIOError, "Ticket lookup failed: "+err.Error(), map[string]any{"id": id})
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", contract.NewError(contract.ErrInvalidRepository, "Ticket directory must not be a symlink.", map[string]any{"id": id})
	}
	if !info.IsDir() {
		return "", contract.NewError(contract.ErrInvalidRepository, "Ticket path is not a directory.", map[string]any{"id": id})
	}
	task := filepath.Join(dir, "TASK.md")
	taskInfo, err := os.Lstat(task)
	if err != nil {
		if os.IsNotExist(err) {
			return "", contract.NewError(contract.ErrInvalidRepository, "TASK.md is missing.", map[string]any{"id": id})
		}
		return "", contract.NewError(contract.ErrIOError, "TASK.md lookup failed: "+err.Error(), map[string]any{"id": id})
	}
	if taskInfo.Mode()&os.ModeSymlink != 0 {
		return "", contract.NewError(contract.ErrInvalidRepository, "TASK.md must not be a symlink.", map[string]any{"id": id})
	}
	if !taskInfo.Mode().IsRegular() {
		return "", contract.NewError(contract.ErrInvalidRepository, "TASK.md is not a regular file.", map[string]any{"id": id})
	}
	return task, nil
}

func readTaskFile(st *store.Store, id string) ([]byte, error) {
	data, info, rootErr := st.ReadTask(id, int64(TaskMaxBytes))
	if rootErr == nil {
		return data, nil
	}
	if info != nil || rootErr != nil {
		if errors.Is(rootErr, store.ErrReadLimit) {
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			return nil, contract.NewError(contract.ErrFileTooLarge,
				"TASK.md exceeds the 1 MiB managed-file limit.",
				map[string]any{"path": id + "/TASK.md", "size": size})
		}
		if ce, ok := rootErr.(*contract.Error); ok {
			if ce.Code == contract.ErrFileTooLarge {
				size := int64(0)
				if info != nil {
					size = info.Size()
				}
				return nil, contract.NewError(contract.ErrFileTooLarge,
					"TASK.md exceeds the 1 MiB managed-file limit.",
					map[string]any{"path": id + "/TASK.md", "size": size})
			}
			if ce.Code != contract.ErrNotFound {
				return nil, rootErr
			}
		}
		return nil, rootErr
	}
	task, err := taskPath(st, id)
	if err != nil {
		return nil, err
	}
	info, err = os.Lstat(task)
	if err != nil {
		return nil, contract.NewError(contract.ErrIOError, "TASK.md lookup failed: "+err.Error(), map[string]any{"id": id})
	}
	file, err := os.Open(task)
	if err != nil {
		return nil, contract.NewError(contract.ErrIOError, "TASK.md read failed: "+err.Error(), map[string]any{"id": id})
	}
	data, readErr := io.ReadAll(io.LimitReader(file, int64(TaskMaxBytes)+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, contract.NewError(contract.ErrIOError, "TASK.md read failed: "+readErr.Error(), map[string]any{"id": id})
	}
	if closeErr != nil {
		return nil, contract.NewError(contract.ErrIOError, "TASK.md close failed: "+closeErr.Error(), map[string]any{"id": id})
	}
	if len(data) > TaskMaxBytes {
		return nil, contract.NewError(contract.ErrFileTooLarge,
			"TASK.md exceeds the 1 MiB managed-file limit.",
			map[string]any{"path": id + "/TASK.md", "size": info.Size()})
	}
	return data, nil
}

func wrapStoreError(what string, err error) error {
	if os.IsNotExist(err) {
		return contract.NewError(contract.ErrRepoNotFound, what+" not found.", nil)
	}
	return contract.NewError(contract.ErrIOError, what+": "+err.Error(), nil)
}
