package cli

import (
	"strings"
	"testing"
)

func TestReviewHoldCanonicalAndObjectFirstCommands(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	canonical := exactlyOneJSONObject(t, mustCLI(t, "create", "Canonical review hold", "Pause this review."))["id"].(string)
	mustCLI(t, "submit", canonical)
	before := exactlyOneJSONObject(t, mustCLI(t, "ready", "review"))["items"].([]any)
	if len(before) != 1 || before[0].(map[string]any)["id"] != canonical {
		t.Fatalf("review ticket not initially selectable: %v", before)
	}
	held := exactlyOneJSONObject(t, mustCLI(t, "hold", canonical, "--actor", "operator", "--handoff", "Resume after follow-up.", "-m", "Paused review."))
	if held["from_state"] != "review" || held["state"] != "hold" {
		t.Fatalf("canonical review hold result=%v", held)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", canonical, "--full"))
	if view["state"] != "hold" || view["assignee"] != nil || !strings.Contains(view["body"].(string), "Resume after follow-up.") || !strings.Contains(view["body"].(string), "operator: Paused review.") {
		t.Fatalf("canonical held review=%v", view)
	}
	after := exactlyOneJSONObject(t, mustCLI(t, "ready", "review"))["items"].([]any)
	if len(after) != 0 {
		t.Fatalf("held ticket remained in review-ready selection: %v", after)
	}
	selection := exactlyOneJSONObject(t, mustCLI(t, "next", "review"))
	if selection["item"] != nil {
		t.Fatalf("held ticket remained in reviewer selection: %v", selection)
	}

	objectFirst := exactlyOneJSONObject(t, mustCLI(t, "create", "Object-first review hold", "Pause through object-first syntax."))["id"].(string)
	mustCLI(t, "submit", objectFirst)
	result := exactlyOneJSONObject(t, mustCLI(t, objectFirst, "hold"))
	if result["from_state"] != "review" || result["state"] != "hold" {
		t.Fatalf("object-first review hold result=%v", result)
	}
	help, code := runCLIHuman(t, "help", "hold")
	if code != 0 || !strings.Contains(help, "open or review") || !strings.Contains(help, "ownership") {
		t.Fatalf("hold help: exit=%d output=%q", code, help)
	}
}

func TestReviewHoldUsesPersistedOwnerAfterClaim(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Review hold claim race", "Do not overwrite a concurrent claim."))["id"].(string)
	mustCLI(t, "submit", id)
	observed := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if observed["state"] != "review" || observed["assignee"] != nil {
		t.Fatalf("initial ticket state=%v", observed)
	}
	claimed := exactlyOneJSONObject(t, mustCLI(t, "claim", id, "--actor", "alice"))
	if claimed["assignee"] != "alice" {
		t.Fatalf("claim result=%v", claimed)
	}
	if out, code := runCLI(t, "hold", id, "--actor", "bob"); code == 0 || errCode(t, out) != "already_claimed" {
		t.Fatalf("non-owner review hold: exit=%d output=%q", code, out)
	}
	current := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if current["state"] != "review" {
		t.Fatalf("failed hold changed the persisted claim: %v", current)
	}
	if out, code := runCLI(t, "claim", id, "--actor", "bob"); code == 0 || errCode(t, out) != "already_claimed" {
		t.Fatalf("failed hold did not preserve Alice's claim: exit=%d output=%q", code, out)
	}
	result := exactlyOneJSONObject(t, mustCLI(t, "hold", id, "--actor", "alice"))
	if result["from_state"] != "review" || result["state"] != "hold" {
		t.Fatalf("owner review hold: %v", result)
	}
}
