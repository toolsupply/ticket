package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitMarkdownDecoratorAndOutputPrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Decorator test", "Keep $(touch should-not-run) as data."))
	id := created["id"].(string)

	inputPath := filepath.Join(dir, "decorator-input")
	argsPath := filepath.Join(dir, "decorator-args")
	renderer := installTestHelper(t, filepath.Join(dir, "decorator-helper"), "decorator-record")
	t.Setenv("TICKET_DECORATOR", renderer+` -w 0 -s "pink theme"`)
	t.Setenv("DECORATOR_INPUT", inputPath)
	t.Setenv("DECORATOR_ARGS", argsPath)

	out, code := runCLIHuman(t, "show", id)
	if code != 0 || out != "decorated output\n" {
		t.Fatalf("decorated show: exit=%d out=%q", code, out)
	}
	input, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(input), "Keep $(touch should-not-run) as data.") {
		t.Fatalf("decorator did not receive Markdown data: %q", input)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(args) != "-w|0|-s|pink theme" {
		t.Fatalf("decorator arguments: %q", args)
	}

	if err := os.WriteFile(inputPath, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	markdown, code := runCLIHuman(t, "show", id, "--markdown")
	if code != 0 || !strings.Contains(markdown, "Keep $(touch should-not-run) as data.") {
		t.Fatalf("raw Markdown bypass: exit=%d out=%q", code, markdown)
	}
	if got, err := os.ReadFile(inputPath); err != nil || string(got) != "sentinel" {
		t.Fatalf("--markdown invoked decorator: err=%v input=%q", err, got)
	}

	jsonOutput, code := runCLI(t, "show", id)
	if code != 0 || !strings.Contains(jsonOutput, `"id":"`+id+`"`) {
		t.Fatalf("JSON precedence: exit=%d out=%q", code, jsonOutput)
	}
	if got, err := os.ReadFile(inputPath); err != nil || string(got) != "sentinel" {
		t.Fatalf("-j invoked decorator: err=%v input=%q", err, got)
	}

	t.Setenv("TICKET_DECORATOR", "none")
	plain, code := runCLIHuman(t, "show", id)
	if code != 0 || !strings.Contains(plain, id+" Decorator test") || strings.Contains(plain, "decorated output") {
		t.Fatalf("none decorator should use plain output: exit=%d out=%q", code, plain)
	}
	if got, err := os.ReadFile(inputPath); err != nil || string(got) != "sentinel" {
		t.Fatalf("none decorator invoked renderer: err=%v input=%q", err, got)
	}
}

func TestMarkdownDecoratorFailuresAreReported(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	if out, stderr, code := runCLIHumanError(t, "list"); code != 0 || !strings.Contains(out, "STATE") || stderr != "" {
		t.Fatalf("unset decorator baseline: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}

	t.Setenv("TICKET_DECORATOR", filepath.Join(dir, "missing-renderer"))
	if out, stderr, code := runCLIHumanError(t, "list"); code == 0 || out != "" || !strings.Contains(stderr, "Markdown decorator") || !strings.Contains(stderr, "missing-renderer") {
		t.Fatalf("missing decorator: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}

	renderer := installTestHelper(t, filepath.Join(dir, "decorator-helper"), "decorator-fail")
	t.Setenv("TICKET_DECORATOR", renderer)
	if out, stderr, code := runCLIHumanError(t, "list"); code == 0 || out != "" || !strings.Contains(stderr, "renderer stderr") || !strings.Contains(stderr, "Markdown decorator") {
		t.Fatalf("failing decorator: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
}
