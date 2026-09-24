// List operation and complete ticket reads.
package domain

import (
	"sort"
	"strconv"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/identity"
	"github.com/toolsupply/ticket/internal/store"
)

const (
	DefaultLimit = 20
)

type ListOptions struct {
	IDs               []string
	State             string
	States            []string
	Tags, WithoutTags []string
	Assignee, Parent  string
	Unassigned        bool
	Priority          *int
	Limit             int
	LimitSet          bool
	Offset            int
	NonterminalOnly   bool
	Unlimited         bool
	Fields            []string
	Sort              string
	ArchivedOnly      bool
	IncludeArchived   bool
	// DependencyView requests presentation data for direct dependencies:
	// "blocking" includes only unresolved dependencies, while "all" includes
	// every direct dependency.
	DependencyView string
}

type DependencySummary struct {
	ID        string `json:"id"`
	Satisfied bool   `json:"satisfied"`
	Archived  bool   `json:"archived"`
	Missing   bool   `json:"missing,omitempty"`
}

type Summary struct {
	ID           string              `json:"id"`
	Title        *string             `json:"title,omitempty"`
	State        *string             `json:"state,omitempty"`
	Priority     *int                `json:"priority,omitempty"`
	Assignee     *string             `json:"assignee,omitempty"`
	Tags         []string            `json:"tags,omitempty"`
	Parent       *string             `json:"parent,omitempty"`
	DependsOn    []string            `json:"depends_on,omitempty"`
	Path         *string             `json:"path,omitempty"`
	Archived     bool                `json:"archived"`
	Dependencies []DependencySummary `json:"dependencies,omitempty"`
}

type ListResult struct {
	Items            []Summary `json:"items"`
	More             bool      `json:"more"`
	ShowDependencies bool      `json:"-"`
	IDsOnly          bool      `json:"-"`
}

// List is the compatibility projection for the legacy list API. Selection is
// compiled into the repository query engine so list and TQL cannot drift.
func List(st *store.Store, opts ListOptions) (*ListResult, error) {
	set, fields, err := selectList(st, opts)
	if err != nil {
		return nil, err
	}
	return ProjectTicketSet(st, set, fields, opts.DependencyView)
}

// SelectList evaluates the legacy list selection once and returns its
// canonical TicketSet. Presentation callers can then project that same set
// without reconstructing selectors or evaluating the query a second time.
func SelectList(st *store.Store, opts ListOptions) (*TicketSet, error) {
	set, _, err := selectList(st, opts)
	return set, err
}

func selectList(st *store.Store, opts ListOptions) (*TicketSet, []string, error) {
	spec, fields, err := listQuerySpec(st, opts)
	if err != nil {
		return nil, nil, err
	}
	set, err := EvaluateQuery(st, spec)
	return set, fields, err
}

func listQuerySpec(st *store.Store, opts ListOptions) (QuerySpec, []string, error) {
	if opts.LimitSet && opts.Limit <= 0 {
		return QuerySpec{}, nil, contract.NewError(contract.ErrInvalidArgument, "Limit must be greater than zero.", nil)
	}
	if opts.Offset < 0 {
		return QuerySpec{}, nil, contract.NewError(contract.ErrInvalidArgument, "Offset must not be negative.", nil)
	}
	states := append([]string(nil), opts.States...)
	if opts.State != "" {
		states = append(states, opts.State)
	}
	if len(states) == 0 {
		if len(opts.IDs) > 0 {
			states = []string{"all"}
		} else if opts.ArchivedOnly || opts.IncludeArchived {
			states = []string{"all"}
		} else {
			states = []string{"open"}
		}
	}
	terms := make([]Expr, 0, len(states)+len(opts.IDs)+len(opts.Tags)+len(opts.WithoutTags)+4)
	for index, state := range states {
		if state == "all" {
			continue
		}
		normalized, valid := NormalizeLifecycleState(state)
		if !valid {
			return QuerySpec{}, nil, contract.NewError(contract.ErrInvalidArgument, "State must be open, hold, review, signoff, closed, rejected, or all.", nil)
		}
		states[index] = normalized
	}
	for _, state := range states {
		if state == "all" && len(states) != 1 {
			return QuerySpec{}, nil, contract.NewError(contract.ErrInvalidArgument, "The all state cannot be combined with other states.", nil)
		}
	}
	if len(states) > 0 && states[0] != "all" {
		stateTerms := make([]Expr, 0, len(states))
		for _, state := range states {
			stateTerms = append(stateTerms, Field("state", state))
		}
		terms = append(terms, Or(stateTerms...))
	}
	if opts.Assignee != "" && opts.Unassigned {
		return QuerySpec{}, nil, contract.NewError(contract.ErrInvalidArgument, "Assignee and unassigned conflict.", nil)
	}
	if opts.Priority != nil && (*opts.Priority < 0 || *opts.Priority > 4) {
		return QuerySpec{}, nil, contract.NewError(contract.ErrInvalidArgument, "Priority must be an integer 0-4.", nil)
	}
	if opts.DependencyView != "" && opts.DependencyView != "blocking" && opts.DependencyView != "all" {
		return QuerySpec{}, nil, contract.NewError(contract.ErrInvalidArgument, "Dependency view must be blocking or all.", nil)
	}
	var err error
	tags, err := normalizeTags(opts.Tags)
	if err != nil {
		return QuerySpec{}, nil, err
	}
	withoutTags, err := normalizeTags(opts.WithoutTags)
	if err != nil {
		return QuerySpec{}, nil, err
	}
	idTerms := make([]Expr, 0, len(opts.IDs))
	for _, id := range opts.IDs {
		if _, _, ok := store.ParseID(id); !ok && !identity.ValidShorthand(id) {
			return QuerySpec{}, nil, contract.NewError(contract.ErrInvalidArgument,
				"Reference is not a valid ticket ID.", map[string]any{"id": id})
		}
		idTerms = append(idTerms, Field("id", id))
	}
	if len(idTerms) > 0 {
		terms = append(terms, Or(idTerms...))
	}
	for _, tag := range tags {
		terms = append(terms, Field("tag", tag))
	}
	for _, tag := range withoutTags {
		terms = append(terms, Not(Field("tag", tag)))
	}
	if opts.Assignee != "" {
		terms = append(terms, Field("assignee", opts.Assignee))
	}
	if opts.Unassigned {
		terms = append(terms, Bare("unclaimed"))
	}
	if opts.NonterminalOnly {
		terms = append(terms, Not(Bare("terminal")))
	}
	if opts.Parent != "" {
		terms = append(terms, Field("parent", opts.Parent))
	}
	if opts.Priority != nil {
		terms = append(terms, Field("priority", "P"+strconv.Itoa(*opts.Priority)))
	}
	fields, err := queryProjectionFields(opts.Fields)
	if err != nil {
		return QuerySpec{}, nil, err
	}
	sortSpec := SortPriorityID
	switch opts.Sort {
	case "":
	case "id_desc":
		sortSpec = SortIDDesc
	case "modified_desc":
		sortSpec = SortModifiedDesc
	default:
		return QuerySpec{}, nil, contract.NewError(contract.ErrInvalidArgument, "Invalid list sort.", nil)
	}
	limit := opts.Limit
	if opts.Unlimited {
		limit = 0
	} else if limit <= 0 {
		limit = DefaultLimit
	}
	scope := ScopeActive
	if opts.ArchivedOnly {
		scope = ScopeArchived
	} else if opts.IncludeArchived {
		scope = ScopeAll
	}
	return QuerySpec{Expr: And(terms...), Scope: scope, Sort: sortSpec, Limit: limit, Offset: opts.Offset}, fields, nil
}

