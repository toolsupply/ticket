package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/markdown"
	"github.com/toolsupply/ticket/internal/store"
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
	Claim   bool
}

type StateOptions struct {
	Actor   string
	Message *string
}

type TransitionResult struct {
	ID        string `json:"id"`
	Changed   bool   `json:"changed"`
	FromState string `json:"from_state,omitempty"`
	State     string `json:"state"`
	Assignee  string `json:"assignee,omitempty"`
}

type BatchTransitionResult struct {
	Items []TransitionResult `json:"items"`
}

type preparedTransition struct {
	data   []byte
	result TransitionResult
}

func prepareTransition(t *Ticket, newBody []byte, fromState, state string) (*preparedTransition, error) {
	data, err := renderTransition(t, newBody)
	if err != nil {
		return nil, err
	}
	return &preparedTransition{
		data:   data,
		result: TransitionResult{ID: t.ID, Changed: true, FromState: fromState, State: state},
	}, nil
}

func renderTransition(t *Ticket, newBody []byte) ([]byte, error) {
	data, err := renderUpdated(t, newBody, map[string]bool{"state": true, "assignee": true})
	if err != nil {
		return nil, err
	}
	if len(data) > TaskMaxBytes {
		return nil, contract.NewError(contract.ErrFileTooLarge, "Updated TASK.md exceeds the 1 MiB managed-file limit.", nil)
	}
	if _, err := ParseTicketFile(t.ID, data); err != nil {
		return nil, err
	}
	return data, nil
}

func publishPreparedTransition(st *store.Store, prepared *preparedTransition) (*TransitionResult, error) {
	if !prepared.result.Changed {
		return &prepared.result, nil
	}
	if _, err := st.ReplaceTask(prepared.result.ID, prepared.data, TaskMaxBytes); err != nil {
		return nil, err
	}
	return &prepared.result, nil
}

