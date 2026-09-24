package domain

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toolsupply/ticket/internal/contract"
)

func TestGraphMutationRejectsDirectAndTransitiveCycles(t *testing.T) {
	e := newEnv(t, 9401)
	a := e.create(t, "a", CreateOptions{})
	b := e.create(t, "b", CreateOptions{DependsOn: []string{a}})
	c := e.create(t, "c", CreateOptions{DependsOn: []string{b}})
	for _, tc := range []struct {
		name string
		id   string
		set  map[string]any
	}{
		{"direct", a, map[string]any{"depends_on": []any{b}}},
		{"transitive", a, map[string]any{"depends_on": []any{c}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			beforePath := filepath.Join(e.base, "tickets", tc.id, "TASK.md")
			before, err := os.ReadFile(beforePath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Update(e.st, tc.id, UpdateOptions{Set: tc.set}); err == nil {
				t.Fatal("cyclic dependency accepted")
			} else if got := contractCode(t, err); got != contract.ErrDependencyCycle {
				t.Fatalf("error=%s want dependency_cycle", got)
			}
			after, err := os.ReadFile(beforePath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("rejected graph mutation changed bytes")
			}
		})
	}
	beforePath := filepath.Join(e.base, "tickets", b, "TASK.md")
	before, err := os.ReadFile(beforePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Update(e.st, b, UpdateOptions{Set: map[string]any{"depends_on": []any{b}}}); err == nil || contractCode(t, err) != contract.ErrInvalidArgument {
		t.Fatalf("self dependency result: %v", err)
	}
	after, _ := os.ReadFile(beforePath)
	if !bytes.Equal(before, after) {
		t.Fatal("rejected self reference changed bytes")
	}

	// Parent cycles are checked separately from dependency cycles.
	root := e.create(t, "root", CreateOptions{})
	child := e.create(t, "child", CreateOptions{Parent: root})
	grand := e.create(t, "grand", CreateOptions{Parent: child})
	rootPath := filepath.Join(e.base, "tickets", root, "TASK.md")
	rootBefore, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Update(e.st, root, UpdateOptions{Set: map[string]any{"parent": grand}}); err == nil {
		t.Fatal("parent cycle accepted")
	} else if got := contractCode(t, err); got != contract.ErrParentCycle {
		t.Fatalf("error=%s want parent_cycle", got)
	}
	rootAfter, err := os.ReadFile(rootPath)
	if err != nil || !bytes.Equal(rootBefore, rootAfter) {
		t.Fatal("rejected parent mutation changed bytes")
	}
}

func TestGraphMutationRejectsCombinedReadinessCycles(t *testing.T) {
	e := newEnv(t, 9407)
	parent := e.create(t, "parent", CreateOptions{})
	child := e.create(t, "child", CreateOptions{Parent: parent})
	childPath := filepath.Join(e.base, "tickets", child, "TASK.md")
	before, err := os.ReadFile(childPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Update(e.st, child, UpdateOptions{Set: map[string]any{"depends_on": []any{parent}}}); err == nil {
		t.Fatal("direct combined readiness cycle accepted")
	} else if got := contractCode(t, err); got != contract.ErrReadinessCycle {
		t.Fatalf("error=%s want readiness_cycle", got)
	}
	after, err := os.ReadFile(childPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected combined cycle changed bytes")
	}

	first := e.create(t, "first", CreateOptions{})
	second := e.create(t, "second", CreateOptions{Parent: first})
	third := e.create(t, "third", CreateOptions{Parent: second})
	thirdPath := filepath.Join(e.base, "tickets", third, "TASK.md")
	before, err = os.ReadFile(thirdPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Update(e.st, third, UpdateOptions{Set: map[string]any{"depends_on": []any{first}}}); err == nil {
		t.Fatal("transitive combined readiness cycle accepted")
	} else if got := contractCode(t, err); got != contract.ErrReadinessCycle {
		t.Fatalf("error=%s want readiness_cycle", got)
	}
	after, err = os.ReadFile(thirdPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected transitive combined cycle changed bytes")
	}
}

func TestValidateGraphsDiagnosesPersistedCombinedReadinessCycle(t *testing.T) {
	e := newEnv(t, 9408)
	parent := e.create(t, "parent", CreateOptions{})
	child := e.create(t, "child", CreateOptions{Parent: parent})
	insertTaskFMLine(t, e.base, child, "depends_on: ["+parent+"]\n")
	if err := ValidateGraphs(e.st); err == nil || contractCode(t, err) != contract.ErrReadinessCycle {
		t.Fatalf("persisted combined cycle diagnosis: %v", err)
	}
}

func TestValidateGraphsDiagnosesDuplicateDependencies(t *testing.T) {
	e := newEnv(t, 9409)
	dependency := e.create(t, "dependency", CreateOptions{})
	target := e.create(t, "target", CreateOptions{})
	insertTaskFMLine(t, e.base, target, "depends_on: ["+dependency+", "+dependency+"]\n")
	if err := ValidateGraphs(e.st); err == nil || contractCode(t, err) != contract.ErrInvalidTicket {
		t.Fatalf("duplicate dependency was not diagnosed: %v", err)
	}
}

func TestReadinessPrerequisiteStatesAndChildren(t *testing.T) {
	e := newEnv(t, 9402)
	dep := e.create(t, "dependency", CreateOptions{Sections: map[string]string{"objective": "dependency"}})
	dependent := e.create(t, "dependent", CreateOptions{DependsOn: []string{dep}, Sections: map[string]string{
		"objective": "Do the work.", "acceptance": "It is done.",
	}})
	r, err := Readiness(e.st, dependent)
	if err != nil {
		t.Fatal(err)
	}
	if r.Ready || !hasBlocker(r, "dependency_open", dep) {
		t.Fatalf("open dependency readiness: %+v", r)
	}
	if err := setStateLine(t, e.base, dep, "- State: open\n", "- State: completed\n"); err != nil {
		t.Fatal(err)
	}
	r, err = Readiness(e.st, dependent)
	if err != nil || !r.Ready {
		t.Fatalf("completed dependency readiness: %+v (%v)", r, err)
	}
	if err := setStateLine(t, e.base, dep, "- State: completed\n", "- State: rejected\n"); err != nil {
		t.Fatal(err)
	}
	r, err = Readiness(e.st, dependent)
	if err != nil || r.Ready || !hasBlocker(r, "dependency_rejected", dep) {
		t.Fatalf("rejected dependency readiness: %+v (%v)", r, err)
	}

	// A missing reference is relevant readiness data, but does not mutate
	// the dependent while it is being inspected.
	p := filepath.Join(e.base, "tickets", dependent, "TASK.md")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	missing := "20010101-99999"
	data = bytes.Replace(data, []byte("- Depends on: "+dep), []byte("- Depends on: "+missing), 1)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	r, err = Readiness(e.st, dependent)
	if err != nil || r.Ready || !hasBlocker(r, "dependency_missing", missing) {
		t.Fatalf("missing dependency readiness: %+v (%v)", r, err)
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Fatal("readiness changed repository bytes")
	}

	// Direct nonterminal children block; terminal children do not. The relationship
	// is independent from dependency readiness.
	parent := e.create(t, "parent", CreateOptions{Sections: map[string]string{
		"objective": "Parent work.", "acceptance": "Parent is done.",
	}})
	child := e.create(t, "child", CreateOptions{Parent: parent, Sections: map[string]string{"objective": "child work"}})
	if _, err := Update(e.st, parent, UpdateOptions{Set: map[string]any{"depends_on": []any{child}}}); err != nil {
		t.Fatalf("parent depending on child should be accepted: %v", err)
	}
	r, err = Readiness(e.st, parent)
	if err != nil || r.Ready || !hasBlocker(r, "dependency_open", child) || !hasBlocker(r, "open_children", child) {
		t.Fatalf("open child readiness: %+v (%v)", r, err)
	}
	if err := setStateLine(t, e.base, child, "- State: open\n", "- State: completed\n"); err != nil {
		t.Fatal(err)
	}
	r, err = Readiness(e.st, parent)
	if err != nil || !r.Ready {
		t.Fatalf("completed child readiness: %+v (%v)", r, err)
	}
	previous := "completed"
	for _, state := range []string{"hold", "review", "signoff"} {
		if err := setStateLine(t, e.base, child, "- State: "+previous+"\n", "- State: "+state+"\n"); err != nil {
			t.Fatal(err)
		}
		r, err = Readiness(e.st, parent)
		if err != nil || r.Ready || !hasBlocker(r, "open_children", child) {
			t.Fatalf("%s child readiness: %+v (%v)", state, r, err)
		}
		previous = state
	}
	if err := setStateLine(t, e.base, child, "- State: "+previous+"\n", "- State: rejected\n"); err != nil {
		t.Fatal(err)
	}
	r, err = Readiness(e.st, parent)
	if err != nil || r.Ready || !hasBlocker(r, "dependency_rejected", child) {
		t.Fatalf("rejected child readiness: %+v (%v)", r, err)
	}
}

func TestReadinessRejectsMalformedMetadata(t *testing.T) {
	e := newEnv(t, 9405)
	target := e.create(t, "target", CreateOptions{Sections: map[string]string{"objective": "t", "acceptance": "t"}})
	bad := e.create(t, "bad", CreateOptions{})
	badPath := filepath.Join(e.base, "tickets", bad, "TASK.md")
	badBefore, _ := os.ReadFile(badPath)
	badData := bytes.Replace(badBefore, []byte("- Priority: P2\n"), []byte("- Priority: wrong\n"), 1)
	if bytes.Equal(badBefore, badData) {
		badData = append([]byte("- Priority: wrong\n"), badBefore...)
	}
	if err := os.WriteFile(badPath, badData, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Readiness(e.st, target); err == nil || contractCode(t, err) != contract.ErrInvalidTicket {
		t.Fatalf("malformed graph metadata error: %v", err)
	}
	if err := ValidateGraphs(e.st); err == nil || contractCode(t, err) != contract.ErrInvalidTicket {
		t.Fatalf("check accepted malformed ticket: %v", err)
	}
	e2 := newEnv(t, 9406)
	bodyTicket := e2.create(t, "body error", CreateOptions{})
	bodyPath := filepath.Join(e2.base, "tickets", bodyTicket, "TASK.md")
	bodyBefore, err := os.ReadFile(bodyPath)
	if err != nil {
		t.Fatal(err)
	}
	bodyAfter := bytes.Replace(bodyBefore, []byte("## Objective\n\n"), []byte("## Objective\n\n## objective\n"), 1)
	if err := os.WriteFile(bodyPath, bodyAfter, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGraphs(e2.st); err != nil {
		t.Fatalf("check rejected nonfatal duplicate section: %v", err)
	}
	if got, err := os.ReadFile(bodyPath); err != nil || !bytes.Equal(got, bodyAfter) || bytes.Equal(got, bodyBefore) {
		t.Fatalf("check changed malformed body: %v", err)
	}
}

func TestContentUpdateIgnoresDisconnectedCycle(t *testing.T) {
	e := newEnv(t, 9410)
	a := e.create(t, "a", CreateOptions{})
	b := e.create(t, "b", CreateOptions{})
	c := e.create(t, "c", CreateOptions{})
	insertTaskFMLine(t, e.base, b, "depends_on: ["+c+"]\n")
	insertTaskFMLine(t, e.base, c, "depends_on: ["+b+"]\n")
	if _, err := Update(e.st, a, UpdateOptions{Set: map[string]any{"title": "a changed"}}); err != nil {
		t.Fatalf("content update blocked by disconnected cycle: %v", err)
	}
}

func TestGraphReadsDistinguishMissingTicketFromDamagedDirectory(t *testing.T) {
	e := newEnv(t, 9411)
	target := e.create(t, "target", CreateOptions{Sections: map[string]string{"objective": "x", "acceptance": "x"}})
	damaged := e.create(t, "damaged", CreateOptions{})
	damagedPath := filepath.Join(e.base, "tickets", damaged, "TASK.md")
	if err := os.Remove(damagedPath); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(e.base, "tickets", target, "TASK.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Readiness(e.st, target); err == nil || contractCode(t, err) != contract.ErrInvalidTicket {
		t.Fatalf("damaged directory readiness error: %v", err)
	}
	if err := ValidateGraphs(e.st); err == nil || contractCode(t, err) != contract.ErrInvalidTicket {
		t.Fatalf("damaged directory graph error: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(e.base, "tickets", target, "TASK.md"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("graph reads changed authoritative data")
	}

	e2 := newEnv(t, 9412)
	dep := e2.create(t, "dep", CreateOptions{})
	dependent := e2.create(t, "dependent", CreateOptions{DependsOn: []string{dep}, Sections: map[string]string{"objective": "x", "acceptance": "x"}})
	if err := os.RemoveAll(filepath.Join(e2.base, "tickets", dep)); err != nil {
		t.Fatal(err)
	}
	r, err := Readiness(e2.st, dependent)
	if err != nil || r.Ready || !hasBlocker(r, "dependency_missing", dep) {
		t.Fatalf("removed prerequisite readiness: %+v (%v)", r, err)
	}
	if err := ValidateGraphs(e2.st); err == nil || contractCode(t, err) != contract.ErrDanglingReference {
		t.Fatalf("check did not report dangling prerequisite: %v", err)
	}
	e3 := newEnv(t, 9415)
	child := e3.create(t, "child", CreateOptions{})
	missingParent := "20260101-00000"
	insertTaskFMLine(t, e3.base, child, "parent: "+missingParent+"\n")
	if err := ValidateGraphs(e3.st); err == nil || contractCode(t, err) != contract.ErrDanglingReference {
		t.Fatalf("check did not report dangling parent: %v", err)
	}
}

func TestCheckDiagnosesTicketShapedNoncanonicalDirectories(t *testing.T) {
	e := newEnv(t, 9413)
	if err := os.Mkdir(filepath.Join(e.st.Root, "unrelated"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGraphs(e.st); err != nil {
		t.Fatalf("unrelated directory affected check: %v", err)
	}
	if err := os.Mkdir(filepath.Join(e.st.Root, "20260913-not-canonical"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGraphs(e.st); err == nil || contractCode(t, err) != contract.ErrInvalidTicket {
		t.Fatalf("check did not diagnose noncanonical ticket directory: %v", err)
	}
}

func TestReadinessActionabilityAndBlockerOrder(t *testing.T) {
	e := newEnv(t, 9414)
	target := e.create(t, "target", CreateOptions{Sections: map[string]string{"objective": "target"}})
	dep := e.create(t, "dependency", CreateOptions{Sections: map[string]string{"objective": "dependency"}})
	child := e.create(t, "child", CreateOptions{Parent: target, Sections: map[string]string{"objective": "child"}})
	child2 := e.create(t, "child2", CreateOptions{Parent: target, Sections: map[string]string{"objective": "child2"}})
	insertTaskFMLine(t, e.base, target, "assignee: agent\n")
	insertTaskFMLine(t, e.base, target, "blocked_reason: waiting\n")
	insertTaskFMLine(t, e.base, target, "depends_on: ["+dep+"]\n")
	r, err := Readiness(e.st, target)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"assigned", "external", "dependency_open", "open_children", "open_children"}
	if r.Ready || len(r.Blockers) != len(want) {
		t.Fatalf("blockers: %+v", r)
	}
	for i, code := range want {
		if r.Blockers[i].Code != code {
			t.Fatalf("blocker %d=%s want %s (child=%s)", i, r.Blockers[i].Code, code, child)
		}
	}
	childIDs := []string{child, child2}
	if childIDs[1] < childIDs[0] {
		childIDs[0], childIDs[1] = childIDs[1], childIDs[0]
	}
	if r.Blockers[len(r.Blockers)-2].ID != childIDs[0] || r.Blockers[len(r.Blockers)-1].ID != childIDs[1] {
		t.Fatalf("open child blocker ID order: %+v", r.Blockers)
	}
	implicit := e.create(t, "implicit objective", CreateOptions{Sections: map[string]string{"objective": "The objective is enough."}})
	if r, err := Readiness(e.st, implicit); err != nil || !r.Ready {
		t.Fatalf("missing optional acceptance blocked readiness: %+v (%v)", r, err)
	}

	unchecked := e.create(t, "unchecked", CreateOptions{Sections: map[string]string{"objective": "x", "acceptance": "- [ ] later"}})
	if r, err := Readiness(e.st, unchecked); err != nil || !r.Ready {
		t.Fatalf("unchecked acceptance: %+v (%v)", r, err)
	}
	depBefore, err := os.ReadFile(filepath.Join(e.base, "tickets", dep, "TASK.md"))
	if err != nil {
		t.Fatal(err)
	}
	contextual := e.create(t, "contextual", CreateOptions{DependsOn: []string{dep}, Sections: map[string]string{"objective": "x", "acceptance": "x"}})
	if r, err := Readiness(e.st, contextual); err != nil || r.Ready || !hasBlocker(r, "dependency_open", dep) {
		t.Fatalf("contextual links satisfied dependency: %+v (%v)", r, err)
	}
	depAfter, err := os.ReadFile(filepath.Join(e.base, "tickets", dep, "TASK.md"))
	if err != nil || !bytes.Equal(depBefore, depAfter) {
		t.Fatal("contextual links mutated prerequisite")
	}
	external := e.create(t, "external", CreateOptions{Sections: map[string]string{"objective": "x", "acceptance": "x"}})
	insertTaskFMLine(t, e.base, external, "blocked_reason: outside\n")
	if r, err := Readiness(e.st, external); err != nil || r.Ready || !hasBlocker(r, "external", "") {
		t.Fatalf("external blocker: %+v (%v)", r, err)
	}

	terminalPrereq := e.create(t, "terminal", CreateOptions{DependsOn: []string{dep}, Sections: map[string]string{"objective": "terminal"}})
	if err := setStateLine(t, e.base, terminalPrereq, "- State: open\n", "- State: completed\n"); err != nil {
		t.Fatal(err)
	}
	historic := e.create(t, "historic", CreateOptions{DependsOn: []string{terminalPrereq}, Sections: map[string]string{"objective": "x", "acceptance": "x"}})
	if r, err := Readiness(e.st, historic); err != nil || !r.Ready {
		t.Fatalf("completed direct prerequisite with open history: %+v (%v)", r, err)
	}

	e3 := newEnv(t, 9416)
	expected := e3.create(t, "expected", CreateOptions{Sections: map[string]string{"objective": "x", "acceptance": "x"}})
	// Replace the objective with whitespace directly to exercise readiness's
	// required-content trim rule.
	path := filepath.Join(e3.base, "tickets", expected, "TASK.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content = bytes.Replace(content, []byte("## Objective\n\nx\n"), []byte("## Objective\n\n \n"), 1)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if r, err := Readiness(e3.st, expected); err != nil || !hasBlocker(r, "missing_objective", "") {
		t.Fatalf("whitespace objective: %+v (%v)", r, err)
	}
	positive := e3.create(t, "positive", CreateOptions{Sections: map[string]string{"objective": "x", "acceptance": "x"}})
	_ = e3.create(t, "draft", CreateOptions{})
	ready, err := Ready(e3.st, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ready.Items) != 1 || ready.Items[0].ID != positive || ready.More {
		t.Fatalf("exact ready result: %+v", ready)
	}
}

func TestNextUsesReadyOrderingAndClaim(t *testing.T) {
	e := newEnv(t, 9420)
	blockedDependency := e.create(t, "blocked dependency", CreateOptions{Priority: 0, Sections: map[string]string{"objective": "dependency"}})
	insertTaskFMLine(t, e.base, blockedDependency, "assignee: worker\n")
	blocked := e.create(t, "blocked", CreateOptions{Priority: 0, DependsOn: []string{blockedDependency}, Sections: map[string]string{"objective": "blocked"}})
	assigned := e.create(t, "assigned", CreateOptions{Priority: 0, Sections: map[string]string{"objective": "assigned"}})
	insertTaskFMLine(t, e.base, assigned, "assignee: worker\n")
	parent := e.create(t, "parent", CreateOptions{Priority: 0, Sections: map[string]string{"objective": "parent"}})
	child := e.create(t, "child", CreateOptions{Parent: parent, Priority: 0, Sections: map[string]string{"objective": "child"}})
	insertTaskFMLine(t, e.base, child, "assignee: worker\n")
	hold := e.create(t, "hold", CreateOptions{Priority: 0, Sections: map[string]string{"objective": "hold"}})
	if _, err := Hold(e.st, hold, HoldOptions{}); err != nil {
		t.Fatal(err)
	}
	review := e.create(t, "review", CreateOptions{Priority: 0, Sections: map[string]string{"objective": "review"}})
	if _, err := Submit(e.st, review, SubmitOptions{}); err != nil {
		t.Fatal(err)
	}
	completed := e.create(t, "completed", CreateOptions{Priority: 0, Sections: map[string]string{"objective": "completed"}})
	if _, err := Close(e.st, completed, CloseOptions{}); err != nil {
		t.Fatal(err)
	}
	rejected := e.create(t, "rejected", CreateOptions{Priority: 0, Sections: map[string]string{"objective": "rejected"}})
	if _, err := Reject(e.st, rejected, RejectOptions{Outcome: "declined"}); err != nil {
		t.Fatal(err)
	}
	selected := e.create(t, "selected", CreateOptions{Priority: 1, Sections: map[string]string{"objective": "selected"}})
	before, err := os.ReadFile(filepath.Join(e.base, "tickets", selected, "TASK.md"))
	if err != nil {
		t.Fatal(err)
	}
	next, err := Next(e.st, false, "")
	if err != nil || next.Item == nil || next.Item.ID != selected {
		t.Fatalf("next selection: %+v (%v)", next, err)
	}
	after, err := os.ReadFile(filepath.Join(e.base, "tickets", selected, "TASK.md"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("read-only next changed the selected ticket")
	}
	claimed, err := Next(e.st, true, "agent")
	if err != nil || claimed.Item == nil || claimed.Item.ID != selected || claimed.Item.Assignee == nil || *claimed.Item.Assignee != "agent" {
		t.Fatalf("next claim: %+v (%v)", claimed, err)
	}
	ticket, err := ReadTicket(e.st, selected)
	if err != nil || ticket.Assignee != "agent" || ticket.State != "open" {
		t.Fatalf("claimed selected ticket: %+v (%v)", ticket, err)
	}
	empty, err := Next(e.st, false, "")
	if err != nil || empty.Item != nil {
		t.Fatalf("empty frontier: %+v (%v)", empty, err)
	}
	_ = blocked
}

func TestNextWithoutTagFiltersSelectionAndClaim(t *testing.T) {
	e := newEnv(t, 9421)
	excluded := e.create(t, "excluded", CreateOptions{Priority: 0, Tags: []string{"skip"}, Sections: map[string]string{"objective": "excluded"}})
	allowed := e.create(t, "allowed", CreateOptions{Priority: 1, Tags: []string{"keep"}, Sections: map[string]string{"objective": "allowed"}})

	selected, err := NextWithOptions(e.st, NextOptions{Queue: "open", WithoutTags: []string{"skip", "skip"}})
	if err != nil || selected.Item == nil || selected.Item.ID != allowed {
		t.Fatalf("filtered open selection: %+v (%v)", selected, err)
	}
	claimed, err := NextWithOptions(e.st, NextOptions{Queue: "open", WithoutTags: []string{"skip"}, Claim: true, Actor: "agent"})
	if err != nil || claimed.Item == nil || claimed.Item.ID != allowed || claimed.Item.Assignee == nil || *claimed.Item.Assignee != "agent" {
		t.Fatalf("filtered open claim: %+v (%v)", claimed, err)
	}
	empty, err := NextWithOptions(e.st, NextOptions{Queue: "open", WithoutTags: []string{"skip"}})
	if err != nil || empty.Item != nil {
		t.Fatalf("excluded-only open queue was not empty: %+v (%v)", empty, err)
	}
	_ = excluded

	reviewExcluded := e.create(t, "review excluded", CreateOptions{Tags: []string{"skip"}, Sections: map[string]string{"objective": "review excluded"}})
	reviewAllowed := e.create(t, "review allowed", CreateOptions{Tags: []string{"keep"}, Sections: map[string]string{"objective": "review allowed"}})
	if _, err := Submit(e.st, reviewExcluded, SubmitOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Submit(e.st, reviewAllowed, SubmitOptions{}); err != nil {
		t.Fatal(err)
	}
	review, err := NextWithOptions(e.st, NextOptions{Queue: "review", WithoutTags: []string{"skip", "skip"}})
	if err != nil || review.Item == nil || review.Item.ID != reviewAllowed {
		t.Fatalf("filtered review selection: %+v (%v)", review, err)
	}
}

func insertTaskFMLine(t *testing.T, base, id, line string) {
	t.Helper()
	p := filepath.Join(base, "tickets", id, "TASK.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if idx := bytes.Index(b, []byte("---\n")); idx >= 0 {
		idx += len("---\n")
		b = append(b[:idx], append([]byte(line), b[idx:]...)...)
	} else {
		idx := bytes.IndexByte(b, '\n')
		if idx < 0 {
			t.Fatalf("%s: missing title line", id)
		}
		visible := strings.TrimSuffix(line, "\n")
		switch {
		case strings.HasPrefix(visible, "state: "):
			visible = "- State: " + strings.TrimPrefix(visible, "state: ")
		case strings.HasPrefix(visible, "priority: "):
			visible = "- Priority: P" + strings.TrimPrefix(visible, "priority: ")
		case strings.HasPrefix(visible, "parent: "):
			visible = "- Parent: " + strings.TrimPrefix(visible, "parent: ")
		case strings.HasPrefix(visible, "blocked_reason: "):
			visible = "- Blocked reason: " + strings.TrimPrefix(visible, "blocked_reason: ")
		case strings.HasPrefix(visible, "assignee: "):
			visible = "- Assignee: " + strings.TrimPrefix(visible, "assignee: ")
		case strings.HasPrefix(visible, "depends_on: ["):
			visible = "- Depends on: " + strings.TrimSuffix(strings.TrimPrefix(visible, "depends_on: ["), "]")
		}
		insert := []byte(visible + "\n")
		at := idx + 1
		b = append(b[:at], append(insert, b[at:]...)...)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasBlocker(r *ReadinessView, code, id string) bool {
	for _, b := range r.Blockers {
		if b.Code == code && b.ID == id {
			return true
		}
	}
	return false
}
