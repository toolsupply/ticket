package cli

import (
	"strings"
	"testing"
)

func TestCreateShortTitleAndPriorityAliases(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "-t", "Short aliases", "-p", "1"))
	if created["id"] == nil {
		t.Fatalf("create result: %v", created)
	}
	id := created["id"].(string)
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if view["title"] != "Short aliases" || view["priority"] != float64(1) {
		t.Fatalf("alias values were not applied: %v", view)
	}
	human, _, code := runCLIHumanStdin(t, "## Objective\n\nFirst objective line\nA second line.\n", "create", "-t", "Human status")
	if code != 0 || !strings.Contains(human, "state:      open\npriority:   P2\nobjective:  First objective line\n") {
		t.Fatalf("human create status: exit=%d out=%q", code, human)
	}
	humanStatus, code := runCLIHuman(t, "status", strings.Fields(human)[1])
	if code != 0 || !strings.Contains(humanStatus, "objective:  First objective line\n") {
		t.Fatalf("human status objective: exit=%d out=%q", code, humanStatus)
	}
}
