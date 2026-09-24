package store

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/toolsupply/ticket/internal/contract"
)

// ArchiveDirName is the visible repository directory containing archived
// tickets. It is deliberately not hidden: archive location is part of the
// repository's durable filesystem model.
const ArchiveDirName = "archive"

// TicketLocation identifies the authoritative namespace containing a ticket.
type TicketLocation uint8

const (
	LocationActive TicketLocation = iota
	LocationArchived
)

func (l TicketLocation) Archived() bool { return l == LocationArchived }

// TicketLocation resolves the one authoritative active/archive location for a
// canonical ticket ID. Duplicate IDs and unsafe archive structures are
// repository errors, never an arbitrary choice between two files.
func (st *Store) TicketLocation(id string) (TicketLocation, error) {
	if _, _, ok := ParseID(id); !ok {
		return LocationActive, contract.NewError(contract.ErrInvalidArgument,
			"Reference is not a valid ticket ID.", map[string]any{"id": id})
	}
	r, ephemeral, err := st.openRoot()
	if err != nil {
		return LocationActive, wrapIO("ticket repository", err)
	}
	if ephemeral {
		defer r.Close()
	}
	if err := validateArchiveRoot(r); err != nil {
		return LocationActive, err
	}
	_, activeExists, err := ticketDirState(r, id)
	if err != nil {
		return LocationActive, err
	}
	_, archivedExists, err := ticketDirState(r, filepath.Join(ArchiveDirName, id))
	if err != nil {
		return LocationActive, err
	}
	if activeExists && archivedExists {
		return LocationActive, contract.NewError(contract.ErrInvalidRepository,
			"Ticket ID exists in both active and archive namespaces.", map[string]any{"id": id})
	}
	if activeExists {
		return LocationActive, nil
	}
	if archivedExists {
		return LocationArchived, nil
	}
	return LocationActive, contract.NewError(contract.ErrNotFound,
		"Ticket not found.", map[string]any{"id": id})
}

func ticketDirState(r *os.Root, name string) (os.FileInfo, bool, error) {
	info, err := r.Lstat(name)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, contract.NewError(contract.ErrIOError,
			"Ticket lookup failed: "+err.Error(), map[string]any{"path": name})
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, false, contract.NewError(contract.ErrInvalidRepository,
			"Ticket target must be a real directory.", map[string]any{"path": name})
	}
	return info, true, nil
}

func validateArchiveRoot(r *os.Root) error {
	info, err := r.Lstat(ArchiveDirName)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return contract.NewError(contract.ErrIOError,
			"Cannot inspect archive directory: "+err.Error(), nil)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return contract.NewError(contract.ErrInvalidRepository,
			"archive must be a real directory and must not be a symlink.", nil)
	}
	return nil
}

func ensureArchiveRoot(r *os.Root) error {
	if err := validateArchiveRoot(r); err == nil {
		if _, err := r.Lstat(ArchiveDirName); err == nil {
			return nil
		}
	} else {
		return err
	}
	if err := r.Mkdir(ArchiveDirName, 0o755); err != nil {
		return contract.NewError(contract.ErrIOError,
			"Cannot create archive directory: "+err.Error(), nil)
	}
	return nil
}

// TaskRelPath returns the repository-relative managed path for a ticket.
func (st *Store) TaskRelPath(id string) (string, error) {
	loc, err := st.TicketLocation(id)
	if err != nil {
		return "", err
	}
	if loc.Archived() {
		return path.Join(ArchiveDirName, id, "TASK.md"), nil
	}
	return path.Join(id, "TASK.md"), nil
}

// TicketDirRelPath returns the repository-relative ticket directory path.
func (st *Store) TicketDirRelPath(id string) (string, error) {
	taskPath, err := st.TaskRelPath(id)
	if err != nil {
		return "", err
	}
	return path.Dir(taskPath), nil
}

// ArchiveEntries lists direct archive children. It does not parse or read
// every ticket; normal active scans use direct archive lookup only for
// referenced IDs.
func (st *Store) ArchiveEntries() ([]fs.DirEntry, error) {
	r, ephemeral, err := st.openRoot()
	if err != nil {
		return nil, wrapIO("ticket repository", err)
	}
	if ephemeral {
		defer r.Close()
	}
	if err := validateArchiveRoot(r); err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(r.FS(), ArchiveDirName)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, wrapIO("archive", err)
	}
	return entries, nil
}

// ArchiveTicket atomically moves an active ticket into archive/ while the
// caller's ticket-root lock is held.
func (st *Store) ArchiveTicket(id string) error {
	loc, err := st.TicketLocation(id)
	if err != nil {
		return err
	}
	if loc.Archived() {
		return contract.NewError(contract.ErrArchived, "Ticket is already archived.", map[string]any{"id": id})
	}
	return st.moveTicket(id, filepath.Join(ArchiveDirName, id))
}

// UnarchiveTicket atomically moves an archived ticket back into the active
// namespace while the caller's ticket-root lock is held.
func (st *Store) UnarchiveTicket(id string) error {
	loc, err := st.TicketLocation(id)
	if err != nil {
		return err
	}
	if !loc.Archived() {
		return contract.NewError(contract.ErrInvalidTransition, "Ticket is not archived.", map[string]any{"id": id})
	}
	return st.moveTicket(filepath.Join(ArchiveDirName, id), id)
}

func (st *Store) moveTicket(source, destination string) error {
	r, ephemeral, err := st.openRoot()
	if err != nil {
		return wrapIO("ticket repository", err)
	}
	if ephemeral {
		defer r.Close()
	}
	if filepath.Dir(destination) == ArchiveDirName {
		if err := ensureArchiveRoot(r); err != nil {
			return err
		}
	}
	if _, exists, err := ticketDirState(r, source); err != nil {
		return err
	} else if !exists {
		return contract.NewError(contract.ErrNotFound,
			"Ticket not found.", map[string]any{"path": source})
	}
	if _, err := r.Lstat(destination); err == nil {
		return contract.NewError(contract.ErrConflict,
			"Archive destination already exists.", map[string]any{"path": destination})
	} else if !os.IsNotExist(err) {
		return contract.NewError(contract.ErrIOError,
			"Cannot inspect archive destination: "+err.Error(), map[string]any{"path": destination})
	}
	if err := r.Rename(source, destination); err != nil {
		return contract.NewError(contract.ErrIOError,
			"Cannot move ticket: "+err.Error(), map[string]any{"from": source, "to": destination})
	}
	return nil
}
