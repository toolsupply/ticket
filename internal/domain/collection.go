package domain

import (
	"os"
	"path"
	"sort"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/identity"
	"github.com/toolsupply/ticket/internal/store"
)

func sortTickets(tickets []*Ticket) {
	sort.Slice(tickets, func(i, j int) bool {
		a, b := tickets[i], tickets[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.ID < b.ID
	})
}

func scanWith(st *store.Store) ([]*Ticket, []DiagnosticSummary, error) {
	entries, err := st.RootEntries()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, wrapStoreError("ticket root", err)
	}
	var tickets []*Ticket
	var diags []DiagnosticSummary
	for _, entry := range entries {
		name := entry.Name()
		if name == ".local" || name == store.ArchiveDirName || entry.Type().IsRegular() {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			diags = append(diags, DiagnosticSummary{Path: name, Message: "ticket directory must not be a symlink"})
			continue
		}
		if !entry.IsDir() {
			continue
		}
		if !identity.ValidID(name) && looksLikeTicketID(name) {
			diags = append(diags, DiagnosticSummary{Path: name, Message: "ticket directory name is not a canonical ticket ID"})
			continue
		}
		if !identity.ValidID(name) {
			continue
		}
		info, err := st.TicketDirInfo(name)
		if err != nil {
			if ce, ok := err.(*contract.Error); ok && ce.Code == contract.ErrInvalidRepository {
				return nil, nil, err
			}
			diags = append(diags, DiagnosticSummary{Path: name, Message: "ticket path is not a directory"})
			continue
		}
		if !info.IsDir() {
			diags = append(diags, DiagnosticSummary{Path: name, Message: "ticket path is not a directory"})
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			diags = append(diags, DiagnosticSummary{Path: name, Message: "ticket directory must not be a symlink"})
			continue
		}
		ticket, err := ReadTicket(st, name)
		if err != nil {
			if ce, ok := err.(*contract.Error); ok {
				if ce.Code == contract.ErrInvalidRepository {
					return nil, nil, err
				}
				d := DiagnosticSummary{Path: name, Message: ce.Message}
				if ce.Details != nil {
					if values, ok := ce.Details["diagnostics"].([]map[string]any); ok {
						d.Diagnostics = values
					}
				}
				diags = append(diags, d)
			} else {
				diags = append(diags, DiagnosticSummary{Path: name, Message: err.Error()})
			}
			continue
		}
		tickets = append(tickets, ticket)
	}
	return tickets, diags, nil
}

func scanArchivedWith(st *store.Store) ([]*Ticket, []DiagnosticSummary, error) {
	entries, err := st.ArchiveEntries()
	if err != nil {
		if _, ok := err.(*contract.Error); ok {
			return nil, nil, err
		}
		return nil, nil, wrapStoreError("archive", err)
	}
	var tickets []*Ticket
	var diags []DiagnosticSummary
	for _, entry := range entries {
		name := entry.Name()
		archivePath := path.Join(store.ArchiveDirName, name)
		if entry.Type()&os.ModeSymlink != 0 {
			diags = append(diags, DiagnosticSummary{Path: archivePath, Message: "ticket directory must not be a symlink"})
			continue
		}
		if !entry.IsDir() {
			if identity.ValidID(name) || looksLikeTicketID(name) {
				diags = append(diags, DiagnosticSummary{Path: archivePath, Message: "ticket archive entry must be a directory"})
			}
			continue
		}
		if !identity.ValidID(name) && looksLikeTicketID(name) {
			diags = append(diags, DiagnosticSummary{Path: archivePath, Message: "ticket directory name is not a canonical ticket ID"})
			continue
		}
		if !identity.ValidID(name) {
			continue
		}
		ticket, err := ReadTicket(st, name)
		if err != nil {
			if ce, ok := err.(*contract.Error); ok && ce.Code == contract.ErrInvalidRepository {
				return nil, nil, err
			}
			diags = append(diags, DiagnosticSummary{Path: archivePath, Message: err.Error()})
			continue
		}
		if !ticket.Archived {
			diags = append(diags, DiagnosticSummary{Path: archivePath, Message: "archive ticket resolved outside archive namespace"})
			continue
		}
		tickets = append(tickets, ticket)
	}
	return tickets, diags, nil
}

func looksLikeTicketID(name string) bool {
	if len(name) < 9 {
		return false
	}
	for i := 0; i < 8; i++ {
		if name[i] < '0' || name[i] > '9' {
			return false
		}
	}
	return name[8] == '-'
}

type DiagnosticSummary struct {
	Path        string
	Message     string
	Diagnostics []map[string]any
}

func collectionError(diags []DiagnosticSummary) *contract.Error {
	summaries := make([]map[string]any, 0, 20)
	total := 0
	for _, d := range diags {
		n := len(d.Diagnostics)
		if n == 0 {
			n = 1
		}
		total += n
		for _, item := range d.Diagnostics {
			if len(summaries) >= 20 {
				break
			}
			copyItem := map[string]any{}
			for key, value := range item {
				copyItem[key] = value
			}
			if copyItem["path"] == nil {
				copyItem["path"] = d.Path
			}
			summaries = append(summaries, copyItem)
		}
		if len(d.Diagnostics) == 0 && len(summaries) < 20 {
			summaries = append(summaries, map[string]any{"severity": "error", "code": "invalid_ticket", "path": d.Path, "message": d.Message})
		}
		if len(summaries) >= 20 {
			break
		}
	}
	details := map[string]any{"diagnostics": summaries}
	if total > 20 {
		details["diagnostics_truncated"] = true
	}
	return contract.NewError(contract.ErrInvalidTicket, "Collection scan failed on malformed ticket metadata.", details)
}
