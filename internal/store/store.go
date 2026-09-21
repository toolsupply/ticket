package store

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/identity"
)

// Store is a locked handle on one ticket repository.
// A Store holds one exclusive ticket-root lock for its lifetime.
type Store struct {
	Root string
	Cfg  Config
	Lock *Lock
	root *os.Root

	ids identity.IDSource
}

var (
	deleteRename        = os.Rename
	deleteRemoveAll     = os.RemoveAll
	closeRevalidateRoot = func(root *os.Root) { _ = root.Close() }
)

const maxDeletionStageAttempts = 16

// OpenOptions injects the ID source used for ticket creation tests.
type OpenOptions struct {
	IDSource identity.IDSource
	Root     string
}

func (o OpenOptions) withDefaults() OpenOptions {
	if o.IDSource == nil {
		o.IDSource = identity.RandomSource{}
	}
	return o
}

// Open resolves the root, loads the repository config, and acquires
// the exclusive ticket-root lock.
func Open(cwd string, o OpenOptions) (*Store, error) {
	o = o.withDefaults()
	root, err := DiscoverWithRoot(cwd, o.Root)
	if err != nil {
		return nil, err
	}
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return nil, wrapIO("ticket repository", err)
	}
	cfg, err := loadConfigRoot(rootHandle)
	if err != nil {
		rootHandle.Close()
		return nil, err
	}
	if err := ensureLocalRoot(rootHandle); err != nil {
		rootHandle.Close()
		return nil, err
	}
	lk, err := AcquireLockRoot(rootHandle, 5*time.Second)
	if err != nil {
		rootHandle.Close()
		return nil, err
	}
	return &Store{
		Root: root,
		Cfg:  cfg,
		Lock: lk,
		root: rootHandle,
		ids:  o.IDSource,
	}, nil
}

// Close releases the lock.
func (st *Store) Close() {
	if st == nil {
		return
	}
	if st.Lock != nil {
		st.Lock.Release()
	}
	if st.root != nil {
		st.root.Close()
		st.root = nil
	}
}

func (st *Store) openRoot() (*os.Root, bool, error) {
	if st.root != nil {
		return st.root, false, nil
	}
	r, err := os.OpenRoot(st.Root)
	return r, true, err
}

// RevalidateAfterSync verifies the repository's runtime state after an SCM
// operation that may have replaced files under the ticket root. If the live
// lock file changed, it acquires the replacement before releasing the old
// lock so callers never continue without a lock over the current path.
func (st *Store) RevalidateAfterSync() error {
	if st == nil || st.Lock == nil || st.Lock.file == nil {
		return contract.NewError(contract.ErrInvalidRepository,
			"Ticket repository lock is not active.", nil)
	}
	currentRoot, err := os.OpenRoot(st.Root)
	if err != nil {
		return wrapIO("ticket repository", err)
	}
	lockInfo, err := validateLiveLocalRoot(currentRoot)
	if err != nil {
		closeRevalidateRoot(currentRoot)
		return err
	}
	if heldInfo, statErr := st.Lock.file.Stat(); statErr != nil {
		closeRevalidateRoot(currentRoot)
		return contract.NewError(contract.ErrIOError,
			"Cannot inspect the held ticket-root lock: "+statErr.Error(), nil)
	} else if !os.SameFile(heldInfo, lockInfo) {
		next, acquireErr := AcquireLockRoot(currentRoot, 5*time.Second)
		if acquireErr != nil {
			closeRevalidateRoot(currentRoot)
			return acquireErr
		}
		previous := st.Lock
		st.Lock = next
		if releaseErr := previous.Release(); releaseErr != nil {
			_ = next.Release()
			st.Lock = previous
			closeRevalidateRoot(currentRoot)
			return contract.NewError(contract.ErrIOError,
				"Cannot release the stale ticket-root lock: "+releaseErr.Error(), nil)
		}
		if st.root != nil {
			st.root.Close()
		}
		st.root = currentRoot
	} else {
		closeRevalidateRoot(currentRoot)
	}
	cfg, err := loadConfigRoot(st.root)
	if err != nil {
		return err
	}
	st.Cfg = cfg
	return nil
}

