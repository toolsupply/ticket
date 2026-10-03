package domain

import (
	"strings"
	"testing"

	"github.com/toolsupply/ticket/internal/contract"
)

func TestHoldReviewPreservesOrdinaryTransitionSemantics(t *testing.T) {
	e := newEnv(t, 9801)
	id := workflowTicket(t, e, "unassigned review hold")
	if _, err := Submit(e.st, id, SubmitOptions{Handoff: stringPtr("Original handoff.")}); err != nil {
		t.Fatal(err)
	}
	result, err := Hold(e.st, id, HoldOptions{
		Actor: "operator", Handoff: stringPtr("Paused for follow-up."), Message: stringPtr("Held review work."),
	})
	if err != nil || !result.Changed || result.FromState != "review" || result.State != "hold" {
		t.Fatalf("review hold result: %+v (%v)", result, err)
	}
	ticket, err := ReadTicket(e.st, id)
	if err != nil || ticket.State != "hold" || ticket.Assignee != "" {
		t.Fatalf("held review ticket: %+v (%v)", ticket, err)
	}
	if strings.TrimSpace(ticket.SectionText("handoff")) != "Paused for follow-up." {
		t.Fatalf("review hold handoff=%q", ticket.SectionText("handoff"))
	}
	if log := ticket.SectionText("work_log"); !strings.Contains(log, "operator: Held review work.") {
		t.Fatalf("review hold work log=%q", log)
	}
	ready, err := ReadyWithOptions(e.st, ReadyOptions{Queue: "review"})
	if err != nil || len(ready.Items) != 0 {
		t.Fatalf("held review ticket remained review-ready: %+v (%v)", ready, err)
	}
}

func TestAssignedReviewOwnerCanHoldWithHandoff(t *testing.T) {
	e := newEnv(t, 9802)
	id := workflowTicket(t, e, "assigned review hold")
	if _, err := Submit(e.st, id, SubmitOptions{Handoff: stringPtr("Review handoff.")}); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(e.st, id, ClaimOptions{Actor: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	result, err := Hold(e.st, id, HoldOptions{
		Actor: "reviewer", Handoff: stringPtr("Revisions are ready."), Message: stringPtr("Pausing review."),
	})
	if err != nil || result.FromState != "review" || result.State != "hold" {
		t.Fatalf("owner review hold: %+v (%v)", result, err)
	}
	ticket, err := ReadTicket(e.st, id)
	if err != nil || ticket.State != "hold" || ticket.Assignee != "" || strings.TrimSpace(ticket.SectionText("handoff")) != "Revisions are ready." {
		t.Fatalf("owner-held review ticket: %+v (%v)", ticket, err)
	}
	if !strings.Contains(ticket.SectionText("work_log"), "reviewer: Pausing review.") {
		t.Fatalf("owner hold work log=%q", ticket.SectionText("work_log"))
	}
}

func TestHoldOwnershipErrorsMatchForOpenAndReview(t *testing.T) {
	for i, initial := range []string{"open", "review"} {
		t.Run(initial, func(t *testing.T) {
			e := newEnv(t, uint64(9803+i))
			id := workflowTicket(t, e, "non-owner hold from "+initial)
			if initial == "review" {
				if _, err := Submit(e.st, id, SubmitOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Claim(e.st, id, ClaimOptions{Actor: "alice"}); err != nil {
				t.Fatal(err)
			}
			if _, err := Hold(e.st, id, HoldOptions{Actor: "bob"}); err == nil || contractCode(t, err) != contract.ErrAlreadyClaimed {
				t.Fatalf("non-owner hold from %s error=%v", initial, err)
			}
			ticket, err := ReadTicket(e.st, id)
			if err != nil || ticket.State != initial || ticket.Assignee != "alice" {
				t.Fatalf("non-owner hold changed ticket: %+v (%v)", ticket, err)
			}
		})
	}
}

func TestHoldStillRejectsOtherSourceStates(t *testing.T) {
	for i, initial := range []string{"hold", "signoff", "closed", "rejected"} {
		t.Run(initial, func(t *testing.T) {
			e := newEnv(t, uint64(9810+i))
			id := workflowTicket(t, e, "hold from "+initial)
			switch initial {
			case "hold":
				if _, err := Hold(e.st, id, HoldOptions{}); err != nil {
					t.Fatal(err)
				}
			case "signoff":
				if _, err := Submit(e.st, id, SubmitOptions{}); err != nil {
					t.Fatal(err)
				}
				if _, err := Approve(e.st, id, ReviewOptions{}); err != nil {
					t.Fatal(err)
				}
			case "closed":
				if _, err := Close(e.st, id, CloseOptions{}); err != nil {
					t.Fatal(err)
				}
			case "rejected":
				if _, err := Reject(e.st, id, RejectOptions{Outcome: "No longer needed."}); err != nil {
					t.Fatal(err)
				}
			}
			before, err := ReadTicket(e.st, id)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Hold(e.st, id, HoldOptions{}); err == nil || contractCode(t, err) != contract.ErrInvalidTransition {
				t.Fatalf("hold from %s error=%v", initial, err)
			}
			after, err := ReadTicket(e.st, id)
			if err != nil || after.State != before.State || after.Assignee != before.Assignee {
				t.Fatalf("invalid hold changed ticket: before=%+v after=%+v err=%v", before, after, err)
			}
		})
	}
}

func TestHoldDependencyRemainsBlocking(t *testing.T) {
	e := newEnv(t, 9820)
	dependency := workflowTicket(t, e, "held dependency")
	blocked := e.create(t, "blocked by held dependency", CreateOptions{
		DependsOn: []string{dependency}, Sections: map[string]string{"objective": "Wait for dependency."},
	})
	independent := workflowTicket(t, e, "independent ready ticket")
	if _, err := Hold(e.st, dependency, HoldOptions{}); err != nil {
		t.Fatal(err)
	}
	ready, err := Ready(e.st, ListOptions{})
	if err != nil || len(ready.Items) != 1 || ready.Items[0].ID != independent {
		t.Fatalf("implementation ready frontier=%+v; blocked=%s independent=%s err=%v", ready, blocked, independent, err)
	}
}
