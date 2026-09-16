package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"ticket/internal/contract"
	"ticket/internal/store"
)

type CloseOptions struct {
	Outcome string
	Actor   string
	Message *string
}

type RejectOptions struct {
	Actor   string
	Outcome string
	Message *string
}

type ReviewOptions struct {
	Actor   string
	Message *string
}

type SubmitOptions struct {
	Actor   string
	Handoff *string
	Message *string
}

type HoldOptions struct {
	Actor   string
	Handoff *string
	Message *string
}

type OpenOptions struct {
	Handoff *string
	Actor   string
	Message *string
}

type TransitionResult struct {
	ID      string `json:"id"`
	Changed bool   `json:"changed"`
	State   string `json:"state"`
}

type BatchTransitionResult struct {
	Items []TransitionResult `json:"items"`
}

// CloseMany closes the supplied tickets in deterministic ID order. All
// targets are read and validated before the first publication.
func CloseMany(st *store.Store, ids []string, opts CloseOptions) (*BatchTransitionResult, error) {
	resolved := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		full, err := mustResolve(st, id)
		if err != nil {
			return nil, err
		}
		if seen[full] {
			continue
		}
		seen[full] = true
		resolved = append(resolved, full)
	}
	sort.Strings(resolved)
	if strings.TrimSpace(opts.Outcome) != "" {
		if _, err := ValidateSectionContent("outcome", opts.Outcome); err != nil {
			return nil, err
		}
	}
	for _, id := range resolved {
		t, err := ReadTicket(st, id)
		if err != nil {
			return nil, err
		}
		if t.State == "rejected" {
			return nil, contract.NewError(contract.ErrInvalidTransition, "Rejected tickets cannot be closed.", nil)
		}
	}
	result := &BatchTransitionResult{Items: make([]TransitionResult, 0, len(resolved))}
	for _, id := range resolved {
		closed, err := Close(st, id, opts)
		if err != nil {
			return nil, err
		}
		result.Items = append(result.Items, *closed)
	}
	return result, nil
}

// CloseAll closes every nonterminal ticket in the repository.
func CloseAll(st *store.Store, opts CloseOptions) (*BatchTransitionResult, error) {
	tickets, diags, err := scanWith(st)
	if err != nil {
		return nil, err
	}
	if len(diags) > 0 {
		return nil, collectionError(diags)
	}
	ids := make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		if !ticket.IsTerminal() {
			ids = append(ids, ticket.ID)
		}
	}
	return CloseMany(st, ids, opts)
}

// Close is the authoritative human completion action. It may close any
// nonterminal work state and clears stale ownership; an outcome is optional.
func Close(st *store.Store, id string, opts CloseOptions) (*TransitionResult, error) {
	full, err := mustResolve(st, id)
	if err != nil {
		return nil, err
	}
	t, err := ReadTicket(st, full)
	if err != nil {
		return nil, err
	}
	if t.State == "completed" {
		return &TransitionResult{ID: full, Changed: false, State: "completed"}, nil
	}
	if t.State == "rejected" {
		return nil, contract.NewError(contract.ErrInvalidTransition, "Rejected tickets cannot be closed.", nil)
	}
	sections := map[string]string{}
	if strings.TrimSpace(opts.Outcome) != "" {
		sections["outcome"] = opts.Outcome
	}
	newBody, err := transitionBody(t, sections, opts.Message, opts.Actor)
	if err != nil {
		return nil, err
	}
	t.State = "completed"
	t.Assignee = ""
	return publishTransition(st, t, newBody, "completed")
}

func Reject(st *store.Store, id string, opts RejectOptions) (*TransitionResult, error) {
	full, err := mustResolve(st, id)
	if err != nil {
		return nil, err
	}
	t, err := ReadTicket(st, full)
	if err != nil {
		return nil, err
	}
	if t.IsTerminal() {
		return nil, contract.NewError(contract.ErrInvalidTransition, "Terminal tickets cannot be rejected.", nil)
	}
	if t.Assignee != "" {
		if err := validateActor(opts.Actor); err != nil {
			return nil, err
		}
		if t.Assignee != opts.Actor {
			return nil, contract.NewError(contract.ErrAlreadyClaimed, "The ticket is assigned to another actor.", map[string]any{"id": full})
		}
	}
	if strings.TrimSpace(opts.Outcome) == "" {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Reject requires a nonempty outcome.", nil)
	}
	outcome, err := ValidateSectionContent("outcome", opts.Outcome)
	if err != nil {
		return nil, err
	}
	if t.State == "open" || t.State == "review" {
		readiness, err := Readiness(st, full)
		if err != nil {
			return nil, err
		}
		for _, blocker := range readiness.Blockers {
			if blocker.Code == "open_children" {
				return nil, contract.NewError(contract.ErrDependencyUnresolved, "Nonterminal child tickets prevent rejection.", map[string]any{"id": blocker.ID})
			}
		}
	}
	t.State = "rejected"
	t.Assignee = ""
	newBody, err := transitionBody(t, map[string]string{"outcome": outcome}, opts.Message, opts.Actor)
	if err != nil {
		return nil, err
	}
	return publishTransition(st, t, newBody, "rejected")
}

func Approve(st *store.Store, id string, opts ReviewOptions) (*TransitionResult, error) {
	return moveAssigned(st, id, opts.Actor, nil, opts.Message, "review", "signoff", "approve")
}