func validateLiveLocal(root string) (os.FileInfo, error) {
	localPath := filepath.Join(root, ".local")
	info, err := os.Lstat(localPath)
	if os.IsNotExist(err) {
		return nil, contract.NewError(contract.ErrInvalidRepository,
			".local disappeared during SCM synchronization; refusing to continue.", nil)
	}
	if err != nil {
		return nil, contract.NewError(contract.ErrIOError,
			"Cannot inspect .local after SCM synchronization: "+err.Error(), nil)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, contract.NewError(contract.ErrInvalidRepository,
			".local must remain a real directory after SCM synchronization.", nil)
	}
	lockPath := filepath.Join(localPath, "lock")
	lockInfo, err := os.Lstat(lockPath)
	if os.IsNotExist(err) {
		return nil, contract.NewError(contract.ErrInvalidRepository,
			".local/lock disappeared during SCM synchronization; refusing to continue.", nil)
	}
	if err != nil {
		return nil, contract.NewError(contract.ErrIOError,
			"Cannot inspect .local/lock after SCM synchronization: "+err.Error(), nil)
	}
	if lockInfo.Mode()&os.ModeSymlink != 0 || !lockInfo.Mode().IsRegular() {
		return nil, contract.NewError(contract.ErrInvalidRepository,
			".local/lock must remain a regular file after SCM synchronization.", nil)
	}
	return lockInfo, nil
}

func validateLiveLocalRoot(root *os.Root) (os.FileInfo, error) {
	info, err := root.Lstat(".local")
	if os.IsNotExist(err) {
		return nil, contract.NewError(contract.ErrInvalidRepository,
			".local disappeared during SCM synchronization; refusing to continue.", nil)
	}
	if err != nil {
		return nil, contract.NewError(contract.ErrIOError,
			"Cannot inspect .local after SCM synchronization: "+err.Error(), nil)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, contract.NewError(contract.ErrInvalidRepository,
			".local must remain a real directory after SCM synchronization.", nil)
	}
	lockInfo, err := root.Lstat(filepath.Join(".local", "lock"))
	if os.IsNotExist(err) {
		return nil, contract.NewError(contract.ErrInvalidRepository,
			".local/lock disappeared during SCM synchronization; refusing to continue.", nil)
	}
	if err != nil {
		return nil, contract.NewError(contract.ErrIOError,
			"Cannot inspect .local/lock after SCM synchronization: "+err.Error(), nil)
	}
	if lockInfo.Mode()&os.ModeSymlink != 0 || !lockInfo.Mode().IsRegular() {
		return nil, contract.NewError(contract.ErrInvalidRepository,
			".local/lock must remain a regular file after SCM synchronization.", nil)
	}
	return lockInfo, nil
}

// NewID generates a persistent random object ID with the given prefix.
func (st *Store) NewID(prefix string) (string, error) {
	id, err := st.ids.ID(prefix)
	if err != nil {
		return "", contract.NewError(contract.ErrRandomnessUnavailable,
			"System entropy source unavailable.", nil)
	}
	return id, nil
}

func (st *Store) taskPath(id string) (string, error) {
	if _, _, ok := ParseID(id); !ok {
		return "", contract.NewError(contract.ErrInvalidArgument,
			"Reference is not a valid ticket ID.", map[string]any{"id": id})
	}
	return filepath.Join(st.Root, id, "TASK.md"), nil
}

func (st *Store) taskName(id string) (string, error) {
	if _, _, ok := ParseID(id); !ok {
		return "", contract.NewError(contract.ErrInvalidArgument,
			"Reference is not a valid ticket ID.", map[string]any{"id": id})
	}
	return filepath.Join(id, "TASK.md"), nil
}

