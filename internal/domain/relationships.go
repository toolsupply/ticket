package domain

import (
	"errors"
	"strings"
	"ticket/internal/contract"
	"ticket/internal/store"
)

// validateGraphCandidate validates parent and dependency cycles after a
// proposed relationship mutation.
func validateGraphCandidate(tickets []*Ticket, candidate *Ticket) error {
	byID := make(map[string]*Ticket, len(tickets)+1)
	for _, t := range tickets {
		byID[t.ID] = t
	}
	byID[candidate.ID] = candidate
	parent := make(map[string]string, len(byID))
	deps := make(map[string][]string, len(byID))
	for id, t := range byID {
		parent[id] = t.Parent
		deps[id] = t.DependsOn
	}
	if err := detectStringGraphCycle(parent, contract.ErrParentCycle); err != nil {
		return err
	}
	return detectSliceGraphCycle(deps, contract.ErrDependencyCycle)
}

type graphIndex struct {
	byID     map[string]*Ticket
	children map[string][]*Ticket
}

func newGraphIndex(tickets []*Ticket) *graphIndex {
	idx := &graphIndex{byID: make(map[string]*Ticket, len(tickets)), children: make(map[string][]*Ticket)}
	for _, t := range tickets {
		idx.byID[t.ID] = t
	}
	for _, t := range tickets {
		if t.Parent != "" && idx.byID[t.Parent] != nil {
			idx.children[t.Parent] = append(idx.children[t.Parent], t)
		}
	}
	for parent := range idx.children {
		sortTickets(idx.children[parent])
	}
	return idx
}

func detectStringGraphCycle(edges map[string]string, code contract.ErrorCode) error {
	state := map[string]uint8{}
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return contract.NewError(code, "Relationship graph contains a cycle.", map[string]any{"id": id})
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		if next := edges[id]; next != "" {
			if _, ok := edges[next]; ok {
				if err := visit(next); err != nil {
					return err
				}
			}
		}
		state[id] = 2
		return nil
	}
	for id := range edges {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func detectSliceGraphCycle(edges map[string][]string, code contract.ErrorCode) error {
	state := map[string]uint8{}
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return contract.NewError(code, "Relationship graph contains a cycle.", map[string]any{"id": id})
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		for _, next := range edges[id] {
			if _, ok := edges[next]; ok {
				if err := visit(next); err != nil {
					return err
				}
			}
		}
		state[id] = 2
		return nil
	}
	for id := range edges {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

// ValidateGraphs checks all existing parent/dependency graph edges without
// changing repository state.
func loadGraph(st *store.Store) ([]*Ticket, error) {
	tickets, diags, err := scanWith(st)
	if err != nil {
		return nil, err
	}
	if len(diags) > 0 {
		return nil, collectionError(diags)
	}
	return tickets, nil
}

func ValidateGraphs(st *store.Store) error {
	tickets, err := loadGraph(st)
	if err != nil || len(tickets) == 0 {
		return err
	}
	byID := make(map[string]bool, len(tickets))
	for _, ticket := range tickets {
		byID[ticket.ID] = true
	}
	for _, ticket := range tickets {
		if ticket.Parent != "" && !byID[ticket.Parent] {
			return contract.NewError(contract.ErrDanglingReference,
				"Parent references a missing ticket.", map[string]any{"id": ticket.ID, "parent": ticket.Parent})
		}
		for _, dep := range ticket.DependsOn {
			if !byID[dep] {
				return contract.NewError(contract.ErrDanglingReference,
					"Dependency references a missing ticket.", map[string]any{"id": ticket.ID, "depends_on": dep})
			}
		}
	}
	return validateGraphCandidate(tickets, tickets[0])
}

// ValidateEditedTicket checks a parsed editor draft against the current
// repository without modifying it. The caller performs any optimistic
// snapshot comparison before publishing the draft.
func ValidateEditedTicket(st *store.Store, candidate *Ticket) error {
	tickets, err := loadGraph(st)
	if err != nil {
		return err
	}
	return validateCandidateReferences(st, tickets, candidate)
}

// PublishEditedTicket validates and atomically replaces an existing ticket
// with the complete bytes from an editor draft.
func PublishEditedTicket(st *store.Store, candidate *Ticket) (bool, error) {
	if err := ValidateEditedTicket(st, candidate); err != nil {
		return false, err
	}
	return st.ReplaceTask(candidate.ID, candidate.FileBytes, TaskMaxBytes)
}

func validateCandidateReferences(st *store.Store, tickets []*Ticket, candidate *Ticket) error {
	if candidate == nil {
		return contract.NewError(contract.ErrInvalidArgument, "Edited ticket is missing.", nil)
	}
	if candidate.Parent != "" {
		if candidate.Parent == candidate.ID {
			return contract.NewError(contract.ErrParentCycle, "Parent relationship would create a cycle.", map[string]any{"id": candidate.ID})
		}
		if _, err := st.ResolveID(candidate.Parent, true); err != nil {
			return err
		}
	}
	for _, dependency := range candidate.DependsOn {
		if dependency == candidate.ID {
			return contract.NewError(contract.ErrDependencyCycle, "Dependency relationship would create a cycle.", map[string]any{"id": candidate.ID})
		}
		if _, err := st.ResolveID(dependency, true); err != nil {
			return err
		}
	}
	return validateGraphCandidate(tickets, candidate)
}

// CreateFromDraft publishes a validated editor draft after allocating its
// ticket ID. The draft bytes are preserved exactly; only the directory name
// supplies the new identity.
func CreateFromDraft(st *store.Store, draft *Ticket) (*CreateResult, error) {
	if draft == nil || strings.TrimSpace(draft.Title) == "" {
		return nil, contract.NewError(contract.ErrInvalidArgument, "A nonempty title is required.", nil)
	}
	candidateData := draft.FileBytes
	candidate := *draft
	if candidate.State == "open" && strings.TrimSpace(candidate.SectionText("objective")) == "" {
		candidate.State = "hold"
		var err error
		candidateData, err = renderUpdated(&candidate, candidate.Body, map[string]bool{"state": true})
		if err != nil {
			return nil, err
		}
		candidate.FileBytes = candidateData
	}
	tickets, err := loadGraph(st)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < maxCollisionRetries; attempt++ {
		id, err := st.NewID("")
		if err != nil {
			return nil, contract.NewError(contract.ErrRandomnessUnavailable,
				"Randomness is unavailable; cannot generate an ID.", nil)
		}
		occupied, err := st.TicketExists(id)
		if err != nil {
			return nil, err
		}
		if occupied {
			continue
		}
		candidate.ID = id
		candidate.TaskRelPath = id + "/TASK.md"
		if err := validateCandidateReferences(st, tickets, &candidate); err != nil {
			return nil, err
		}
		if err := st.PublishTicket(id, candidateData, TaskMaxBytes); err == nil {
			return &CreateResult{ID: id, Path: id + "/TASK.md", Changed: true,
				State: candidate.State, Priority: candidate.Priority, Objective: objectivePreview(&candidate)}, nil
		} else if !errors.Is(err, store.ErrTargetExists) {
			return nil, err
		}
	}
	return nil, contract.NewError(contract.ErrInternalError, "ID collisions exceeded the retry budget.", nil)
}
