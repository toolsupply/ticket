package domain

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/toolsupply/ticket/internal/contract"
)

func TestCloseAndOpenLifecycle(t *testing.T) {
	e := newEnv(t, 9501)
	id := e.create(t, "close me", CreateOptions{Sections: map[string]string{"objective": "ship it", "acceptance": "the result is usable"}})
	path := filepath.Join(e.base, "tickets", id, "TASK.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	closed, err := Close(e.st, id, CloseOptions{Outcome: "Shipped."})
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if !closed.Changed || closed.State != "closed" || closed.ID != id {
		t.Fatalf("close result: %+v", closed)
	}
	ticket, err := ReadTicket(e.st, id)
	if err != nil || ticket.State != "closed" || ticket.Assignee != "" || ticket.SectionText("outcome") != "Shipped." {
		t.Fatalf("completed ticket: %+v (%v)", ticket, err)
	}
	completedBytes, _ := os.ReadFile(path)
	noop, err := Close(e.st, id, CloseOptions{Outcome: "ignored"})
	if err != nil || noop.Changed || noop.State != "closed" {
		t.Fatalf("completed close: %+v (%v)", noop, err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, completedBytes) {
		t.Fatal("completed no-op changed bytes")
	}
	opened, err := Open(e.st, id, OpenOptions{})
	if err != nil || !opened.Changed || opened.State != "open" {
		t.Fatalf("open completed ticket: %+v (%v)", opened, err)
	}
	open, err := ReadTicket(e.st, id)
	if err != nil || open.State != "open" || open.Assignee != "" || open.SectionText("outcome") != "Shipped." {
		t.Fatalf("opened ticket: %+v (%v)", open, err)
	}
	if got, _ := os.ReadFile(path); bytes.Equal(got, before) {
		t.Fatal("lifecycle did not publish a changed document")
	}
}

func TestRejectRequiresOutcomeAndCloseIgnoresOpenChildren(t *testing.T) {
	e := newEnv(t, 9502)
	parent := e.create(t, "parent", CreateOptions{Sections: map[string]string{"objective": "x", "acceptance": "x"}})
	child := e.create(t, "child", CreateOptions{Parent: parent, Sections: map[string]string{"objective": "x", "acceptance": "x"}})
	path := filepath.Join(e.base, "tickets", parent, "TASK.md")
	if _, err := Reject(e.st, parent, RejectOptions{}); err == nil || contractCode(t, err) != contract.ErrInvalidArgument {
		t.Fatalf("missing reject outcome: %v", err)
	}
	closed, err := Close(e.st, parent, CloseOptions{Outcome: "done"})
	if err != nil || !closed.Changed || closed.State != "closed" {
		t.Fatalf("close with open child: %+v (%v)", closed, err)
	}
	if got, _ := os.ReadFile(path); len(got) == 0 {
		t.Fatal("closed ticket disappeared")
	}
	if child == "" {
		t.Fatal("child fixture was not created")
	}
}

func TestRejectAcceptsEveryNonterminalState(t *testing.T) {
	for _, initial := range []string{"hold", "signoff"} {
		e := newEnv(t, 9510)
		id := workflowTicket(t, e, "reject "+initial)
		switch initial {
		case "hold":
			if _, err := Hold(e.st, id, HoldOptions{}); err != nil {
				t.Fatalf("hold: %v", err)
			}
		case "signoff":
			if _, err := Submit(e.st, id, SubmitOptions{}); err != nil {
				t.Fatalf("submit: %v", err)
			}
			if _, err := Approve(e.st, id, ReviewOptions{}); err != nil {
				t.Fatalf("approve: %v", err)
			}
		}
		result, err := Reject(e.st, id, RejectOptions{Outcome: "Abandoned from " + initial + "."})
		if err != nil || !result.Changed || result.State != "rejected" {
			t.Fatalf("reject %s: %+v (%v)", initial, result, err)
		}
		ticket, err := ReadTicket(e.st, id)
		if err != nil || ticket.State != "rejected" || ticket.Assignee != "" || ticket.SectionText("outcome") != "Abandoned from "+initial+"." {
			t.Fatalf("rejected %s ticket: %+v (%v)", initial, ticket, err)
		}
	}
}

func TestCloseIgnoresDependenciesAndClearsOwnership(t *testing.T) {
	e := newEnv(t, 9503)
	dep := e.create(t, "dependency", CreateOptions{Sections: map[string]string{"objective": "x", "acceptance": "x"}})
	id := e.create(t, "dependent", CreateOptions{DependsOn: []string{dep}, Sections: map[string]string{"objective": "x", "acceptance": "x"}})
	insertTaskFMLine(t, e.base, id, "assignee: alice\n")
	closed, err := Close(e.st, id, CloseOptions{Outcome: "done"})
	if err != nil || !closed.Changed || closed.State != "closed" {
		t.Fatalf("close dependent: %+v (%v)", closed, err)
	}
	ticket, err := ReadTicket(e.st, id)
	if err != nil || ticket.State != "closed" || ticket.Assignee != "" {
		t.Fatalf("closed dependent: %+v (%v)", ticket, err)
	}
}

func TestCloseManyTargetsAndAll(t *testing.T) {
	e := newEnv(t, 9504)
	first := e.create(t, "first", CreateOptions{Sections: map[string]string{"objective": "first"}})
	second := e.create(t, "second", CreateOptions{Sections: map[string]string{"objective": "second"}})
	third := e.create(t, "third", CreateOptions{Sections: map[string]string{"objective": "third"}})
	result, err := CloseMany(e.st, []string{second, first, second}, CloseOptions{})
	if err != nil || len(result.Items) != 2 {
		t.Fatalf("close many: %+v (%v)", result, err)
	}
	if result.Items[0].ID != first || result.Items[1].ID != second {
		t.Fatalf("close many order: %+v", result.Items)
	}
	for _, id := range []string{first, second} {
		ticket, err := ReadTicket(e.st, id)
		if err != nil || ticket.State != "closed" {
			t.Fatalf("closed %s: %+v (%v)", id, ticket, err)
		}
	}
	all, err := CloseAll(e.st, CloseOptions{Outcome: "finished"})
	if err != nil || len(all.Items) != 1 || all.Items[0].ID != third || all.Items[0].State != "closed" {
		t.Fatalf("close all: %+v (%v)", all, err)
	}
}

func TestCloseManyRejectedPreflightPreservesEarlierTargets(t *testing.T) {
	e := newEnv(t, 9505)
	open := e.create(t, "open", CreateOptions{Sections: map[string]string{"objective": "open"}})
	rejected := e.create(t, "rejected", CreateOptions{Sections: map[string]string{"objective": "rejected"}})
	if _, err := Reject(e.st, rejected, RejectOptions{Outcome: "declined"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(e.base, "tickets", open, "TASK.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CloseMany(e.st, []string{open, rejected}, CloseOptions{}); err == nil || contractCode(t, err) != contract.ErrInvalidTransition {
		t.Fatalf("rejected target accepted: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(e.base, "tickets", open, "TASK.md"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed batch changed open target: %v", err)
	}
}

func TestApproveManyOwnershipConflictPreservesEarlierTargets(t *testing.T) {
	e := newEnv(t, 9506)
	unassigned := workflowTicket(t, e, "unassigned review")
	assigned := workflowTicket(t, e, "assigned review")
	if _, err := Submit(e.st, unassigned, SubmitOptions{}); err != nil {
		t.Fatalf("submit unassigned review: %v", err)
	}
	if _, err := Claim(e.st, assigned, ClaimOptions{Actor: "worker"}); err != nil {
		t.Fatalf("claim assigned review: %v", err)
	}
	if _, err := Submit(e.st, assigned, SubmitOptions{Actor: "worker"}); err != nil {
		t.Fatalf("submit assigned review: %v", err)
	}
	insertTaskFMLine(t, e.base, assigned, "assignee: other\n")
	firstBefore, err := os.ReadFile(filepath.Join(e.base, "tickets", unassigned, "TASK.md"))
	if err != nil {
		t.Fatal(err)
	}
	secondBefore, err := os.ReadFile(filepath.Join(e.base, "tickets", assigned, "TASK.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApproveMany(e.st, []string{unassigned, assigned}, ReviewOptions{Actor: "reviewer"}); err == nil || contractCode(t, err) != contract.ErrAlreadyClaimed {
		t.Fatalf("assigned target accepted: %v", err)
	}
	firstAfter, err := os.ReadFile(filepath.Join(e.base, "tickets", unassigned, "TASK.md"))
	if err != nil || !bytes.Equal(firstBefore, firstAfter) {
		t.Fatalf("ownership conflict changed earlier target: %v", err)
	}
	secondAfter, err := os.ReadFile(filepath.Join(e.base, "tickets", assigned, "TASK.md"))
	if err != nil || !bytes.Equal(secondBefore, secondAfter) {
		t.Fatalf("ownership conflict changed later target: %v", err)
	}
}

func TestCloseManyWorkLogErrorPreservesEarlierTargets(t *testing.T) {
	e := newEnv(t, 9507)
	first := e.create(t, "first close", CreateOptions{Sections: map[string]string{"objective": "first"}})
	second := e.create(t, "second close", CreateOptions{Sections: map[string]string{"objective": "second"}})
	secondPath := filepath.Join(e.base, "tickets", second, "TASK.md")
	secondBody, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	secondBody = append(secondBody, []byte("\n## Work log\n\n- first\n\n## Work log\n\n- second\n")...)
	if _, err := e.st.ReplaceTask(second, secondBody, TaskMaxBytes); err != nil {
		t.Fatalf("seed duplicate Work log: %v", err)
	}
	firstPath := filepath.Join(e.base, "tickets", first, "TASK.md")
	firstBefore, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CloseMany(e.st, []string{first, second}, CloseOptions{Message: stringPtr("close batch")}); err == nil || contractCode(t, err) != contract.ErrInvalidTicket {
		t.Fatalf("duplicate Work log accepted: %v", err)
	}
	firstAfter, err := os.ReadFile(firstPath)
	if err != nil || !bytes.Equal(firstBefore, firstAfter) {
		t.Fatalf("Work log error changed earlier target: %v", err)
	}
}
