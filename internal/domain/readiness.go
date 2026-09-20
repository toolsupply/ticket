package domain

import (
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/store"
)

// ReadinessBlocker describes one reason a ticket cannot be claimed.
type ReadinessBlocker struct {
	Code    string `json:"code"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
}

// ReadinessView is the explicit readiness projection used by ready and show.
type ReadinessView struct {
	Ready    bool               `json:"ready"`
	Blockers []ReadinessBlocker `json:"blockers,omitempty"`
}

// NextResult contains the first ticket on the current ready frontier.
type NextResult struct {
	Item    *Summary `json:"item"`
	Changed bool     `json:"-"`
}

// NextOptions selects one worker queue and its simple filters.
type NextOptions struct {
	Queue    string
	Tags     []string
	Priority *int
	Claim    bool
	Actor    string
}

var blockerOrder = map[string]int{
	"state_blocked": 0, "assigned": 1, "missing_objective": 2,
	"external": 3, "dependency_open": 4, "dependency_rejected": 5,
	"dependency_missing": 6, "open_children": 7,
}

// Readiness computes actionability and direct prerequisite/child blockers.
func Readiness(st *store.Store, id string) (*ReadinessView, error) {
	full, err := mustResolve(st, id)
	if err != nil {
		return nil, err
	}
	t, err := ReadTicket(st, full)
	if err != nil {
		return nil, err
	}
	tickets, err := loadGraph(st)
	if err != nil {
		return nil, err
	}
	idx := newGraphIndex(tickets)
	return readinessForWithChildren(t, idx.byID, idx.children), nil
}

// Ready lists actionable tickets using the same row projection as List.
func Ready(st *store.Store, opts ListOptions) (*ListResult, error) {
	requestedState, requestedAssignee := opts.State, opts.Assignee
	if requestedState != "" || len(opts.States) > 0 || requestedAssignee != "" || opts.Unassigned {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Ready accepts only readiness filters.", nil)
	}
	opts.State = "open"
	if opts.LimitSet && opts.Limit <= 0 {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Limit must be greater than zero.", nil)
	}
	if opts.Limit <= 0 {
		opts.Limit = DefaultLimit
	}
	if opts.Priority != nil && (*opts.Priority < 0 || *opts.Priority > 4) {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Priority must be an integer 0-4.", nil)
	}
	var err error
	if opts.Tags, err = normalizeTags(opts.Tags); err != nil {
		return nil, err
	}
	if opts.WithoutTags, err = normalizeTags(opts.WithoutTags); err != nil {
		return nil, err
	}
	if opts.Fields == nil {
		opts.Fields = []string{"id", "title", "state", "priority", "assignee"}
	}
	if len(opts.Fields) == 0 || opts.Fields[0] != "id" {
		opts.Fields = append([]string{"id"}, opts.Fields...)
	}
	for _, field := range opts.Fields {
		if !isListField(field) {
			return nil, contract.NewError(contract.ErrInvalidArgument, "Invalid field "+field+" in fields.", nil)
		}
	}
	if opts.Parent != "" {
		full, err := st.ResolveID(opts.Parent, true)
		if err != nil {
			return nil, err
		}
		opts.Parent = full
	}
	parentFull := opts.Parent
	tickets, err := loadGraph(st)
	if err != nil {
		return nil, err
	}
	idx := newGraphIndex(tickets)
	byID := idx.byID
	children := idx.children
	selected := make([]*Ticket, 0, len(tickets))
	for _, t := range tickets {
		if matchesFilter(t, opts, parentFull) {
			selected = append(selected, t)
		}
	}
	var matches []*Ticket
	for _, t := range selected {
		if readinessForWithChildren(t, byID, children).Ready {
			matches = append(matches, t)
		}
	}
	sortTickets(matches)
	more := len(matches) > opts.Limit
	if more {
		matches = matches[:opts.Limit]
	}
	result := &ListResult{Items: []Summary{}, More: more}
	for _, t := range matches {
		result.Items = append(result.Items, project(t, opts.Fields))
	}
	return result, nil
}

// Next returns the first ticket selected by the requested queue. When claim
// is true, selection and ownership mutation happen while the caller's store
// lock is held, so cooperating invocations cannot claim the same ticket.
func Next(st *store.Store, claim bool, actor string) (*NextResult, error) {
	return NextWithOptions(st, NextOptions{Claim: claim, Actor: actor})
}

func NextWithOptions(st *store.Store, opts NextOptions) (*NextResult, error) {
	queue := opts.Queue
	if queue == "" {
		queue = "open"
	}
	if queue != "open" && queue != "review" {
		return nil, contract.NewError(contract.ErrInvalidArgument,
			"Queue must be open or review.", nil)
	}
	if opts.Priority != nil && (*opts.Priority < 0 || *opts.Priority > 4) {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Priority must be an integer 0-4.", nil)
	}
	if opts.Claim {
		if err := validateActor(opts.Actor); err != nil {
			return nil, err
		}
		owned, err := List(st, ListOptions{
			States: []string{"open", "review"}, Assignee: opts.Actor,
			Fields: []string{"id", "state", "priority", "tags", "assignee"}, Unlimited: true,
		})
		if err != nil {
			return nil, err
		}
		if len(owned.Items) > 1 {
			return nil, contract.NewError(contract.ErrConflict,
				"Actor has multiple active tickets; resolve ownership before requesting next work.",
				map[string]any{"actor": opts.Actor})
		}
		if len(owned.Items) == 1 {
			item := owned.Items[0]
			if item.State == nil || *item.State != queue {
				return nil, contract.NewError(contract.ErrConflict,
					"Actor already owns work in another queue.", map[string]any{"actor": opts.Actor, "id": item.ID})
			}
			if !summaryMatchesWorkFilters(item, opts) {
				return nil, contract.NewError(contract.ErrConflict,
					"Actor already owns work outside the requested filters.", map[string]any{"actor": opts.Actor, "id": item.ID})
			}
			return &NextResult{Item: &item}, nil
		}
	}
	var selected *ListResult
	var err error
	if queue == "open" {
		selected, err = Ready(st, ListOptions{Tags: opts.Tags, Priority: opts.Priority,
			Fields: []string{"id", "title", "state", "priority", "assignee", "tags"},
			Limit:  1, LimitSet: true})
	} else {
		selected, err = List(st, ListOptions{
			State: "review", Tags: opts.Tags, Priority: opts.Priority,
			Fields:     []string{"id", "title", "state", "priority", "assignee", "tags"},
			Unassigned: true, Limit: 1, LimitSet: true,
		})
	}
	if err != nil {
		return nil, err
	}
	if len(selected.Items) == 0 {
		return &NextResult{}, nil
	}
	item := selected.Items[0]
	if opts.Claim {
		claimed, err := Claim(st, item.ID, ClaimOptions{Actor: opts.Actor})
		if err != nil {
			return nil, err
		}
		item.Assignee = &claimed.Assignee
	}
	return &NextResult{Item: &item, Changed: opts.Claim}, nil
}

func summaryMatchesWorkFilters(item Summary, opts NextOptions) bool {
	if opts.Priority != nil && (item.Priority == nil || *item.Priority != *opts.Priority) {
		return false
	}
	have := make(map[string]bool, len(item.Tags))
	for _, tag := range item.Tags {
		have[tag] = true
	}
	for _, tag := range opts.Tags {
		if !have[tag] {
			return false
		}
	}
	return true
}

func readinessForWithChildren(t *Ticket, byID map[string]*Ticket, children map[string][]*Ticket) *ReadinessView {
	var blockers []ReadinessBlocker
	if t.State != "open" {
		blockers = append(blockers, ReadinessBlocker{"state_blocked", "", "Ticket is not open for implementation."})
	}
	if t.Assignee != "" {
		blockers = append(blockers, ReadinessBlocker{"assigned", "", "Ticket is assigned."})
	}
	if strings.TrimSpace(t.SectionText("objective")) == "" {
		blockers = append(blockers, ReadinessBlocker{"missing_objective", "", "Objective is empty."})
	}
	if t.BlockedReason != "" {
		blockers = append(blockers, ReadinessBlocker{"external", "", t.BlockedReason})
	}
	for _, dep := range t.DependsOn {
		other, ok := byID[dep]
		if !ok {
			blockers = append(blockers, ReadinessBlocker{"dependency_missing", dep, "Dependency is missing."})
		} else if other.State == "rejected" {
			blockers = append(blockers, ReadinessBlocker{"dependency_rejected", dep, "Dependency is rejected."})
		} else if other.State != "completed" {
			blockers = append(blockers, ReadinessBlocker{"dependency_open", dep, "Dependency is not completed."})
		}
	}
	for _, child := range children[t.ID] {
		if child.State != "completed" && child.State != "rejected" {
			blockers = append(blockers, ReadinessBlocker{"open_children", child.ID, "A nonterminal child ticket remains."})
		}
	}
	for i := range blockers {
		for j := i + 1; j < len(blockers); j++ {
			if blockerOrder[blockers[j].Code] < blockerOrder[blockers[i].Code] ||
				(blockerOrder[blockers[j].Code] == blockerOrder[blockers[i].Code] && blockers[j].ID < blockers[i].ID) {
				blockers[i], blockers[j] = blockers[j], blockers[i]
			}
		}
	}
	return &ReadinessView{Ready: len(blockers) == 0, Blockers: blockers}
}

// SectionText returns section content without exposing parser details.
func (t *Ticket) SectionText(key string) string {
	return sectionText(t, key)
}

func sectionText(t *Ticket, key string) string {
	if s, ok := t.Sections[key]; ok {
		return s.DisplayContent(t.Body)
	}
	return ""
}