func prepareClose(st *store.Store, id string, opts CloseOptions) (*preparedTransition, error) {
	full, err := mustResolve(st, id)
	if err != nil {
		return nil, err
	}
	t, err := ReadTicket(st, full)
	if err != nil {
		return nil, err
	}
	if t.State == "completed" {
		return &preparedTransition{result: TransitionResult{ID: full, FromState: "completed", State: "completed"}}, nil
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
	fromState := t.State
	t.State = "completed"
	t.Assignee = ""
	return prepareTransition(t, newBody, fromState, "completed")
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
	prepared := make([]*preparedTransition, 0, len(resolved))
	for _, id := range resolved {
		transition, err := prepareClose(st, id, opts)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, transition)
	}
	result := &BatchTransitionResult{Items: make([]TransitionResult, 0, len(resolved))}
	for _, transition := range prepared {
		closed, err := publishPreparedTransition(st, transition)
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
	prepared, err := prepareClose(st, id, opts)
	if err != nil {
		return nil, err
	}
	return publishPreparedTransition(st, prepared)
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
	fromState := t.State
	t.State = "rejected"
	t.Assignee = ""
	newBody, err := transitionBody(t, map[string]string{"outcome": outcome}, opts.Message, opts.Actor)
	if err != nil {
		return nil, err
	}
	return publishTransition(st, t, newBody, fromState, "rejected")
}

// Review is the human lifecycle correction path. It bypasses ordinary
// lifecycle legality, but an existing assignment may only be changed by its
// effective owner.
func Review(st *store.Store, id string, opts StateOptions) (*TransitionResult, error) {
	return setState(st, id, "review", opts, true)
}

// SetState is the explicit administrative correction path. It intentionally
// bypasses ordinary workflow legality and ownership checks while retaining
// atomic ticket publication and the normal Work-log handling.
func SetState(st *store.Store, id, state string, opts StateOptions) (*TransitionResult, error) {
	return setState(st, id, state, opts, false)
}

func setState(st *store.Store, id, state string, opts StateOptions, enforceOwnership bool) (*TransitionResult, error) {
	if !validLifecycleState(state) {
		return nil, contract.NewError(contract.ErrInvalidArgument,
			"State must be open, hold, review, signoff, completed, or rejected.", nil)
	}
	full, err := mustResolve(st, id)
	if err != nil {
		return nil, err
	}
	t, err := ReadTicket(st, full)
	if err != nil {
		return nil, err
	}
	if enforceOwnership && t.Assignee != "" {
		if err := validateActor(opts.Actor); err != nil {
			return nil, err
		}
		if t.Assignee != opts.Actor {
			return nil, contract.NewError(contract.ErrAlreadyClaimed,
				"The ticket is assigned to another actor.", map[string]any{"id": full})
		}
	}
	if t.State == state && t.Assignee == "" && opts.Message == nil {
		return &TransitionResult{ID: full, Changed: false, FromState: state, State: state}, nil
	}
	fromState := t.State
	t.State = state
	t.Assignee = ""
	newBody, err := transitionBody(t, map[string]string{}, opts.Message, opts.Actor)
	if err != nil {
		return nil, err
	}
	return publishTransition(st, t, newBody, fromState, state)
}

func Approve(st *store.Store, id string, opts ReviewOptions) (*TransitionResult, error) {
	prepared, err := prepareApprove(st, id, opts)
	if err != nil {
		return nil, err
	}
	return publishPreparedTransition(st, prepared)
}

func prepareApprove(st *store.Store, id string, opts ReviewOptions) (*preparedTransition, error) {
	full, err := mustResolve(st, id)
	if err != nil {
		return nil, err
	}
	t, err := ReadTicket(st, full)
	if err != nil {
		return nil, err
	}
	if t.State != "review" {
		return nil, contract.NewError(contract.ErrInvalidTransition, "Cannot approve a ticket in state "+t.State+".", nil)
	}
	if t.Assignee != "" {
		if err := validateActor(opts.Actor); err != nil {
			return nil, err
		}
		if t.Assignee != opts.Actor {
			return nil, contract.NewError(contract.ErrAlreadyClaimed, "The ticket is assigned to another actor.", map[string]any{"id": full})
		}
	}
	fromState := t.State
	t.State, t.Assignee = "signoff", ""
	newBody, err := transitionBody(t, map[string]string{}, opts.Message, opts.Actor)
	if err != nil {
		return nil, err
	}
	return prepareTransition(t, newBody, fromState, "signoff")
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
	prepared := make([]*preparedTransition, 0, len(resolved))
	for _, id := range resolved {
		transition, err := prepareApprove(st, id, opts)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, transition)
	}
	result := &BatchTransitionResult{Items: make([]TransitionResult, 0, len(resolved))}
	for _, transition := range prepared {
		approved, err := publishPreparedTransition(st, transition)
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
	if opts.Claim {
		if err := validateActor(opts.Actor); err != nil {
			return nil, err
		}
	}
	full, err := mustResolve(st, id)
	if err != nil {
		return nil, err
	}
	t, err := ReadTicket(st, full)
	if err != nil {
		return nil, err
	}
	if t.Assignee != "" {
		if err := validateActor(opts.Actor); err != nil {
			return nil, err
		}
		if t.Assignee != opts.Actor {
			return nil, contract.NewError(contract.ErrAlreadyClaimed, "The ticket is assigned to another actor.", map[string]any{"id": full})
		}
	}
	if opts.Claim {
		candidate := *t
		candidate.State = "open"
		candidate.Assignee = ""
		tickets, err := loadGraph(st)
		if err != nil {
			return nil, err
		}
		idx := newGraphIndex(tickets)
		if readiness := readinessForWithChildren(&candidate, idx.byID, idx.children); !readiness.Ready {
			return nil, contract.NewError(contract.ErrDependencyUnresolved,
				"Ticket is not ready to claim.", map[string]any{"blockers": readiness.Blockers})
		}
	}
	sections := map[string]string{}
	if opts.Handoff != nil {
		sections["handoff"] = *opts.Handoff
	}
	newBody, err := transitionBody(t, sections, opts.Message, opts.Actor)
	if err != nil {
		return nil, err
	}
	if !opts.Claim && t.State == "open" && t.Assignee == "" && opts.Handoff == nil && opts.Message == nil {
		return &TransitionResult{ID: full, Changed: false, FromState: "open", State: "open"}, nil
	}
	fromState := t.State
	t.State = "open"
	if opts.Claim {
		t.Assignee = opts.Actor
	} else {
		t.Assignee = ""
	}
	result, err := publishTransition(st, t, newBody, fromState, "open")
	if err != nil {
		return nil, err
	}
	if opts.Claim {
		result.Assignee = opts.Actor
	}
	return result, nil
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
	fromState := t.State
	t.State, t.Assignee = to, ""
	sections := map[string]string{}
	if handoff != nil {
		sections["handoff"] = *handoff
	}
	newBody, err := transitionBody(t, sections, message, actor)
	if err != nil {
		return nil, err
	}
	return publishTransition(st, t, newBody, fromState, to)
}

func transitionBody(t *Ticket, sections map[string]string, message *string, actor string) ([]byte, error) {
	if message != nil {
		workLog, err := uniqueWorkLogSection(t)
		if err != nil {
			return nil, err
		}
		log, err := workLogContent(t, workLog, *message, actor)
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

func validLifecycleState(state string) bool {
	switch state {
	case "open", "hold", "review", "signoff", "completed", "rejected":
		return true
	default:
		return false
	}
}

// uniqueWorkLogSection returns the parsed Work log range. The ordinary ticket
// parser keeps the first duplicate section for compatibility, but a mutation
// that appends history must reject an ambiguous document.
func uniqueWorkLogSection(t *Ticket) (*markdown.Section, error) {
	parsed := markdown.ParseBody(t.Body)
	var found *markdown.Section
	for _, section := range parsed.Sections {
		if section.Key != "work_log" {
			continue
		}
		if found != nil {
			return nil, contract.NewError(contract.ErrInvalidTicket,
				"Ticket contains multiple Work log sections.", nil)
		}
		copy := section
		found = &copy
	}
	return found, nil
}

func workLogContent(t *Ticket, section *markdown.Section, message, actor string) (string, error) {
	if strings.ContainsAny(message, "\r\n") {
		return "", contract.NewError(contract.ErrInvalidArgument,
			"Message must be a single line.", nil)
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "", contract.NewError(contract.ErrInvalidArgument, "Message must not be empty.", nil)
	}
	if markdown.ContainsTopLevelHeading(message) {
		return "", contract.NewError(contract.ErrInvalidArgument,
			"Message must not contain an H1 or H2 heading.", nil)
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "human"
	}
	if err := validateActor(actor); err != nil {
		return "", err
	}
	var entry strings.Builder
	fmt.Fprintf(&entry, "- %s %s: %s\n", time.Now().UTC().Format(time.RFC3339), actor, message)
	existing := ""
	if section != nil {
		existing = strings.TrimRight(section.DisplayContent(t.Body), "\r\n")
	}
	if existing != "" {
		existing += "\n"
	}
	content := existing + entry.String()
	return ValidateSectionContent("work_log", content)
}

func publishTransition(st *store.Store, t *Ticket, newBody []byte, fromState, state string) (*TransitionResult, error) {
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
	return &TransitionResult{ID: t.ID, Changed: true, FromState: fromState, State: state}, nil
}
