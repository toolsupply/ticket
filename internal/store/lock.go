package store

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/toolsupply/ticket/internal/contract"
)

// lock.go: bounded advisory locking over the ticket root's .local/lock
// file. v1 uses a single exclusive lock for reads and writes. The lock file is never
// deleted while live; the OS releases it when the process exits.
//
// Platform primitives are implemented per OS (unix flock / win32
// LockFileEx); they are never replaced by a persistent
// lockfile-exists test.

var errLockTimeout = fmt.Errorf("lock timeout")

const lockPollInterval = 10 * time.Millisecond

// Lock is an acquired advisory lock on a ticket repository.
type Lock struct {
	path string
	file *os.File
}

// AcquireLock acquires the exclusive ticket-root lock within timeout.
// The containing directory must exist; it is created by the caller.
func AcquireLock(localDir string, timeout time.Duration) (*Lock, error) {
	path := filepath.Join(localDir, "lock")
	if info, statErr := os.Lstat(path); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, contract.NewError(contract.ErrInvalidRepository,
				".local/lock must be a regular file and must not be a symlink.", nil)
		}
	} else if !os.IsNotExist(statErr) {
		return nil, contract.NewError(contract.ErrIOError,
			"Cannot inspect ticket-root lock file: "+statErr.Error(), nil)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, contract.NewError(contract.ErrIOError,
			"Cannot open ticket-root lock file: "+err.Error(), nil)
	}
	l := &Lock{path: path, file: f}
	if err := acquire(l.file, timeout); err != nil {
		f.Close()
		if err == errLockTimeout {
			return nil, contract.NewError(contract.ErrLockTimeout,
				"Could not acquire the ticket-root lock within the bounded wait.",
				map[string]any{"timeout": secondsJSON(timeout)})
		}
		return nil, contract.NewError(contract.ErrIOError,
			"Lock acquisition failed: "+err.Error(), nil)
	}
	return l, nil
}

// Release unlocks and closes the lock file (the file itself stays).
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := release(l.file)
	l.file.Close()
	l.file = nil
	return err
}

func secondsJSON(d time.Duration) float64 {
	return float64(d) / float64(time.Second)
}
