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
	if code != 0 || !strings.Contains(human, "\n\ntitle:      Human status\nstate:      open\npriority:   P2\n\nobjective:  ### Objective\n") {
		t.Fatalf("human create status: exit=%d out=%q", code, human)
	}
	if strings.Contains(human, "/TASK.md") {
		t.Fatalf("human create output exposed the task filename: %q", human)
	}
	humanStatus, code := runCLIHuman(t, "status", strings.Fields(human)[1])
	if code != 0 || !strings.Contains(humanStatus, "objective:  ### Objective\n") {
		t.Fatalf("human status objective: exit=%d out=%q", code, humanStatus)
	}
}

func TestHumanCreateAndNewConfirmationTruncatesText(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	title := strings.Repeat("T", 50)
	objective := strings.Repeat("O", 100)
	for _, command := range []string{"create", "new"} {
		t.Run(command, func(t *testing.T) {
			human, code := runCLIHuman(t, command, title, objective)
			if code != 0 {
				t.Fatalf("%s: exit=%d out=%q", command, code, human)
			}
			if !strings.Contains(human, "\n\ntitle:      "+strings.Repeat("T", 39)+"…\nstate:      open\npriority:   P2\n\nobjective:  "+strings.Repeat("O", 39)+"…\n") {
				t.Fatalf("%s confirmation missing truncated title/objective or spacing: %q", command, human)
			}
			if strings.Contains(human, "/TASK.md") {
				t.Fatalf("%s confirmation exposed the task filename: %q", command, human)
			}
		})
	}
	jsonOut := mustCLI(t, "create", title, objective)
	jsonResult := exactlyOneJSONObject(t, jsonOut)
	if _, ok := jsonResult["title"]; ok || jsonResult["path"] != jsonResult["id"].(string)+"/TASK.md" {
		t.Fatalf("JSON create result changed: %v", jsonResult)
	}
}
