package cli

import (
	"strings"
	"testing"
)

func TestTransitionResultsIncludeObservedStates(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Transition result", "Report both states."))["id"].(string)

	mustCLI(t, "open", id)
	hold := exactlyOneJSONObject(t, mustCLI(t, "hold", id))
	if hold["from_state"] != "open" || hold["state"] != "hold" {
		t.Fatalf("hold transition: %v", hold)
	}
	opened := exactlyOneJSONObject(t, mustCLI(t, "open", id, "--claim", "--actor", "agent-00"))
	if opened["from_state"] != "hold" || opened["state"] != "open" || opened["assignee"] != "agent-00" {
		t.Fatalf("open and claim transition: %v", opened)
	}

	human, code := runCLIHuman(t, "submit", id, "--actor", "agent-00")
	if code != 0 || !strings.Contains(human, id+": open -> review") {
		t.Fatalf("human transition: exit=%d output=%q", code, human)
	}
}

func TestBatchTransitionHumanOutputIdentifiesEachTicket(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	first := exactlyOneJSONObject(t, mustCLI(t, "create", "First transition", "Complete first."))["id"].(string)
	second := exactlyOneJSONObject(t, mustCLI(t, "create", "Second transition", "Complete second."))["id"].(string)

	out, code := runCLIHuman(t, "close", "all")
	if code != 0 || !strings.Contains(out, first+": open -> closed") || !strings.Contains(out, second+": open -> closed") {
		t.Fatalf("batch transition output: exit=%d output=%q", code, out)
	}
}