// ReadTask validates and reads a managed TASK.md through Store's stable root.
func (st *Store) ReadTask(id string, limit int64) ([]byte, os.FileInfo, error) {
	name, err := st.taskName(id)
	if err != nil {
		return nil, nil, err
	}
	r, ephemeral, err := st.openRoot()
	if err != nil {
		return nil, nil, wrapIO("ticket repository", err)
	}
	if ephemeral {
		defer r.Close()
	}
	info, err := r.Lstat(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, contract.NewError(contract.ErrNotFound, "TASK.md not found.", map[string]any{"id": id})
		}
		return nil, nil, contract.NewError(contract.ErrIOError, "TASK.md lookup failed: "+err.Error(), map[string]any{"id": id})
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, nil, contract.NewError(contract.ErrInvalidRepository, "TASK.md must be a regular file and must not be a symlink.", map[string]any{"id": id})
	}
	beforeManagedOpen()
	f, err := openRootNoFollow(r, name, os.O_RDONLY, 0)
	if err != nil {
		return nil, info, err
	}
	openedInfo, statErr := f.Stat()
	if statErr != nil {
		f.Close()
		return nil, info, statErr
	}
	if !os.SameFile(info, openedInfo) {
		f.Close()
		return nil, info, fmt.Errorf("TASK.md changed during open")
	}
	data, err := readBoundedOpenFile(f, limit)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, info, err
	}
	return data, info, nil
}

func (st *Store) RootEntries() ([]fs.DirEntry, error) {
	if st.root == nil {
		return os.ReadDir(st.Root)
	}
	return fs.ReadDir(st.root.FS(), ".")
}

func (st *Store) TicketDirInfo(id string) (os.FileInfo, error) {
	r, ephemeral, err := st.openRoot()
	if err != nil {
		return nil, wrapIO("ticket repository", err)
	}
	if ephemeral {
		defer r.Close()
	}
	return r.Lstat(id)
}

// TicketExists reports whether a canonical ticket directory already occupies
// the candidate ID. It is used to avoid validating an occupied ID as a new
// graph node before the publication collision check.
func (st *Store) TicketExists(id string) (bool, error) {
	if _, _, ok := ParseID(id); !ok {
		return false, contract.NewError(contract.ErrInvalidArgument,
			"Reference is not a valid ticket ID.", map[string]any{"id": id})
	}
	r, ephemeral, err := st.openRoot()
	if err != nil {
		return false, wrapIO("ticket repository", err)
	}
	if ephemeral {
		defer r.Close()
	}
	_, err = r.Lstat(id)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, contract.NewError(contract.ErrIOError, "Ticket lookup failed: "+err.Error(), map[string]any{"id": id})
}

// DeleteTicket removes one canonical ticket directory. The final path is
// checked with Lstat so a symlink or non-directory can never be followed as
// a ticket target. The directory is first renamed out of the canonical
// namespace; cleanup of the staged directory is a separate best-effort step.
func (st *Store) DeleteTicket(id string) error {
	if _, _, ok := ParseID(id); !ok {
		return contract.NewError(contract.ErrInvalidArgument,
			"Reference is not a valid ticket ID.", map[string]any{"id": id})
	}
	if st.root == nil {
		return st.deleteTicketPath(id)
	}
	r, ephemeral, err := st.openRoot()
	if err != nil {
		return wrapIO("ticket repository", err)
	}
	if ephemeral {
		defer r.Close()
	}
	info, err := r.Lstat(id)
	if err != nil {
		if os.IsNotExist(err) {
			return contract.NewError(contract.ErrNotFound,
				"Ticket not found.", map[string]any{"id": id})
		}
		return contract.NewError(contract.ErrIOError,
			"Cannot inspect ticket: "+err.Error(), map[string]any{"id": id})
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return contract.NewError(contract.ErrInvalidRepository,
			"Ticket target must be a real directory.", map[string]any{"id": id})
	}
	stage, err := temporaryName(id)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < maxDeletionStageAttempts; attempt++ {
		if _, err := r.Lstat(filepath.Join(".local", stage)); os.IsNotExist(err) {
			break
		} else if err != nil {
			return contract.NewError(contract.ErrIOError,
				"Cannot inspect deletion staging path: "+err.Error(), map[string]any{"id": id})
		}
		stage, err = temporaryName(id)
		if err != nil {
			return err
		}
		if attempt == maxDeletionStageAttempts-1 {
			return contract.NewError(contract.ErrInternalError,
				"Deletion staging collisions exceeded the retry budget.", map[string]any{"id": id})
		}
	}
	if err := r.Rename(id, filepath.Join(".local", stage)); err != nil {
		return contract.NewError(contract.ErrIOError,
			"Cannot stage ticket for deletion: "+err.Error(), map[string]any{"id": id})
	}
	if err := r.RemoveAll(filepath.Join(".local", stage)); err != nil {
		return contract.NewError(contract.ErrIOError,
			"Ticket was removed from the repository, but staged cleanup is incomplete: "+err.Error(),
			map[string]any{"id": id, "removal_applied": true})
	}
	return nil
}

