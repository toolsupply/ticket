package domain

import (
	"ticket/internal/contract"
	"ticket/internal/store"
)

// ClaimOptions contains the actor performing the claim.
// Ownership is advisory metadata; it is not a lease or a workflow phase.
type ClaimOptions struct {
	Actor string
}

type ClaimResult struct {
	ID       string `json:"id"`
	Changed  bool   `json:"changed"`
	Assignee string `json:"assignee"`
}

type ReleaseOptions struct {
	Actor   string
	Handoff *string
	Message *string
}

type ReleaseResult struct {
	ID      string `json:"id"`
	Changed bool   `json:"changed"`
}

func Claim(st *store.Store, id string, opts ClaimOptions) (*ClaimResult, error) {
	if err := validateActor(opts.Actor); err != nil {
		return nil, err
	}
	full, err := mustResolve(st, id)
	if err != nil {
		return nil, err
	}
	t, err := ReadTicket(st, full)
	if err != nil {
		return nil, err
	}
	if t.State != "open" && t.State != "review" {
		return nil, contract.NewError(contract.ErrInvalidTransition, "Only open or review tickets can be claimed.", nil)
	}
	if t.Assignee != "" {
		if t.Assignee == opts.Actor {
			return &ClaimResult{ID: full, Changed: false, Assignee: t.Assignee}, nil
		}
		return nil, contract.NewError(contract.ErrAlreadyClaimed, "Ticket is assigned to another actor.", map[string]any{"id": full})
	}
	if t.State == "open" {
		readiness, err := Readiness(st, full)
		if err != nil {
			return nil, err
		}
		if !readiness.Ready {
			return nil, contract.NewError(contract.ErrDependencyUnresolved, "Ticket is not ready to claim.", map[string]any{"blockers": readiness.Blockers})
		}
	}

	t.Assignee = opts.Actor
	data, err := renderUpdated(t, t.Body, map[string]bool{"assignee": true})
	if err != nil {
		return nil, err
	}
	if len(data) > TaskMaxBytes {
		return nil, contract.NewError(contract.ErrFileTooLarge, "Updated TASK.md exceeds the 1 MiB managed-file limit.", nil)
	}
	if _, err := ParseTicketFile(full, data); err != nil {
		return nil, err
	}
	_, err = st.ReplaceTask(full, data, TaskMaxBytes)
	if err != nil {
		return nil, err
	}
	return &ClaimResult{ID: full, Changed: true, Assignee: opts.Actor}, nil
}

func Release(st *store.Store, id string, opts ReleaseOptions) (*ReleaseResult, error) {
	if err := validateActor(opts.Actor); err != nil {
		return nil, err
	}
	full, err := mustResolve(st, id)
	if err != nil {
		return nil, err
	}
	t, err := ReadTicket(st, full)
	if err != nil {
		return nil, err
	}
	if (t.State != "open" && t.State != "review") || t.Assignee == "" {
		return nil, contract.NewError(contract.ErrInvalidTransition, "Only an assigned open or review ticket can be released.", nil)
	}
	if t.Assignee != opts.Actor {
		return nil, contract.NewError(contract.ErrAlreadyClaimed, "The ticket is assigned to another actor.", map[string]any{"id": full})
	}
	t.Assignee = ""
	sections := map[string]string{}
	if opts.Handoff != nil {
		sections["handoff"] = *opts.Handoff
	}
	newBody, err := transitionBody(t, sections, opts.Message, opts.Actor)
	if err != nil {
		return nil, err
	}
	data, err := renderUpdated(t, newBody, map[string]bool{"assignee": true})
	if err != nil {
		return nil, err
	}
	if len(data) > TaskMaxBytes {
		return nil, contract.NewError(contract.ErrFileTooLarge, "Updated TASK.md exceeds the 1 MiB managed-file limit.", nil)
	}
	if _, err := ParseTicketFile(full, data); err != nil {
		return nil, err
	}
	_, err = st.ReplaceTask(full, data, TaskMaxBytes)
	if err != nil {
		return nil, err
	}
	return &ReleaseResult{ID: full, Changed: true}, nil
}

func validateActor(actor string) error {
	if actor == "" || !assigneePattern.MatchString(actor) {
		return contract.NewError(contract.ErrMissingActor, "An actor is required.", nil)
	}
	return nil
}
