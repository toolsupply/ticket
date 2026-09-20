package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAndRejectUseCurrentTicketWithPositionalText(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Current transition", "Exercise current-ticket transitions."))
	id := created["id"].(string)
	if _, code := runCLIHuman(t, "show", id); code != 0 {
		t.Fatalf("select current ticket: exit=%d", code)
	}
	if _, code := runCLI(t, "hold", id); code != 0 {
		t.Fatalf("hold: exit=%d", code)
	}
	if out, code := runCLIHuman(t, "open", "Continue from the current ticket"); code != 0 || !strings.Contains(out, id+": hold -> open") {
		t.Fatalf("open with current ticket and handoff: exit=%d output=%q", code, out)
	}
	opened := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	if opened["state"] != "open" || !strings.Contains(opened["body"].(string), "Continue from the current ticket") {
		t.Fatalf("opened ticket: %v", opened)
	}

	second := exactlyOneJSONObject(t, mustCLI(t, "create", "Current rejection", "Exercise current-ticket rejection."))["id"].(string)
	if _, code := runCLIHuman(t, "show", second); code != 0 {
		t.Fatalf("select rejection ticket: exit=%d", code)
	}
	if out, code := runCLIHuman(t, "reject", "Duplicate work"); code != 0 || !strings.Contains(out, second+": open -> rejected") {
		t.Fatalf("reject with current ticket and outcome: exit=%d output=%q", code, out)
	}
	rejected := exactlyOneJSONObject(t, mustCLI(t, "show", second))
	if rejected["state"] != "rejected" || rejected["sections"].(map[string]any)["outcome"].(map[string]any)["text"] != "Duplicate work" {
		t.Fatalf("rejected ticket: %v", rejected)
	}
}

