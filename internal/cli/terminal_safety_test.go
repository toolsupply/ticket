package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/toolsupply/ticket/internal/contract"
)

func hasUnsafeTerminalControl(value string, allowNewline bool) bool {
	for _, r := range value {
		if allowNewline && r == '\n' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return true
		}
	}
	return false
}

func TestSanitizeTerminalTextRemovesControlsAndPreservesMultilineNewlines(t *testing.T) {
	input := "title\x1b]52;c;secret\a\nnext\r\t\x00\x1f\x7f\u0085✓"
	got := safeMultiline(input)
	if hasUnsafeTerminalControl(got, true) || !strings.Contains(got, "\n") || strings.ContainsRune(got, '\r') || strings.ContainsRune(got, '\t') {
		t.Fatalf("unsafe multiline output: %q", got)
	}
	if single := safeSingleLine(input); hasUnsafeTerminalControl(single, false) || strings.Contains(single, "\n") {
		t.Fatalf("unsafe single-line output: %q", single)
	}
}

func TestHumanOutputSanitizesStoredControlsWithoutChangingJSON(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLIHuman(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	title := "bad\x1b[31m\x1b]52;c;secret\a\t\u0085\x7f☃ title"
	objective := "first\nsecond\x1b]8;;https://evil.example\a\t\r\u0085\x7f☃"
	created := exactlyOneJSONObject(t, mustCLI(t, "create", title, objective))
	id := created["id"].(string)
	if out, code := runCLI(t, "claim", id, "--actor", "agent"); code != 0 {
		t.Fatalf("claim: exit=%d out=%q", code, out)
	}
	taskPath := filepath.Join(dir, "tickets", id, "TASK.md")
	storedBefore, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	showJSON, code := runCLI(t, "show", id)
	if code != 0 {
		t.Fatalf("show JSON: exit=%d out=%q", code, showJSON)
	}
	var machine map[string]any
	if err := json.Unmarshal([]byte(showJSON), &machine); err != nil {
		t.Fatal(err)
	}
	if machine["title"] != title {
		t.Fatalf("JSON title changed: %#v", machine["title"])
	}
	var machineBefore map[string]any
	if err := json.Unmarshal([]byte(showJSON), &machineBefore); err != nil {
		t.Fatal(err)
	}
	human, code := runCLIHuman(t, "show", id)
	if code != 0 || hasUnsafeTerminalControl(human, true) || !strings.Contains(human, "first\nsecond") {
		t.Fatalf("unsafe human show: exit=%d output=%q", code, human)
	}
	markdown, code := runCLIHuman(t, "show", id, "--markdown")
	if code != 0 || hasUnsafeTerminalControl(markdown, true) {
		t.Fatalf("unsafe Markdown show: exit=%d output=%q", code, markdown)
	}
	list, code := runCLIHuman(t, "list", "--state", "all")
	if code != 0 || hasUnsafeTerminalControl(list, true) || !strings.Contains(list, "@agent") {
		t.Fatalf("unsafe list output: exit=%d output=%q", code, list)
	}
	renderer := installTestHelper(t, filepath.Join(dir, "decorator-helper"), "decorator-record")
	decoratorInput := filepath.Join(dir, "decorator-input")
	t.Setenv("TICKET_DECORATOR", renderer)
	t.Setenv("DECORATOR_INPUT", decoratorInput)
	if out, code := runCLIHuman(t, "show", id); code != 0 || out != "decorated output\n" {
		t.Fatalf("decorated show: exit=%d out=%q", code, out)
	}
	var titles []string
	originalTitleWriter := setInteractiveTerminalTitle
	setInteractiveTerminalTitle = func(value string) { titles = append(titles, value) }
	t.Cleanup(func() { setInteractiveTerminalTitle = originalTitleWriter })
	shellOut, shellErr, shellCode := runShell(t, "show "+id+"\nexit\n")
	if shellCode != 0 || hasUnsafeTerminalControl(shellOut, true) || hasUnsafeTerminalControl(shellErr, true) {
		t.Fatalf("unsafe interactive output: code=%d stdout=%q stderr=%q", shellCode, shellOut, shellErr)
	}
	for _, titleValue := range titles {
		if hasUnsafeTerminalControl(titleValue, false) {
			t.Fatalf("unsafe interactive title: %q", titleValue)
		}
	}
	showJSONAfter, code := runCLI(t, "show", id)
	if code != 0 {
		t.Fatalf("show JSON after human output: exit=%d out=%q", code, showJSONAfter)
	}
	var machineAfter map[string]any
	if err := json.Unmarshal([]byte(showJSONAfter), &machineAfter); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(machineBefore, machineAfter) {
		t.Fatalf("JSON changed after human rendering:\nbefore=%v\nafter=%v", machineBefore, machineAfter)
	}
	storedAfter, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storedBefore, storedAfter) {
		t.Fatalf("TASK.md changed after human rendering")
	}
}

func TestMarkdownDecoratorReceivesSanitizedInput(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLIHuman(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	title := "decorator\x1b[31m title\a"
	id := exactlyOneJSONObject(t, mustCLI(t, "create", title, "body\x1b]52;c;secret\a"))["id"].(string)
	inputPath := filepath.Join(dir, "decorator-input")
	renderer := installTestHelper(t, filepath.Join(dir, "decorator-helper"), "decorator-record")
	t.Setenv("TICKET_DECORATOR", renderer)
	t.Setenv("DECORATOR_INPUT", inputPath)
	if out, code := runCLIHuman(t, "show", id); code != 0 || out != "decorated output\n" {
		t.Fatalf("decorated show: exit=%d out=%q", code, out)
	}
	data, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if hasUnsafeTerminalControl(string(data), true) {
		t.Fatalf("decorator received unsafe Markdown: %q", data)
	}
}

func TestHumanErrorMessageSanitizesControls(t *testing.T) {
	message := "bad\x1b[31m message\a"
	got := humanErrorMessage(contract.NewError(contract.ErrInvalidArgument, message, nil))
	if hasUnsafeTerminalControl(got, false) {
		t.Fatalf("unsafe human error: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatal("sanitized error is not valid UTF-8")
	}
}
