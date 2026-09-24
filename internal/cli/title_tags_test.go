package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHumanTitleTrailingHashtagsBecomeTags(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "C# parser #go #urgent", "Document the parser.", "--tag", "release"))
	view := exactlyOneJSONObject(t, mustCLI(t, "show", created["id"].(string)))
	if view["title"] != "C# parser" {
		t.Fatalf("title shorthand changed interior hash text: %v", view)
	}
	tags := view["tags"].([]any)
	if len(tags) != 3 || tags[0] != "go" || tags[1] != "release" || tags[2] != "urgent" {
		t.Fatalf("title tags were not merged and normalized: %v", tags)
	}
	if out, code := runCLI(t, "create", "#onlytag"); code == 0 || !strings.Contains(out, "Title must contain text") {
		t.Fatalf("tag-only title was accepted or misreported: exit=%d out=%q", code, out)
	}
}

func TestTitleHashtagShorthandDoesNotApplyToStrictOrObjectiveSources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	jsonCreated := exactlyOneJSONObject(t, runCLIStdinMust(t, `{"title":"JSON #tag","sections":{"objective":"JSON objective."}}`, "create", "--input", "-"))
	jsonView := exactlyOneJSONObject(t, mustCLI(t, "show", jsonCreated["id"].(string)))
	if jsonView["title"] != "JSON #tag" || len(jsonView["tags"].([]any)) != 0 {
		t.Fatalf("strict JSON title shorthand applied: %v", jsonView)
	}

	objective := filepath.Join(dir, "objective.md")
	if err := os.WriteFile(objective, []byte("# Markdown #tag\n\nObjective text.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	markdownCreated := exactlyOneJSONObject(t, mustCLI(t, "create", "--objective", objective))
	markdownView := exactlyOneJSONObject(t, mustCLI(t, "show", markdownCreated["id"].(string)))
	if markdownView["title"] != "Markdown #tag" || len(markdownView["tags"].([]any)) != 0 {
		t.Fatalf("Objective Markdown title shorthand applied: %v", markdownView)
	}
}
