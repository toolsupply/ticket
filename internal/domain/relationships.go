package domain

import (
	"errors"
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/store"
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
	if err := detectSliceGraphCycle(deps, contract.ErrDependencyCycle); err != nil {
		return err
	}

	// Parent and dependency graphs can each be acyclic while their combined
	// blocker edges form a readiness deadlock. A parent waits on each
	// nonterminal child, and a nonterminal ticket waits on each nonterminal
	// dependency, so validate that effective wait-for graph as well.
	waitFor := make(map[string][]string, len(byID))
	for id := range byID {
		waitFor[id] = nil
	}
	all := append([]*Ticket(nil), tickets...)
	all = append(all, candidate)
	for _, ticket := range all {
		if byID[ticket.ID] != ticket {
			continue
		}
		if ticket.Parent != "" && !ticket.IsTerminal() {
			if parentTicket, ok := byID[ticket.Parent]; ok {
				waitFor[parentTicket.ID] = append(waitFor[parentTicket.ID], ticket.ID)
			}
		}
		if ticket.IsTerminal() {
			continue
		}
		for _, dependency := range ticket.DependsOn {
			if dependencyTicket, ok := byID[dependency]; ok && !dependencyTicket.IsTerminal() {
				waitFor[ticket.ID] = append(waitFor[ticket.ID], dependencyTicket.ID)
			}
		}
	}
	return detectSliceGraphCycleWithMessage(waitFor, contract.ErrReadinessCycle,
		"Readiness wait-for graph contains a cycle.")
}

// validateActivatedGraph checks the effective wait-for graph only when a
// lifecycle transition changes a ticket from terminal to nonterminal.
func validateActivatedGraph(st *store.Store, candidate *Ticket, fromState string) error {
	if candidate == nil || candidate.IsTerminal() || !terminalState(fromState) {
		return nil
	}
	tickets, err := loadGraph(st)
	if err != nil {
		return err
	}
	return validateGraphCandidate(tickets, candidate)
}

func terminalState(state string) bool {
	return state == StateClosed || state == "rejected"
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
	return detectSliceGraphCycleWithMessage(edges, code, "Relationship graph contains a cycle.")
}