func TestReviewUsesDirectStateTransitionAndCurrentTicket(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Direct review", "Send this ticket to review."))["id"].(string)
	if out, code := runCLIHuman(t, "review", id); code != 0 || out != id+": open -> review\n" {
		t.Fatalf("explicit review: exit=%d out=%q", code, out)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if view["state"] != "review" || view["assignee"] != nil {
		t.Fatalf("direct review state: %v", view)
	}
	second := exactlyOneJSONObject(t, mustCLI(t, "create", "Current review", "Use the current ticket."))["id"].(string)
	if _, code := runCLIHuman(t, "show", second); code != 0 {
		t.Fatalf("select review ticket: exit=%d", code)
	}
	if out, code := runCLIHuman(t, "review", "-m", "Sent for review"); code != 0 || out != second+": open -> review\n" {
		t.Fatalf("current review: exit=%d out=%q", code, out)
	}
	if view := exactlyOneJSONObject(t, mustCLI(t, "show", second)); view["state"] != "review" {
		t.Fatalf("current review state: %v", view)
	}
}

func TestReviewEnforcesExistingOwnershipWithoutChangingRejectedTickets(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("TICKET_ACTOR", "")
	mustCLI(t, "init")

	unassigned := exactlyOneJSONObject(t, mustCLI(t, "create", "Unassigned review", "Allow actorless review."))["id"].(string)
	if out, code := runCLI(t, "review", unassigned); code != 0 || exactlyOneJSONObject(t, out)["state"] != "review" {
		t.Fatalf("actorless unassigned review: exit=%d out=%q", code, out)
	}

	sameActor := exactlyOneJSONObject(t, mustCLI(t, "create", "Owned review", "Allow same-owner review."))["id"].(string)
	mustCLI(t, "claim", sameActor, "--actor", "alice")
	if out, code := runCLI(t, "review", sameActor, "--actor", "alice"); code != 0 || exactlyOneJSONObject(t, out)["state"] != "review" {
		t.Fatalf("same-owner review: exit=%d out=%q", code, out)
	}
	if view := exactlyOneJSONObject(t, mustCLI(t, "show", sameActor)); view["assignee"] != nil {
		t.Fatalf("same-owner review retained assignment: %v", view)
	}

	otherActor := exactlyOneJSONObject(t, mustCLI(t, "create", "Other owner", "Reject another owner."))["id"].(string)
	mustCLI(t, "claim", otherActor, "--actor", "alice")
	path := filepath.Join(dir, "tickets", otherActor, "TASK.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if out, code := runCLI(t, "review", otherActor, "--actor", "bob"); code == 0 || errCode(t, out) != "already_claimed" {
		t.Fatalf("other-owner review: exit=%d out=%q", code, out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected other-owner review changed TASK.md")
	}
	if out, code := runCLI(t, "review", otherActor); code == 0 || errCode(t, out) != "missing_actor" {
		t.Fatalf("missing-actor review: exit=%d out=%q", code, out)
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(before, after) {
		t.Fatalf("missing-actor review changed TASK.md: %v", err)
	}
}

func TestOpenCanClaimTicket(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Open and claim", "Open and claim the ticket."))["id"].(string)
	mustCLI(t, "hold", id)

	result := exactlyOneJSONObject(t, mustCLI(t, "open", id, "--claim", "--actor", "worker"))
	if result["id"] != id || result["changed"] != true || result["assignee"] != "worker" {
		t.Fatalf("open --claim result: %v", result)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if view["state"] != "open" || view["assignee"] != "worker" {
		t.Fatalf("open --claim state: %v", view)
	}

	if out, code := runCLI(t, "help", "open"); code != 0 || !strings.Contains(out, "--claim") {
		t.Fatalf("open help: exit=%d out=%q", code, out)
	}
}

func TestOpenClaimFailurePreservesTicket(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Blocked open and claim", "The objective remains unchanged on failure."))["id"].(string)
	mustCLI(t, "hold", id)
	runCLIStdinMust(t, `{"set":{"blocked_reason":"Waiting for an external dependency."}}`, "update", id, "--input", "-")

	path := filepath.Join("tickets", id, "TASK.md")
	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	if out, code := runCLI(t, "open", id, "--claim", "--actor", "worker"); code == 0 || errCode(t, out) != "dependency_unresolved" {
		t.Fatalf("blocked open --claim: exit=%d out=%q", code, out)
	}
	afterBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatalf("failed open --claim changed TASK.md")
	}
	after := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	if after["state"] != before["state"] || after["assignee"] != before["assignee"] || after["body"] != before["body"] {
		t.Fatalf("failed open --claim changed ticket: before=%v after=%v", before, after)
	}
}

func TestOpenProtectsAnotherActorsClaim(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("TICKET_ACTOR", "")
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Owned open", "Keep the ownership boundary."))["id"].(string)
	mustCLI(t, "claim", id, "--actor", "alice")
	path := filepath.Join("tickets", id, "TASK.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		args []string
		code string
	}{
		{[]string{"open", id, "--actor", "bob"}, "already_claimed"},
		{[]string{"open", id, "--claim", "--actor", "bob"}, "already_claimed"},
		{[]string{"open", id}, "missing_actor"},
	}
	for _, check := range checks {
		args := check.args
		out, code := runCLI(t, args...)
		if code == 0 || errCode(t, out) != check.code {
			t.Fatalf("open ownership check: args=%v exit=%d out=%q", args, code, out)
		}
		after, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(before, after) {
			t.Fatalf("failed open changed TASK.md: args=%v", args)
		}
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if view["state"] != "open" || view["assignee"] != "alice" {
		t.Fatalf("ownership after rejected opens: %v", view)
	}
}

func TestSameActorCanOpenClaimedReviewTicket(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Review ownership", "Return the review ticket."))["id"].(string)
	mustCLI(t, "claim", id, "--actor", "worker")
	mustCLI(t, "submit", id, "--actor", "worker")
	mustCLI(t, "claim", id, "--actor", "reviewer")
	opened := exactlyOneJSONObject(t, mustCLI(t, "open", id, "--actor", "reviewer"))
	if opened["id"] != id || opened["state"] != "open" || opened["changed"] != true {
		t.Fatalf("same-actor review open: %v", opened)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if view["state"] != "open" || view["assignee"] != nil {
		t.Fatalf("same-actor review open state: %v", view)
	}
}
