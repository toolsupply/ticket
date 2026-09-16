package store

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"ticket/internal/contract"
	"ticket/internal/identity"
)

// Store is a locked handle on one ticket repository.
// A Store holds one exclusive ticket-root lock for its lifetime.
type Store struct {
	Root string
	Cfg  Config
	Lock *Lock

	ids identity.IDSource
}

// OpenOptions injects the ID source used for ticket creation tests.
type OpenOptions struct {
	IDSource identity.IDSource
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
	root, err := Discover(cwd)
	if err != nil {
		return nil, err
	}
	cfg, err := LoadConfig(root)
	if err != nil {
		return nil, err
	}
	localDir := filepath.Join(root, ".local")
	if err := ensureLocalDir(localDir); err != nil {
		return nil, err
	}
	lk, err := AcquireLock(localDir, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &Store{
		Root: root,
		Cfg:  cfg,
		Lock: lk,
		ids:  o.IDSource,
	}, nil
}

// Close releases the lock.
func (st *Store) Close() {
	if st == nil || st.Lock == nil {
		return
	}
	st.Lock.Release()
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

// TicketExists reports whether a canonical ticket directory already occupies
// the candidate ID. It is used to avoid validating an occupied ID as a new
// graph node before the publication collision check.
func (st *Store) TicketExists(id string) (bool, error) {
	if _, _, ok := ParseID(id); !ok {
		return false, contract.NewError(contract.ErrInvalidArgument,
			"Reference is not a valid ticket ID.", map[string]any{"id": id})
	}
	_, err := os.Lstat(filepath.Join(st.Root, id))
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
// a ticket target.
func (st *Store) DeleteTicket(id string) error {
	if _, _, ok := ParseID(id); !ok {
		return contract.NewError(contract.ErrInvalidArgument,
			"Reference is not a valid ticket ID.", map[string]any{"id": id})
	}
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
	if err := os.RemoveAll(path); err != nil {
		return contract.NewError(contract.ErrIOError,
			"Cannot delete ticket: "+err.Error(), map[string]any{"id": id})
	}
	return nil
}

// TaskModTime returns the filesystem modification time of one managed
// TASK.md. It preserves the same path and symlink checks as ticket reads.
func (st *Store) TaskModTime(id string) (time.Time, error) {
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
		{"config.json", writeConfig()},
		{".gitignore", []byte(".local/\n")},
		{"README.md", initREADME()},
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
	// Only the manager-owned .local directory may pre-exist.
	for _, e := range entries {
		if e.Name() != ".local" {
			return contract.NewError(contract.ErrInvalidRepository,
				"Init target is not empty; refusing to adopt arbitrary contents.", nil)
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
		dir := filepath.Join(st.Root, id)
		le, err := os.Lstat(dir)
		if err != nil || !le.IsDir() {
			return "", dangling(id)
		}
	} else {
		dir := filepath.Join(st.Root, id)
		le, err := os.Lstat(dir)
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
	entries, err := os.ReadDir(st.Root)
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