func dependencySummaries(ticket *Ticket, byID map[string]*Ticket, view string) []DependencySummary {
	dependencies := make([]DependencySummary, 0, len(ticket.DependsOn))
	for _, id := range ticket.DependsOn {
		other, ok := byID[id]
		dependency := DependencySummary{ID: id, Missing: !ok}
		if ok {
			dependency.Archived = other.Archived
			dependency.Satisfied = other.State == StateClosed
		}
		if view == "blocking" && dependency.Satisfied {
			continue
		}
		dependencies = append(dependencies, dependency)
	}
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].ID < dependencies[j].ID })
	return dependencies
}

func isListField(field string) bool {
	switch field {
	case "id", "title", "state", "priority", "assignee", "tags", "parent", "depends_on", "path", "archived":
		return true
	}
	return false
}

func project(ticket *Ticket, fields []string) Summary {
	set := make(map[string]bool, len(fields))
	for _, field := range fields {
		set[field] = true
	}
	s := Summary{ID: ticket.ID}
	s.Archived = ticket.Archived
	if set["title"] {
		s.Title = &ticket.Title
	}
	if set["state"] {
		s.State = &ticket.State
	}
	if set["priority"] {
		s.Priority = &ticket.Priority
	}
	if set["assignee"] && ticket.Assignee != "" {
		s.Assignee = &ticket.Assignee
	}
	if set["tags"] && len(ticket.Tags) > 0 {
		s.Tags = ticket.Tags
	}
	if set["parent"] && ticket.Parent != "" {
		s.Parent = &ticket.Parent
	}
	if set["depends_on"] && len(ticket.DependsOn) > 0 {
		s.DependsOn = ticket.DependsOn
	}
	if set["path"] {
		s.Path = &ticket.TaskRelPath
	}
	return s
}

func matchesFilter(ticket *Ticket, opts ListOptions, parent string) bool {
	states := opts.States
	if len(states) == 0 {
		if opts.State == "" {
			states = []string{"open"}
		} else {
			states = []string{opts.State}
		}
	}
	matchedState := false
	for _, state := range states {
		if state == "all" || ticket.State == state {
			matchedState = true
			break
		}
	}
	if !matchedState {
		return false
	}
	if opts.NonterminalOnly && ticket.IsTerminal() {
		return false
	}
	have := map[string]bool{}
	for _, tag := range ticket.Tags {
		have[tag] = true
	}
	for _, tag := range opts.Tags {
		if !have[tag] {
			return false
		}
	}
	for _, tag := range opts.WithoutTags {
		if have[tag] {
			return false
		}
	}
	if opts.Assignee != "" && ticket.Assignee != opts.Assignee {
		return false
	}
	if opts.Unassigned && ticket.Assignee != "" {
		return false
	}
	if parent != "" && ticket.Parent != parent {
		return false
	}
	if opts.Priority != nil && ticket.Priority != *opts.Priority {
		return false
	}
	return true
}