func (st *Store) deleteTicketPath(id string) error {
	path := filepath.Join(st.Root, id)
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return contract.NewError(contract.ErrNotFound,
				"Ticket not found.", map[string]any{"id": id})
		}
		return contract.NewError(contract.ErrIOError,
			"Cannot inspect ticket: "+err.Error(), map[string]any{"id": id})
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return contract.NewError(contract.ErrInvalidRepository,
			"Ticket target must be a real directory.", map[string]any{"id": id})
	}
	stage, err := temporaryPath(filepath.Join(st.Root, ".local"), id)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < maxDeletionStageAttempts; attempt++ {
		if _, err := os.Lstat(stage); os.IsNotExist(err) {
			break
		} else if err != nil {
			return contract.NewError(contract.ErrIOError,
				"Cannot inspect deletion staging path: "+err.Error(), map[string]any{"id": id})
		}
		stage, err = temporaryPath(filepath.Join(st.Root, ".local"), id)
		if err != nil {
			return err
		}
		if attempt == maxDeletionStageAttempts-1 {
			return contract.NewError(contract.ErrInternalError,
				"Deletion staging collisions exceeded the retry budget.", map[string]any{"id": id})
		}
	}
	if err := deleteRename(path, stage); err != nil {
		return contract.NewError(contract.ErrIOError,
			"Cannot stage ticket for deletion: "+err.Error(), map[string]any{"id": id})
	}
	if err := deleteRemoveAll(stage); err != nil {
		return contract.NewError(contract.ErrIOError,
			"Ticket was removed from the repository, but staged cleanup is incomplete: "+err.Error(),
			map[string]any{"id": id, "removal_applied": true})
	}
	return nil
}

// TaskModTime returns the filesystem modification time of one managed
// TASK.md. It preserves the same path and symlink checks as ticket reads.
func (st *Store) TaskModTime(id string) (time.Time, error) {
	if st.root != nil {
		name, err := st.taskName(id)
		if err != nil {
			return time.Time{}, err
		}
		info, err := st.root.Stat(name)
		if err != nil {
			return time.Time{}, wrapIO("TASK.md", err)
		}
		return info.ModTime(), nil
	}
	path, err := st.taskPath(id)
	if err != nil {
		return time.Time{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, wrapIO("TASK.md", err)
	}
	return info.ModTime(), nil
}

// InitRoot creates (or idempotently accepts) a ticket repository at root.
// The CLI calls it only for the discovered current-project tickets path.
// init is the one command that needs no existing repository and
// no actor. It creates only manager-owned paths:
// config.json, .gitignore, README.md, and .local/.
//
// An existing valid same-format repository returns created=false and
// is left untouched. A non-empty non-repository target and an unknown
// format are failures, never adoption.
func InitRoot(root string) (created bool, err error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return false, contract.NewError(contract.ErrInvalidArgument,
			"Cannot resolve init path.", nil)
	}
	if info, statErr := os.Lstat(abs); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return false, contract.NewError(contract.ErrInvalidRepository,
				"Ticket repository root must be a real directory.", nil)
		}
	} else if !os.IsNotExist(statErr) {
		return false, contract.NewError(contract.ErrIOError,
			"Cannot inspect init path: "+statErr.Error(), nil)
	}
	if _, cerr := LoadConfig(abs); cerr == nil {
		if err := ensureLocalDir(filepath.Join(abs, ".local")); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := checkInitTarget(abs); err != nil {
		return false, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return false, contract.NewError(contract.ErrIOError,
			"Cannot create repository directory: "+err.Error(), nil)
	}
	if err := ensureLocalDir(filepath.Join(abs, ".local")); err != nil {
		return false, err
	}
	lk, err := AcquireLock(filepath.Join(abs, ".local"), 5*time.Second)
	if err != nil {
		return false, err
	}
	defer lk.Release()
	// Re-check under the lock: a concurrent init may have published
	// while we waited.
	if _, cerr := LoadConfig(abs); cerr == nil {
		return false, nil
	}
	if err := checkInitTarget(abs); err != nil {
		return false, err
	}
	files := []struct {
		name string
		data []byte
	}{
		{".gitignore", []byte(".local/\n")},
		{"README.md", initREADME()},
		{"config.json", writeConfig()},
	}
	for _, f := range files {
		if err := writeFileAtomic(filepath.Join(abs, f.name), f.data); err != nil {
			return false, contract.NewError(contract.ErrIOError,
				"Cannot write "+f.name+": "+err.Error(), nil)
		}
	}
	return true, nil
}

