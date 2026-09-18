package domain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ticket/internal/contract"
)

func workflowTicket(t *testing.T, e *testEnv, title string) string {
	t.Helper()
	return e.create(t, title, CreateOptions{Sections: map[string]string{
		"objective":  "Implement the requested work.",
		"acceptance": "The requested work is complete.",
	}})
}

func TestWorkflowStatesAndTransitions(t *testing.T) {
	e := newEnv(t, 9601)
	id := workflowTicket(t, e, "workflow")

	if _, err := Submit(e.st, id, SubmitOptions{Actor: "alice", Handoff: stringPtr("Please review the implementation.")}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	ticket, err := ReadTicket(e.st, id)
	if err != nil || ticket.State != "review" || ticket.Assignee != "" || ticket.SectionText("handoff") != "Please review the implementation." {
		t.Fatalf("submitted ticket: %+v (%v)", ticket, err)
	}
	if _, err := Claim(e.st, id, ClaimOptions{Actor: "reviewer"}); err != nil {
		t.Fatalf("review claim: %v", err)
	}
	if _, err := Approve(e.st, id, ReviewOptions{Actor: "reviewer"}); err != nil {
		t.Fatalf("approve review: %v", err)
	}
	ticket, _ = ReadTicket(e.st, id)
	if ticket.State != "signoff" || ticket.Assignee != "" {
		t.Fatalf("signoff after review: %+v", ticket)
	}
	if _, err := Close(e.st, id, CloseOptions{Outcome: "Accepted."}); err != nil {
		t.Fatalf("close signoff: %v", err)
	}
	ticket, _ = ReadTicket(e.st, id)
	if ticket.State != "completed" || ticket.Assignee != "" || ticket.SectionText("outcome") != "Accepted." {
		t.Fatalf("completed signoff: %+v", ticket)
	}

	hold := workflowTicket(t, e, "hold")
	if _, err := Claim(e.st, hold, ClaimOptions{Actor: "alice"}); err != nil {
		t.Fatalf("hold claim: %v", err)
	}
	if _, err := Hold(e.st, hold, HoldOptions{Actor: "alice"}); err != nil {
		t.Fatalf("hold: %v", err)
	}
	ticket, _ = ReadTicket(e.st, hold)
	if ticket.State != "hold" || ticket.Assignee != "" {
		t.Fatalf("held ticket: %+v", ticket)
	}
	if _, err := Claim(e.st, hold, ClaimOptions{Actor: "alice"}); err == nil || contractCode(t, err) != contract.ErrInvalidTransition {
		t.Fatalf("held ticket was claimable: %v", err)
	}
	if _, err := Open(e.st, hold, OpenOptions{}); err != nil {
		t.Fatalf("open held ticket: %v", err)
	}
	ticket, _ = ReadTicket(e.st, hold)
	if ticket.State != "open" || ticket.Assignee != "" {
		t.Fatalf("opened held ticket: %+v", ticket)
	}
}

func TestWorkflowMessagesAppendWorkLogWithoutReplacingHandoff(t *testing.T) {
	e := newEnv(t, 9603)
	id := workflowTicket(t, e, "work log")
	if _, err := Submit(e.st, id, SubmitOptions{
		Actor:   "codex",
		Handoff: stringPtr("Please verify the retry path."),
		Message: stringPtr("Implemented retry handling."),
	}); err != nil {
		t.Fatalf("submit with message: %v", err)
	}
	if _, err := Claim(e.st, id, ClaimOptions{Actor: "reviewer"}); err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if _, err := Approve(e.st, id, ReviewOptions{
		Actor:   "reviewer",
		Message: stringPtr("Reviewed the retry handling."),
	}); err != nil {
		t.Fatalf("approve with message: %v", err)
	}
	ticket, err := ReadTicket(e.st, id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(ticket.SectionText("handoff")) != "Please verify the retry path." {
		t.Fatalf("message changed Handoff: %q", ticket.SectionText("handoff"))
	}
	log := ticket.SectionText("work_log")
	for _, want := range []string{"codex: Implemented retry handling.", "reviewer: Reviewed the retry handling."} {
		if !strings.Contains(log, want) {
			t.Fatalf("Work log missing %q: %q", want, log)
		}
	}
	if strings.Count(log, "- ") != 2 || !strings.Contains(log, "T") || !strings.Contains(log, "Z") {
		t.Fatalf("Work log lacks two timestamped entries: %q", log)
	}
	if _, err := Update(e.st, id, UpdateOptions{Sections: map[string]string{"work_log": "replace"}}); err == nil || contractCode(t, err) != contract.ErrInvalidArgument {
		t.Fatalf("direct Work log replacement was accepted: %v", err)
	}
}

func TestWorkLogAppendUsesCanonicalHeadingAndNormalizesEOF(t *testing.T) {
	e := newEnv(t, 9604)
	legacy := e.create(t, "legacy log", CreateOptions{Sections: map[string]string{"objective": "Do the work."}})
	legacyBytes := []byte("---\nstate: open\npriority: 2\n---\n# legacy log\n\n## Objective\n\nDo the work.\n\n## Work Log\nold entry")
	if _, err := e.st.ReplaceTask(legacy, legacyBytes, TaskMaxBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := Submit(e.st, legacy, SubmitOptions{Message: stringPtr("new entry")}); err != nil {
		t.Fatalf("append to legacy heading: %v", err)
	}
	legacyTicket, err := ReadTicket(e.st, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(legacyTicket.FileBytes), "## Work Log\n") {
		t.Fatalf("legacy Work Log heading was not read compatibly: %q", legacyTicket.FileBytes)
	}
	if !strings.HasSuffix(string(legacyTicket.FileBytes), "\n\n") || strings.HasSuffix(string(legacyTicket.FileBytes), "\n\n\n") {
		t.Fatalf("legacy log EOF was not normalized: %q", legacyTicket.FileBytes)
	}

	canonical := e.create(t, "new log", CreateOptions{Sections: map[string]string{"objective": "Do more work."}})
	canonicalBytes := []byte("---\nstate: open\npriority: 2\n---\n# new log\n\n## Objective\n\nDo more work.\n\n\n")
	if _, err := e.st.ReplaceTask(canonical, canonicalBytes, TaskMaxBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := Submit(e.st, canonical, SubmitOptions{Message: stringPtr("first entry")}); err != nil {
		t.Fatalf("append new heading: %v", err)
	}
	canonicalTicket, err := ReadTicket(e.st, canonical)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(canonicalTicket.FileBytes), "## Work log\n") {
		t.Fatalf("new Work log heading is not canonical: %q", canonicalTicket.FileBytes)
	}
	if !strings.HasSuffix(string(canonicalTicket.FileBytes), "\n\n") || strings.HasSuffix(string(canonicalTicket.FileBytes), "\n\n\n") {
		t.Fatalf("new log EOF was not normalized: %q", canonicalTicket.FileBytes)
	}
}

func TestWorkLogAppendBeforeLaterSections(t *testing.T) {
	e := newEnv(t, 9605)
	id := e.create(t, "non-final work log", CreateOptions{Sections: map[string]string{"objective": "Do the work."}})
	data := []byte("---\nstate: open\npriority: 2\n---\n# non-final work log\n\n## Objective\n\nDo the work.\n\n## Handoff\n\nOld handoff.\n\n## Work log\n\n- old entry\n\n## Outcome\n\nExisting outcome text.\n\n## Notes\n\nKeep this custom section.\n")
	if _, err := e.st.ReplaceTask(id, data, TaskMaxBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := Submit(e.st, id, SubmitOptions{Handoff: stringPtr("New handoff."), Message: stringPtr("new entry")}); err != nil {
		t.Fatalf("append non-final Work log: %v", err)
	}
	ticket, err := ReadTicket(e.st, id)
	if err != nil {
		t.Fatal(err)
	}
	body := string(ticket.FileBytes)
	work := strings.Index(body, "## Work log")
	outcome := strings.Index(body, "## Outcome")
	notes := strings.Index(body, "## Notes")
	entry := strings.Index(body, "new entry")
	if work < 0 || outcome < 0 || notes < 0 || entry <= work || entry >= outcome {
		t.Fatalf("new entry was not inserted into Work log: %q", body)
	}
	if strings.Count(body, "new entry") != 1 || !strings.Contains(body, "New handoff.") || strings.Contains(body, "Old handoff.") || !strings.Contains(body, "Existing outcome text.") || !strings.Contains(body, "Keep this custom section.") {
		t.Fatalf("later content was changed or entry duplicated: %q", body)
	}
}

func TestWorkLogAppendRejectsDuplicateAndStructuralMessages(t *testing.T) {
	e := newEnv(t, 9606)
	id := e.create(t, "ambiguous work log", CreateOptions{Sections: map[string]string{"objective": "Do the work."}})
	data := []byte("---\nstate: open\npriority: 2\n---\n# ambiguous work log\n\n## Work log\n\n- first\n\n## Outcome\n\nold\n\n## Work log\n\n- second\n")
	if _, err := e.st.ReplaceTask(id, data, TaskMaxBytes); err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), data...)
	if _, err := Submit(e.st, id, SubmitOptions{Message: stringPtr("new entry")}); err == nil || contractCode(t, err) != contract.ErrInvalidTicket {
		t.Fatalf("duplicate Work log accepted: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(e.st.Root, id, "TASK.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("duplicate Work log mutation changed bytes")
	}

	clean := e.create(t, "message validation", CreateOptions{Sections: map[string]string{"objective": "Do the work."}})
	original, err := os.ReadFile(filepath.Join(e.st.Root, clean, "TASK.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{"# injected", "## Outcome", "first line\nsecond line"} {
		if _, err := Submit(e.st, clean, SubmitOptions{Message: stringPtr(message)}); err == nil || contractCode(t, err) != contract.ErrInvalidArgument {
			t.Fatalf("unsafe message %q accepted: %v", message, err)
		}
		current, readErr := os.ReadFile(filepath.Join(e.st.Root, clean, "TASK.md"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(current) != string(original) {
			t.Fatalf("unsafe message %q changed bytes", message)
		}
	}
	if _, err := Update(e.st, clean, UpdateOptions{Sections: map[string]string{"objective": "safe\n## Work log"}}); err == nil || contractCode(t, err) != contract.ErrInvalidArgument {
		t.Fatalf("structured Work log heading was accepted: %v", err)
	}
	current, err := os.ReadFile(filepath.Join(e.st.Root, clean, "TASK.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(original) {
		t.Fatal("structured Work log heading changed ticket bytes")
	}
}

func TestReviewAndListStates(t *testing.T) {
	e := newEnv(t, 9602)
	id := workflowTicket(t, e, "review")
	if _, err := Submit(e.st, id, SubmitOptions{}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := Claim(e.st, id, ClaimOptions{Actor: "reviewer"}); err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if _, err := Open(e.st, id, OpenOptions{Actor: "reviewer", Handoff: stringPtr("Please revise the edge case.")}); err != nil {
		t.Fatalf("open review: %v", err)
	}
	ticket, err := ReadTicket(e.st, id)
	if err != nil || ticket.State != "open" || ticket.Assignee != "" || ticket.SectionText("handoff") != "Please revise the edge case." {
		t.Fatalf("opened ticket: %+v (%v)", ticket, err)
	}
	if _, err := Update(e.st, id, UpdateOptions{Set: map[string]any{"state": "review"}}); err == nil {
		t.Fatal("ordinary update changed workflow state")
	}

	hold := workflowTicket(t, e, "held")
	if _, err := Hold(e.st, hold, HoldOptions{}); err != nil {
		t.Fatalf("hold unassigned: %v", err)
	}
	review, err := List(e.st, ListOptions{State: "review"})
	if err != nil || len(review.Items) != 0 {
		t.Fatalf("review list: %+v (%v)", review, err)
	}
	held, err := List(e.st, ListOptions{State: "hold"})
	if err != nil || len(held.Items) != 1 || held.Items[0].ID != hold {
		t.Fatalf("hold list: %+v (%v)", held, err)
	}
	if _, err := List(e.st, ListOptions{State: "signoff"}); err != nil {
		t.Fatalf("signoff list: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.base, "tickets", id, "TASK.md")); err != nil {
		t.Fatal(err)
	}
}

func TestOpenCommandReturnsAnyStateToImplementation(t *testing.T) {
	e := newEnv(t, 9705)
	for _, state := range []string{"hold", "review", "signoff", "completed"} {
		id := workflowTicket(t, e, "open "+state)
		switch state {
		case "hold":
			if _, err := Hold(e.st, id, HoldOptions{}); err != nil {
				t.Fatal(err)
			}
		case "review":
			if _, err := Submit(e.st, id, SubmitOptions{}); err != nil {
				t.Fatal(err)
			}
		case "signoff":
			if _, err := Submit(e.st, id, SubmitOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := Approve(e.st, id, ReviewOptions{}); err != nil {
				t.Fatal(err)
			}
		case "completed":
			if _, err := Close(e.st, id, CloseOptions{Outcome: "done"}); err != nil {
				t.Fatal(err)
			}
		}
		result, err := Open(e.st, id, OpenOptions{Handoff: stringPtr("Continue implementation.")})
		if err != nil || !result.Changed || result.State != "open" {
			t.Fatalf("open %s: %+v (%v)", state, result, err)
		}
		ticket, err := ReadTicket(e.st, id)
		if err != nil || ticket.State != "open" || ticket.Assignee != "" || strings.TrimSpace(ticket.SectionText("handoff")) != "Continue implementation." {
			t.Fatalf("opened %s ticket: %+v (%v)", state, ticket, err)
		}
	}
}

func TestBatchApprovalThenIndividualClose(t *testing.T) {
	e := newEnv(t, 9604)
	ids := []string{workflowTicket(t, e, "batch one"), workflowTicket(t, e, "batch two")}
	for _, id := range ids {
		if _, err := Submit(e.st, id, SubmitOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	approved, err := ApproveAll(e.st, ReviewOptions{})
	if err != nil || len(approved.Items) != 2 {
		t.Fatalf("approve all: %+v (%v)", approved, err)
	}
	for _, id := range ids {
		ticket, err := ReadTicket(e.st, id)
		if err != nil || ticket.State != "signoff" || ticket.Assignee != "" {
			t.Fatalf("batch signoff %s: %+v", id, ticket)
		}
		if _, err := Close(e.st, id, CloseOptions{}); err != nil {
			t.Fatalf("close %s: %v", id, err)
		}
		ticket, err = ReadTicket(e.st, id)
		if err != nil || ticket.State != "completed" {
			t.Fatalf("batch completed %s: %+v", id, ticket)
		}
	}
}

func stringPtr(value string) *string { return &value }