// ApproveMany approves supplied review tickets in deterministic ID order.
func ApproveMany(st *store.Store, ids []string, opts ReviewOptions) (*BatchTransitionResult, error) {
	resolved := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		full, err := mustResolve(st, id)
		if err != nil {
			return nil, err
		}
		if !seen[full] {
			seen[full] = true
			resolved = append(resolved, full)
		}
	}
	sort.Strings(resolved)
	result := &BatchTransitionResult{Items: make([]TransitionResult, 0, len(resolved))}
	for _, id := range resolved {
		approved, err := Approve(st, id, opts)
		if err != nil {
			return nil, err
		}
		result.Items = append(result.Items, *approved)
	}
	return result, nil
}

func ApproveAll(st *store.Store, opts ReviewOptions) (*BatchTransitionResult, error) {
	ids, err := stateIDs(st, "review")
	if err != nil {
		return nil, err
	}
	return ApproveMany(st, ids, opts)
}

func stateIDs(st *store.Store, state string) ([]string, error) {
	tickets, diags, err := scanWith(st)
	if err != nil {
		return nil, err
	}
	if len(diags) > 0 {
		return nil, collectionError(diags)
	}
	ids := make([]string, 0)
	for _, ticket := range tickets {
		if ticket.State == state {
			ids = append(ids, ticket.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func Open(st *store.Store, id string, opts OpenOptions) (*TransitionResult, error) {
	full, err := mustResolve(st, id)
	if err != nil {
		return nil, err
	}
	t, err := ReadTicket(st, full)
	if err != nil {
		return nil, err
	}
	sections := map[string]string{}
	if opts.Handoff != nil {
		sections["handoff"] = *opts.Handoff
	}
	newBody, err := transitionBody(t, sections, opts.Message, opts.Actor)
	if err != nil {
		return nil, err
	}
	if t.State == "open" && t.Assignee == "" && opts.Handoff == nil && opts.Message == nil {
		return &TransitionResult{ID: full, Changed: false, State: "open"}, nil
	}
	t.State = "open"
	t.Assignee = ""
	return publishTransition(st, t, newBody, "open")
}

func Submit(st *store.Store, id string, opts SubmitOptions) (*TransitionResult, error) {
	return moveAssigned(st, id, opts.Actor, opts.Handoff, opts.Message, "open", "review", "submit")
}

func Hold(st *store.Store, id string, opts HoldOptions) (*TransitionResult, error) {
	return moveAssigned(st, id, opts.Actor, opts.Handoff, opts.Message, "open", "hold", "hold")
}

func moveAssigned(st *store.Store, id, actor string, handoff, message *string, from, to, action string) (*TransitionResult, error) {
	return moveAssignedAny(st, id, actor, handoff, message, []string{from}, to, action)
}

func moveAssignedAny(st *store.Store, id, actor string, handoff, message *string, from []string, to, action string) (*TransitionResult, error) {
	full, err := mustResolve(st, id)
	if err != nil {
		return nil, err
	}
	t, err := ReadTicket(st, full)
	if err != nil {
		return nil, err
	}
	allowed := false
	for _, candidate := range from {
		if t.State == candidate {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, contract.NewError(contract.ErrInvalidTransition, "Cannot "+action+" a ticket in state "+t.State+".", nil)
	}
	if t.Assignee != "" {
		if err := validateActor(actor); err != nil {
			return nil, err
		}
		if t.Assignee != actor {
			return nil, contract.NewError(contract.ErrAlreadyClaimed, "The ticket is assigned to another actor.", map[string]any{"id": full})
		}
	}
	t.State, t.Assignee = to, ""
	sections := map[string]string{}
	if handoff != nil {
		sections["handoff"] = *handoff
	}
	newBody, err := transitionBody(t, sections, message, actor)
	if err != nil {
		return nil, err
	}
	return publishTransition(st, t, newBody, to)
}

func transitionBody(t *Ticket, sections map[string]string, message *string, actor string) ([]byte, error) {
	if message != nil {
		log, err := workLogContent(t, *message, actor)
		if err != nil {
			return nil, err
		}
		sections["work_log"] = log
	}
	if len(sections) == 0 {
		return t.Body, nil
	}
	return applyBodyChanges(t, UpdateOptions{Sections: sections, allowWorkLog: true}, map[string]bool{})
}

func workLogContent(t *Ticket, message, actor string) (string, error) {
	message = strings.TrimSpace(strings.ReplaceAll(message, "\r\n", "\n"))
	if message == "" {
		return "", contract.NewError(contract.ErrInvalidArgument, "Message must not be empty.", nil)
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "human"
	}
	if err := validateActor(actor); err != nil {
		return "", err
	}
	lines := strings.Split(message, "\n")
	var entry strings.Builder
	fmt.Fprintf(&entry, "- %s %s: %s\n", time.Now().UTC().Format(time.RFC3339), actor, strings.TrimSpace(lines[0]))
	for _, line := range lines[1:] {
		fmt.Fprintf(&entry, "    %s\n", strings.TrimRight(line, "\r"))
	}
	existing := strings.TrimRight(t.SectionText("work_log"), "\r\n")
	if existing != "" {
		existing += "\n"
	}
	content := existing + entry.String()
	return ValidateSectionContent("work_log", content)
}

func publishTransition(st *store.Store, t *Ticket, newBody []byte, state string) (*TransitionResult, error) {
	changed := map[string]bool{"state": true, "assignee": true}
	data, err := renderUpdated(t, newBody, changed)
	if err != nil {
		return nil, err
	}
	if len(data) > TaskMaxBytes {
		return nil, contract.NewError(contract.ErrFileTooLarge, "Updated TASK.md exceeds the 1 MiB managed-file limit.", nil)
	}
	if _, err := ParseTicketFile(t.ID, data); err != nil {
		return nil, err
	}
	_, err = st.ReplaceTask(t.ID, data, TaskMaxBytes)
	if err != nil {
		return nil, err
	}
	return &TransitionResult{ID: t.ID, Changed: true, State: state}, nil
}