func detectSliceGraphCycleWithMessage(edges map[string][]string, code contract.ErrorCode, message string) error {
	state := map[string]uint8{}
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return contract.NewError(code, message, map[string]any{"id": id})
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

func loadGraph(st *store.Store) ([]*Ticket, error) {
	tickets, diags, err := scanWith(st)
	if err != nil {
		return nil, err
	}
	if len(diags) > 0 {
		return nil, collectionError(diags)
	}
	seen := make(map[string]bool, len(tickets))
	for _, ticket := range tickets {
		seen[ticket.ID] = true
	}
	for index := 0; index < len(tickets); index++ {
		ticket := tickets[index]
		references := append([]string{ticket.Parent}, ticket.DependsOn...)
		for _, ref := range references {
			if ref == "" || seen[ref] {
				continue
			}
			archived, readErr := ReadTicket(st, ref)
			if readErr != nil {
				var ce *contract.Error
				if errors.As(readErr, &ce) && ce.Code == contract.ErrNotFound {
					continue
				}
				return nil, readErr
			}
			seen[archived.ID] = true
			tickets = append(tickets, archived)
		}
	}
	return tickets, nil
}

// ValidateGraphs checks all existing parent/dependency graph edges without
// changing repository state.
func ValidateGraphs(st *store.Store) error {
	tickets, err := loadAllGraph(st)
	if err != nil || len(tickets) == 0 {
		return err
	}
	owners := make(map[string]bool, len(tickets))
	for _, ticket := range tickets {
		owners[ticket.ID] = true
	}
	return validateExistingGraphs(tickets, owners)
}

// ValidateActiveGraphs validates active ticket structure and relationships
// without scanning unrelated archived tickets. Archived tickets explicitly
// referenced by active tickets are read to preserve reference resolution,
// but their own relationships are not traversed or validated.
func ValidateActiveGraphs(st *store.Store) error {
	active, diags, err := scanWith(st)
	if err != nil {
		return err
	}
	if len(diags) > 0 {
		return collectionError(diags)
	}
	owners := make(map[string]bool, len(active))
	byID := make(map[string]*Ticket, len(active))
	for _, ticket := range active {
		owners[ticket.ID] = true
		byID[ticket.ID] = ticket
	}
	for _, ticket := range active {
		references := append([]string{ticket.Parent}, ticket.DependsOn...)
		for _, ref := range references {
			if ref == "" || byID[ref] != nil {
				continue
			}
			archived, readErr := ReadTicket(st, ref)
			if readErr != nil {
				var ce *contract.Error
				if errors.As(readErr, &ce) && ce.Code == contract.ErrNotFound {
					continue // The shared validator reports the dangling edge below.
				}
				return readErr
			}
			byID[archived.ID] = archived
		}
	}
	tickets := make([]*Ticket, 0, len(byID))
	for _, ticket := range byID {
		tickets = append(tickets, ticket)
	}
	return validateExistingGraphs(tickets, owners)
}

// validateExistingGraphs shares reference and cycle validation for full and
// active-only checks. owners identifies tickets whose stored relationships
// are authoritative for the selected validation scope.
func validateExistingGraphs(tickets []*Ticket, owners map[string]bool) error {
	byID := make(map[string]*Ticket, len(tickets))
	for _, ticket := range tickets {
		byID[ticket.ID] = ticket
	}
	for _, ticket := range tickets {
		if !owners[ticket.ID] {
			continue
		}
		if ticket.Parent != "" && byID[ticket.Parent] == nil {
			return contract.NewError(contract.ErrDanglingReference,
				"Parent references a missing ticket.", map[string]any{"id": ticket.ID, "parent": ticket.Parent})
		}
		for _, dep := range ticket.DependsOn {
			if byID[dep] == nil {
				return contract.NewError(contract.ErrDanglingReference,
					"Dependency references a missing ticket.", map[string]any{"id": ticket.ID, "depends_on": dep})
			}
		}
	}
	parent := make(map[string]string, len(owners))
	deps := make(map[string][]string, len(owners))
	waitFor := make(map[string][]string, len(owners))
	for id := range owners {
		parent[id] = ""
		deps[id] = nil
		waitFor[id] = nil
	}
	for _, ticket := range tickets {
		if !owners[ticket.ID] {
			continue
		}
		if ticket.Parent != "" && owners[ticket.Parent] {
			parent[ticket.ID] = ticket.Parent
			if !ticket.IsTerminal() {
				waitFor[ticket.Parent] = append(waitFor[ticket.Parent], ticket.ID)
			}
		}
		for _, dependency := range ticket.DependsOn {
			if owners[dependency] {
				deps[ticket.ID] = append(deps[ticket.ID], dependency)
				if !ticket.IsTerminal() && !byID[dependency].IsTerminal() {
					waitFor[ticket.ID] = append(waitFor[ticket.ID], dependency)
				}
			}
		}
	}
	if err := detectStringGraphCycle(parent, contract.ErrParentCycle); err != nil {
		return err
	}
	if err := detectSliceGraphCycle(deps, contract.ErrDependencyCycle); err != nil {
		return err
	}
	return detectSliceGraphCycleWithMessage(waitFor, contract.ErrReadinessCycle,
		"Readiness wait-for graph contains a cycle.")
}

func loadAllGraph(st *store.Store) ([]*Ticket, error) {
	active, activeDiags, err := scanWith(st)
	if err != nil {
		return nil, err
	}
	archived, archiveDiags, err := scanArchivedWith(st)
	if err != nil {
		return nil, err
	}
	if len(activeDiags) > 0 || len(archiveDiags) > 0 {
		return nil, collectionError(append(activeDiags, archiveDiags...))
	}
	seen := make(map[string]bool, len(active)+len(archived))
	for _, ticket := range append(active, archived...) {
		if seen[ticket.ID] {
			return nil, contract.NewError(contract.ErrInvalidRepository,
				"Ticket ID exists in both active and archive namespaces.", map[string]any{"id": ticket.ID})
		}
		seen[ticket.ID] = true
	}
	return append(active, archived...), nil
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
	if candidate == nil {
		return false, contract.NewError(contract.ErrInvalidArgument, "Edited ticket is missing.", nil)
	}
	if err := EnsureActive(st, candidate.ID); err != nil {
		return false, err
	}
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
				Title: candidate.Title, State: candidate.State, Priority: candidate.Priority,
				Objective: objectivePreview(&candidate)}, nil
		} else if !errors.Is(err, store.ErrTargetExists) {
			return nil, err
		}
	}
	return nil, contract.NewError(contract.ErrInternalError, "ID collisions exceeded the retry budget.", nil)
}
