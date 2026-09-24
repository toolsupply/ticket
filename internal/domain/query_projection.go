package domain

import (
	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/store"
)

// ProjectTicketSet returns the existing list projection for a selected set.
// It is intentionally separate from selection so callers can reuse the same
// summaries and renderers without converting the set through formatted output.
// dependencyView may be empty, "blocking", or "all".
func ProjectTicketSet(st *store.Store, set *TicketSet, fields []string, dependencyView string) (*ListResult, error) {
	if set == nil {
		set = &TicketSet{}
	}
	fields, err := queryProjectionFields(fields)
	if err != nil {
		return nil, err
	}
	if dependencyView != "" && dependencyView != "blocking" && dependencyView != "all" {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Dependency view must be blocking or all.", nil)
	}
	var dependencyByID map[string]*Ticket
	if dependencyView != "" {
		dependencyByID, err = projectDependencyTickets(st, set)
		if err != nil {
			return nil, err
		}
	}
	result := &ListResult{
		Items:            make([]Summary, 0, len(set.Tickets)),
		More:             set.More,
		ShowDependencies: dependencyView != "",
	}
	for _, ticket := range set.Tickets {
		if ticket == nil {
			continue
		}
		item := project(ticket, fields)
		if dependencyView != "" {
			item.Dependencies = dependencySummaries(ticket, dependencyByID, dependencyView)
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// projectDependencyTickets starts with the selected set and resolves only its
// direct dependency references. In particular, an active-only projection must
// not enumerate the archive merely to render dependency summaries; unrelated
// malformed archive entries are outside the projection's locality boundary.
func projectDependencyTickets(st *store.Store, set *TicketSet) (map[string]*Ticket, error) {
	if set == nil {
		return map[string]*Ticket{}, nil
	}
	byID := make(map[string]*Ticket, len(set.Tickets))
	for _, ticket := range set.Tickets {
		if ticket != nil {
			byID[ticket.ID] = ticket
		}
	}
	for _, ticket := range set.Tickets {
		if ticket == nil {
			continue
		}
		for _, dependencyID := range ticket.DependsOn {
			if _, ok := byID[dependencyID]; ok {
				continue
			}
			dependency, err := ReadTicket(st, dependencyID)
			if err != nil {
				var ce *contract.Error
				if isNotFoundError(err, &ce) {
					continue
				}
				return nil, err
			}
			byID[dependency.ID] = dependency
		}
	}
	return byID, nil
}

// IDs returns a copy of the set's canonical full ticket IDs.
func (s *TicketSet) IDs() []string {
	if s == nil {
		return nil
	}
	ids := make([]string, 0, len(s.Tickets))
	for _, ticket := range s.Tickets {
		if ticket != nil {
			ids = append(ids, ticket.ID)
		}
	}
	return ids
}

// Len returns the number of tickets in the set.
func (s *TicketSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.Tickets)
}

func queryProjectionFields(fields []string) ([]string, error) {
	if len(fields) == 0 {
		return []string{"id", "title", "state", "priority", "assignee"}, nil
	}
	fields = append([]string(nil), fields...)
	if fields[0] != "id" {
		fields = append([]string{"id"}, fields...)
	}
	for _, field := range fields {
		if !isListField(field) {
			return nil, contract.NewError(contract.ErrInvalidArgument, "Invalid field "+field+" in fields.", nil)
		}
	}
	return fields, nil
}