// checkInitTarget rejects a non-empty non-repository target and a
// target that is a file (fail rather than adopt arbitrary
// contents).
func checkInitTarget(root string) error {
	st, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return contract.NewError(contract.ErrIOError,
			"Cannot inspect init target: "+err.Error(), nil)
	}
	if !st.IsDir() {
		return contract.NewError(contract.ErrInvalidArgument,
			"Init target path is a file, not a directory.", nil)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return contract.NewError(contract.ErrIOError,
			"Cannot read init target: "+err.Error(), nil)
	}
	// A missing config marks an interrupted initialization. Resume only from
	// exact regular copies of files that init itself owns.
	for _, e := range entries {
		if e.Name() == ".local" {
			continue
		}
		var expected []byte
		switch e.Name() {
		case ".gitignore":
			expected = []byte(".local/\n")
		case "README.md":
			expected = initREADME()
		default:
			return contract.NewError(contract.ErrInvalidRepository,
				"Init target is not empty; refusing to adopt arbitrary contents.", nil)
		}
		path := filepath.Join(root, e.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return contract.NewError(contract.ErrIOError,
				"Cannot inspect init file: "+err.Error(), nil)
		}
		if !info.Mode().IsRegular() {
			return contract.NewError(contract.ErrInvalidRepository,
				"Init target contains a non-regular manager file.", nil)
		}
		data, err := ReadBoundedFile(path, 128<<10)
		if err != nil {
			if errors.Is(err, ErrReadLimit) {
				return contract.NewError(contract.ErrFileTooLarge, "Init metadata exceeds the 128 KiB limit.", nil)
			}
			return contract.NewError(contract.ErrIOError,
				"Cannot read init file: "+err.Error(), nil)
		}
		if !bytes.Equal(data, expected) {
			return contract.NewError(contract.ErrInvalidRepository,
				"Init target contains a modified manager file; refusing to overwrite it.", nil)
		}
	}
	if err := ensureLocalDir(filepath.Join(root, ".local")); err != nil {
		return err
	}
	return nil
}

func ensureLocalDir(localDir string) error {
	info, err := os.Lstat(localDir)
	if os.IsNotExist(err) {
		if err := os.Mkdir(localDir, 0o755); err != nil {
			return contract.NewError(contract.ErrIOError, "Cannot prepare .local: "+err.Error(), nil)
		}
		return nil
	}
	if err != nil {
		return contract.NewError(contract.ErrIOError, "Cannot inspect .local: "+err.Error(), nil)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return contract.NewError(contract.ErrInvalidRepository,
			".local must be a real directory and must not be a symlink.", nil)
	}
	return nil
}

