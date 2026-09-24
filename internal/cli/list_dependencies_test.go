package cli

import (
	"regexp"
	"strings"
	"testing"
)

func TestListDependencyViewsAndIDsOnlyMode(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	dependency := exactlyOneJSONObject(t, mustCLI(t, "create", "Dependency", "Complete the prerequisite."))["id"].(string)
	root := exactlyOneJSONObject(t, mustCLI(t, "create", "Blocked root", "Complete the root.", "--depends-on", dependency, "--tag", "cleanup"))["id"].(string)
	if _, code := runCLI(t, "close", dependency); code != 0 {
		t.Fatalf("close dependency: exit=%d", code)
	}
	if _, code := runCLI(t, "archive", dependency); code != 0 {
		t.Fatalf("archive dependency: exit=%d", code)
	}

	human, code := runCLIHuman(t, "list", "--state", "open")
	if code != 0 || !strings.Contains(human, "BLOCKED BY") || !strings.Contains(human, root) || strings.Contains(human, dependency) {
		t.Fatalf("blocking human list: exit=%d output=%q", code, human)
	}

	expanded, code := runCLIHuman(t, "list", "--state", "open", "--deps")
	if code != 0 || !strings.Contains(expanded, dependency[len(dependency)-5:]+"✓(arch)") {
		t.Fatalf("expanded human list: exit=%d output=%q", code, expanded)
	}

	defaultJSON := exactlyOneJSONObject(t, mustCLI(t, "list", "--state", "open"))
	defaultItem := defaultJSON["items"].([]any)[0].(map[string]any)
	if _, ok := defaultItem["dependencies"]; ok {
		t.Fatalf("default JSON gained dependency fields: %v", defaultItem)
	}
	expandedJSON := exactlyOneJSONObject(t, mustCLI(t, "list", "--state", "open", "--deps"))
	dependencyItems := expandedJSON["items"].([]any)[0].(map[string]any)["dependencies"].([]any)
	if len(dependencyItems) != 1 {
		t.Fatalf("expanded JSON dependencies: %v", expandedJSON)
	}
	dependencyItem := dependencyItems[0].(map[string]any)
	if dependencyItem["id"] != dependency || dependencyItem["satisfied"] != true || dependencyItem["archived"] != true {
		t.Fatalf("expanded dependency metadata: %v", dependencyItem)
	}
	activeAll := exactlyOneJSONObject(t, mustCLI(t, "list", "--state", "all", "--deps"))
	if items := activeAll["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != root {
		t.Fatalf("archive dependency leaked into active list: %v", activeAll)
	}
	activeCompleted := exactlyOneJSONObject(t, mustCLI(t, "list", "--state", "completed", "--deps"))
	if items := activeCompleted["items"].([]any); len(items) != 0 {
		t.Fatalf("archived dependency leaked into active completed list: %v", activeCompleted)
	}

	ids, code := runCLIHuman(t, "list", "-1", "--tag", "cleanup")
	if code != 0 || ids != root+"\n" || !regexp.MustCompile(`^20[0-9]{6}-[0-9]{5}\n$`).MatchString(ids) {
		t.Fatalf("IDs-only output: exit=%d output=%q", code, ids)
	}
	idsAlias, code := runCLIHuman(t, "list", "--ids", "--tag", "cleanup")
	if code != 0 || idsAlias != ids {
		t.Fatalf("IDs alias output: exit=%d output=%q want=%q", code, idsAlias, ids)
	}
	for _, args := range [][]string{{"list", "-j", "-1"}, {"list", "--json", "--ids"}, {"list", "-1", "--deps"}} {
		if out, code := runCLI(t, args...); code == 0 || errCode(t, out) != "invalid_argument" {
			t.Fatalf("conflicting list modes %v: exit=%d output=%q", args, code, out)
		}
	}
}
