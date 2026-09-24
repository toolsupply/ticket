package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendObjectiveFromFileAndStdin(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Append target", "Initial objective."))["id"].(string)
	source := filepath.Join(dir, "objective.md")
	if err := os.WriteFile(source, []byte("## Objective\n\nFile paragraph.\n### Detail\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	appended := exactlyOneJSONObject(t, mustCLI(t, "append", id, source))
	if appended["changed"] != true {
		t.Fatalf("append did not report change: %v", appended)
	}
	changedFields := appended["changed_fields"].([]any)
	if len(changedFields) != 1 || changedFields[0] != "sections.objective" {
		t.Fatalf("append changed fields: %v", appended)
	}
	secondSource := filepath.Join(dir, "second-objective.md")
	if err := os.WriteFile(secondSource, []byte("Second file paragraph.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := runCLI(t, "append", id, "-o", secondSource); code != 0 {
		t.Fatalf("append -o file: exit=%d out=%q", code, out)
	}
	if out, code := runCLIStdin(t, "Trailing dash paragraph.\n", "append", id, "-"); code != 0 {
		t.Fatalf("append trailing stdin: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "claim", id, "--actor", "alice"); code != 0 {
		t.Fatalf("claim: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "append", id, source, "--actor", "bob"); code == 0 || !strings.Contains(out, "already_claimed") {
		t.Fatalf("foreign append accepted: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "append", id, source, "--actor", "alice"); code != 0 {
		t.Fatalf("append file: exit=%d out=%q", code, out)
	}
	if out, code := runCLIStdin(t, "# ignored title\n\nStdin paragraph.\n", "append", id, "--objective", "-", "--actor", "alice"); code != 0 {
		t.Fatalf("append stdin: exit=%d out=%q", code, out)
	}
	if out, code := runCLIStdin(t, "Implicit stdin paragraph.\n", "append", id, "--actor", "alice"); code != 0 {
		t.Fatalf("append implicit stdin: exit=%d out=%q", code, out)
	}
	if out, code := runCLIStdin(t, `{"append_sections":{"objective":"Structured paragraph."}}`, "update", id, "--input", "-", "--actor", "alice"); code != 0 {
		t.Fatalf("structured append: exit=%d out=%q", code, out)
	}
	body := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))["body"].(string)
	for _, want := range []string{"Initial objective.", "File paragraph.", "### Detail", "Stdin paragraph.", "Implicit stdin paragraph.", "Structured paragraph."} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "## Objective\n\n## Objective") {
		t.Fatalf("objective wrapper duplicated:\n%s", body)
	}
}

func TestCreateObjectiveFileAndImport(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	objective := filepath.Join(dir, "objective.md")
	if err := os.WriteFile(objective, []byte("# File title\n\nHuman objective.\n## Detail\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "--objective", objective))
	id := created["id"].(string)
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	sections := view["sections"].(map[string]any)
	objectiveView := sections["objective"].(map[string]any)
	if view["title"] != "File title" || !strings.Contains(objectiveView["text"].(string), "Human objective.") {
		t.Fatalf("objective source was not normalized: %v", view)
	}
	positional := exactlyOneJSONObject(t, mustCLI(t, "create", "Inline title", "Inline Objective text."))
	positionalView := exactlyOneJSONObject(t, mustCLI(t, "show", positional["id"].(string), "--full"))
	if !strings.Contains(positionalView["body"].(string), "Inline Objective text.") {
		t.Fatalf("positional Objective text was not preserved: %v", positionalView)
	}
	stdinOut, stdinCode := runCLIStdin(t, "# Stdin title\n\nStdin objective.\n", "create", "--objective", "-")
	if stdinCode != 0 {
		t.Fatalf("stdin objective: exit=%d out=%q", stdinCode, stdinOut)
	}
	stdinCreated := exactlyOneJSONObject(t, stdinOut)
	stdinView := exactlyOneJSONObject(t, mustCLI(t, "show", stdinCreated["id"].(string)))
	if stdinView["title"] != "Stdin title" {
		t.Fatalf("stdin objective title was not extracted: %v", stdinView)
	}
	trailingOut, trailingCode := runCLIStdin(t, "# Trailing source title\n\nTrailing objective.\n", "new", "-")
	if trailingCode != 0 {
		t.Fatalf("trailing source without title: exit=%d out=%q", trailingCode, trailingOut)
	}
	trailingID := exactlyOneJSONObject(t, trailingOut)["id"].(string)
	trailingView := exactlyOneJSONObject(t, mustCLI(t, "show", trailingID))
	if trailingView["title"] != "Trailing source title" {
		t.Fatalf("trailing source title was not extracted: %v", trailingView)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		title string
	}{
		{name: "new positional title", args: []string{"new", "Explicit new", "-"}, title: "Explicit new"},
		{name: "create flag title", args: []string{"create", "-t", "Explicit create", "-"}, title: "Explicit create"},
		{name: "create long flag title", args: []string{"create", "--title", "Explicit long create", "-"}, title: "Explicit long create"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runCLIStdin(t, "Explicit stdin objective.\n", tc.args...)
			if code != 0 {
				t.Fatalf("explicit trailing stdin: exit=%d out=%q", code, out)
			}
			created := exactlyOneJSONObject(t, out)
			view := exactlyOneJSONObject(t, mustCLI(t, "show", created["id"].(string), "--full"))
			if view["title"] != tc.title {
				t.Fatalf("explicit title was not preserved: %v", view)
			}
			body := view["body"].(string)
			if !strings.Contains(body, "Explicit stdin objective.") {
				t.Fatalf("explicit stdin objective was not consumed: %s", body)
			}
		})
	}
	data := "# Imported\n\n- State: open\n- Priority: P1\n\n## Objective\n\nImported objective.\n"
	importPath := filepath.Join(dir, "ticket.md")
	if err := os.WriteFile(importPath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	imported := exactlyOneJSONObject(t, mustCLI(t, "create", "--import", importPath))
	importedView := exactlyOneJSONObject(t, mustCLI(t, "show", imported["id"].(string)))
	if importedView["title"] != "Imported" || importedView["priority"] != float64(1) {
		t.Fatalf("import metadata was not preserved: %v", importedView)
	}
	stdinImport, stdinImportCode := runCLIStdin(t, data, "create", "--import", "-")
	if stdinImportCode != 0 {
		t.Fatalf("stdin import: exit=%d out=%q", stdinImportCode, stdinImport)
	}
	for _, args := range [][]string{
		{"create", "--import", importPath, "Extra title"},
		{"create", "--import", importPath, "--title", "Extra title"},
		{"create", "--import", importPath, "--objective", objective},
		{"create", "--import", importPath, "--input", "-"},
		{"create", "--import", importPath, "--priority", "1"},
	} {
		if out, code := runCLI(t, args...); code == 0 || errCode(t, out) != "invalid_argument" {
			t.Fatalf("ambiguous import accepted: args=%v exit=%d out=%q", args, code, out)
		}
	}
	malformed := filepath.Join(dir, "malformed.md")
	if err := os.WriteFile(malformed, []byte("# One\n\n# Two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := runCLI(t, "create", "--import", malformed); code == 0 || errCode(t, out) != "invalid_argument" || !strings.Contains(out, "Invalid Ticket Markdown import") {
		t.Fatalf("malformed import accepted or misreported: exit=%d out=%q", code, out)
	}
	structured := filepath.Join(dir, "create.json")
	if err := os.WriteFile(structured, []byte(`{"title":"Structured file","sections":{"objective":"Machine objective."}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	structuredCreated := exactlyOneJSONObject(t, mustCLI(t, "create", "--input", structured))
	if structuredCreated["id"] == nil {
		t.Fatalf("structured file create: %v", structuredCreated)
	}
	if out, code := runCLIStdin(t, `{"title":"Strict heading","sections":{"objective":"# Must remain strict"}}`, "create", "--input", "-"); code == 0 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("structured heading accepted or misreported: exit=%d out=%q", code, out)
	}
	duplicateImport := filepath.Join(dir, "duplicate.md")
	duplicateData := "# Duplicate dependency\n\n- State: open\n- Priority: P2\n- Depends on: [" + id + ", " + id + "]\n\n## Objective\n\nImported objective.\n"
	if err := os.WriteFile(duplicateImport, []byte(duplicateData), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := runCLI(t, "create", "--import", duplicateImport); code == 0 || errCode(t, out) != "invalid_argument" || !strings.Contains(out, "Invalid Ticket Markdown import") {
		t.Fatalf("duplicate dependency import accepted or misreported: exit=%d out=%q", code, out)
	}
}

func TestCreateHelpShortCircuitsImportSource(t *testing.T) {
	t.Chdir(t.TempDir())
	missing := filepath.Join(t.TempDir(), "missing-ticket.md")
	out, code := runCLIHuman(t, "create", "--help", "--import", missing)
	if code != 0 || !strings.Contains(out, "create - Create a new ticket") {
		t.Fatalf("create help with missing import: exit=%d out=%q", code, out)
	}
}

func TestCreateRejectsMultipleObjectiveSources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	objective := filepath.Join(dir, "objective.md")
	if err := os.WriteFile(objective, []byte("File objective."), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		input string
		args  []string
	}{
		{name: "create file", args: []string{"create", "Conflict", "Inline", "--objective", objective}},
		{name: "new file", args: []string{"new", "Conflict", "Inline", "--objective", objective}},
		{name: "create stdin option", input: "must not be consumed", args: []string{"create", "Conflict", "Inline", "--objective", "-"}},
		{name: "new trailing stdin", input: "must not be consumed", args: []string{"new", "Conflict", "Inline", "-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out string
			var code int
			if tc.input != "" {
				out, code = runCLIStdin(t, tc.input, tc.args...)
			} else {
				out, code = runCLI(t, tc.args...)
			}
			if code == 0 || errCode(t, out) != "invalid_argument" || !strings.Contains(out, "Objective source may be supplied only once") {
				t.Fatalf("ambiguous Objective source accepted or misreported: exit=%d out=%q", code, out)
			}
		})
	}
}

func TestHumanInputHelpAndModeErrors(t *testing.T) {
	createHelp, createCode := runCLIHuman(t, "help", "create")
	if createCode != 0 || !strings.Contains(createHelp, "TITLE [OBJECTIVE|-") ||
		!strings.Contains(createHelp, "--objective") || !strings.Contains(createHelp, "FILE|-") || !strings.Contains(createHelp, "--import") || !strings.Contains(createHelp, "--input") || !strings.Contains(createHelp, "#tag") {
		t.Fatalf("create help omits human/machine source contracts: exit=%d help=%q", createCode, createHelp)
	}
	appendHelp, appendCode := runCLIHuman(t, "help", "append")
	if appendCode != 0 || !strings.Contains(appendHelp, "ID [OBJECTIVE_FILE|-") || !strings.Contains(appendHelp, "--objective") {
		t.Fatalf("append help omits FILE|- contract: exit=%d help=%q", appendCode, appendHelp)
	}
	if out, code := runCLI(t, "create"); code == 0 || !strings.Contains(out, "A title is required") {
		t.Fatalf("missing title error was unclear: exit=%d out=%q", code, out)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Append errors"))["id"].(string)
	if out, code := runCLIStdin(t, "", "append", id); code == 0 || !strings.Contains(out, "Objective content must not be empty") {
		t.Fatalf("empty append error was unclear: exit=%d out=%q", code, out)
	}
}