func ensureLocalRoot(root *os.Root) error {
	info, err := root.Lstat(".local")
	if os.IsNotExist(err) {
		if err := root.Mkdir(".local", 0o755); err != nil {
			return contract.NewError(contract.ErrIOError, "Cannot prepare .local: "+err.Error(), nil)
		}
		return nil
	}
	if err != nil {
		return contract.NewError(contract.ErrIOError, "Cannot inspect .local: "+err.Error(), nil)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return contract.NewError(contract.ErrInvalidRepository,
			".local must be a real directory and must not be a symlink.", nil)
	}
	return nil
}

func initREADME() []byte {
	return []byte(`# Tickets

Managed ticket repository (format version 1).

- One directory per ticket, named by the full ticket ID.
- Each ticket's state lives in its TASK.md; the first H1 is the title.
- .local/ holds the advisory lock; do not edit it.

Use the ticket CLI for all state changes and publication.

Optional source-control synchronization is controlled by TICKET_SCM and
TICKET_SCM_MODE in the process environment. It is disabled unless configured.
`)
}

// ParseID splits a full ticket ID into prefix and encoded body, validating
// canonical form: a UTC date and five decimal digits.
// Anything else - traversal-shaped paths, wrong length, malformed dates,
// wrong prefix - is rejected
// before any managed path is built.
func ParseID(id string) (prefix, encoded string, ok bool) {
	if !identity.ValidID(id) {
		return "", "", false
	}
	return identity.PrefixOf(id)
}

// ResolveID resolves a full or shorthand ticket ID to a full ID.
// requireExists enforces that the referenced ticket directory exists
// (references) as opposed to mere prefix disambiguation.
func (st *Store) ResolveID(id string, requireExists bool) (string, error) {
	_, _, ok := ParseID(id)
	if !ok {
		if identity.ValidShorthand(id) {
			return st.resolveShorthand(id, requireExists)
		}
		return "", contract.NewError(contract.ErrInvalidArgument,
			"Reference is not a valid ticket ID.", map[string]any{"id": id})
	}
	if requireExists {
		r, ephemeral, err := st.openRoot()
		if err != nil {
			return "", wrapIO("ticket repository", err)
		}
		if ephemeral {
			defer r.Close()
		}
		le, err := r.Lstat(id)
		if err != nil || !le.IsDir() {
			return "", dangling(id)
		}
	} else {
		r, ephemeral, err := st.openRoot()
		if err != nil {
			return "", wrapIO("ticket repository", err)
		}
		if ephemeral {
			defer r.Close()
		}
		le, err := r.Lstat(id)
		if err != nil || !le.IsDir() {
			return "", contract.NewError(contract.ErrNotFound,
				"Ticket not found.", map[string]any{"id": id})
		}
	}
	return id, nil
}

// resolveShorthand matches a unique existing ticket ID by encoded
// prefix.
func (st *Store) resolveShorthand(prefix string, requireExists bool) (string, error) {
	matches, err := st.listTicketDirs()
	if err != nil {
		return "", err
	}
	var hits []string
	for _, full := range matches {
		if strings.HasPrefix(full, prefix) {
			hits = append(hits, full)
		}
	}
	switch len(hits) {
	case 0:
		return "", dangling(prefix)
	case 1:
		return hits[0], nil
	default:
		return "", contract.NewError(contract.ErrAmbiguousID,
			"ID shorthand is ambiguous.", map[string]any{"id": prefix, "matches": len(hits)})
	}
}

func dangling(id string) error {
	return contract.NewError(contract.ErrDanglingReference,
		"Referenced ticket does not exist.", map[string]any{"id": id})
}

// listTicketDirs returns all existing ticket IDs (directory names) in
// the repository, in lexicographic order.
func (st *Store) listTicketDirs() ([]string, error) {
	var entries []fs.DirEntry
	var err error
	if st.root != nil {
		entries, err = fs.ReadDir(st.root.FS(), ".")
	} else {
		entries, err = os.ReadDir(st.Root)
	}
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, wrapIO("ticket root", err)
	}
	var out []string
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
		if !e.IsDir() {
			continue
		}
		if !identity.ValidID(e.Name()) {
			continue
		}
		out = append(out, e.Name())
	}
	return out, nil
}
