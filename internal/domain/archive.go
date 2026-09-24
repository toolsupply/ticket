package domain

import (
	"path"
	"sort"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/store"
)

// ArchiveResult describes a filesystem-only location change. TASK.md is not
// rewritten, so the move preserves the ticket's lifecycle state and history.
type ArchiveResult struct {
	ID       string `json:"id"`
	Archived bool   `json:"archived"`
	Changed  bool   `json:"changed"`
	FromPath string `json:"-"`
	ToPath   string `json:"-"`
}

type BatchArchiveResult struct {
	Items []ArchiveResult `json:"items"`
}

// EnsureActive rejects all ordinary mutations of an archived ticket.
func EnsureActive(st *store.Store, id string) error {
	loc, err := st.TicketLocation(id)
	if err != nil {
		return err
	}
	if loc.Archived() {
		return contract.NewError(contract.ErrArchived,
			"Archived tickets are read-only; unarchive the ticket before modifying it.", map[string]any{"id": id})
	}
	return nil
}

// Archive moves an unassigned terminal ticket into the archive namespace.
func Archive(st *store.Store, ref string) (*ArchiveResult, error) {
	id, err := mustResolve(st, ref)
	if err != nil {
		return nil, err
	}
	if err := EnsureActive(st, id); err != nil {
		return nil, err
	}
	ticket, err := ReadTicket(st, id)
	if err != nil {
		return nil, err
	}
	if !ticket.IsTerminal() {
		return nil, contract.NewError(contract.ErrInvalidTransition,
			"Only closed or rejected tickets can be archived.", map[string]any{"id": id, "state": ticket.State})
	}
	if ticket.Assignee != "" {
		return nil, contract.NewError(contract.ErrAlreadyClaimed,
			"Claimed tickets cannot be archived; release the ticket first.", map[string]any{"id": id})
	}
	if err := ValidateGraphs(st); err != nil {
		return nil, err
	}
	from := path.Join(id)
	to := path.Join(store.ArchiveDirName, id)
	if err := st.ArchiveTicket(id); err != nil {
		return nil, err
	}
	return &ArchiveResult{ID: id, Archived: true, Changed: true, FromPath: from, ToPath: to}, nil
}

// ArchiveMany resolves, deduplicates, sorts, and validates every target
// before moving the first ticket. Filesystem moves are individually atomic,
// but a later move can still fail for an external filesystem reason; the
// preflight prevents ordinary eligibility and graph errors from causing an
// avoidable partial batch.
func ArchiveMany(st *store.Store, refs []string) (*BatchArchiveResult, error) {
	resolved := make([]string, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		id, err := mustResolve(st, ref)
		if err != nil {
			return nil, err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		resolved = append(resolved, id)
	}
	sort.Strings(resolved)
	if len(resolved) == 0 {
		return &BatchArchiveResult{Items: []ArchiveResult{}}, nil
	}
	for _, id := range resolved {
		if err := EnsureActive(st, id); err != nil {
			return nil, err
		}
		ticket, err := ReadTicket(st, id)
		if err != nil {
			return nil, err
		}
		if !ticket.IsTerminal() {
			return nil, contract.NewError(contract.ErrInvalidTransition,
				"Only closed or rejected tickets can be archived.", map[string]any{"id": id, "state": ticket.State})
		}
		if ticket.Assignee != "" {
			return nil, contract.NewError(contract.ErrAlreadyClaimed,
				"Claimed tickets cannot be archived; release the ticket first.", map[string]any{"id": id})
		}
	}
	if err := ValidateGraphs(st); err != nil {
		return nil, err
	}
	result := &BatchArchiveResult{Items: make([]ArchiveResult, 0, len(resolved))}
	for _, id := range resolved {
		from := path.Join(id)
		to := path.Join(store.ArchiveDirName, id)
		if err := st.ArchiveTicket(id); err != nil {
			return nil, err
		}
		result.Items = append(result.Items, ArchiveResult{ID: id, Archived: true, Changed: true, FromPath: from, ToPath: to})
	}
	return result, nil
}

// Unarchive moves a ticket back into the active namespace without changing
// TASK.md or its lifecycle state.
func Unarchive(st *store.Store, ref string) (*ArchiveResult, error) {
	id, err := mustResolve(st, ref)
	if err != nil {
		return nil, err
	}
	loc, err := st.TicketLocation(id)
	if err != nil {
		return nil, err
	}
	if !loc.Archived() {
		return nil, contract.NewError(contract.ErrInvalidTransition,
			"Ticket is not archived.", map[string]any{"id": id})
	}
	if _, err := ReadTicket(st, id); err != nil {
		return nil, err
	}
	from := path.Join(store.ArchiveDirName, id)
	to := path.Join(id)
	if err := st.UnarchiveTicket(id); err != nil {
		return nil, err
	}
	return &ArchiveResult{ID: id, Archived: false, Changed: true, FromPath: from, ToPath: to}, nil
}
