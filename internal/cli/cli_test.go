package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/store"
)

// runCLI executes one invocation with cwd and captures stdout.
func runCLI(t *testing.T, args ...string) (out string, code int) {
	t.Helper()
	var buf bytes.Buffer
	jsonArgs := append(append([]string{}, args...), "-j")
	code = Run(jsonArgs, &buf)
	return buf.String(), code
}

func runCLIHuman(t *testing.T, args ...string) (out string, code int) {
	t.Helper()
	var buf bytes.Buffer
	code = Run(args, &buf)
	return buf.String(), code
}

func runCLIHumanError(t *testing.T, args ...string) (out, stderr string, code int) {
	t.Helper()
	old := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writer
	var buf bytes.Buffer
	code = Run(args, &buf)
	_ = writer.Close()
	os.Stderr = old
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	return buf.String(), string(data), code
}

func runCLIHumanStdin(t *testing.T, input string, args ...string) (out, stderr string, code int) {
	t.Helper()
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stdinWriter.WriteString(input); err != nil {
		t.Fatal(err)
	}
	_ = stdinWriter.Close()
	defer stdinReader.Close()

	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdin, oldStderr := os.Stdin, os.Stderr
	os.Stdin, os.Stderr = stdinReader, stderrWriter
	var stdout bytes.Buffer
	code = Run(args, &stdout)
	_ = stderrWriter.Close()
	os.Stdin, os.Stderr = oldStdin, oldStderr
	data, err := io.ReadAll(stderrReader)
	_ = stderrReader.Close()
	if err != nil {
		t.Fatal(err)
	}
	return stdout.String(), string(data), code
}

func runCLIStdin(t *testing.T, input string, args ...string) (out string, code int) {
	t.Helper()
	old := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString(input); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	os.Stdin = reader
	defer func() { os.Stdin = old; _ = reader.Close() }()
	return runCLI(t, args...)
}

// exactlyOneJSONObject asserts the output is exactly one compact JSON
// object followed by LF.
func exactlyOneJSONObject(t *testing.T, out string) map[string]any {
	t.Helper()
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("output does not end with LF: %q", out)
	}
	body := out[:len(out)-1]
	if strings.Contains(body, "\n") {
		t.Fatalf("more than one line: %q", out)
	}
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(body))
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("not one JSON object: %v", err)
	}
	if dec.More() {
		t.Fatalf("trailing data after object: %q", out)
	}
	return m
}

// C01: success envelopes are exactly one JSON object plus LF.
func TestVersionEnvelope(t *testing.T) {
	out, code := runCLI(t, "version")
	if code != 0 {
		t.Fatalf("exit=%d out=%q", code, out)
	}
	m := exactlyOneJSONObject(t, out)
	if m["api_version"].(float64) != 1 || m["storage_version"].(float64) != 1 {
		t.Fatalf("versions: %v", m)
	}
}

// C02: unknown flag -> stable invocation error + nonzero exit.
func TestUnknownFlag(t *testing.T) {
	out, code := runCLI(t, "list", "--nope")
	if code == 0 {
		t.Fatalf("expected failure, got %q", out)
	}
	m := exactlyOneJSONObject(t, out)
	errObj, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error envelope: %v", m)
	}
	if errObj["code"] != "invalid_argument" {
		t.Fatalf("code=%v", errObj["code"])
	}
}

func TestAlternateRepositoryAndInitPathAreRejected(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "list", "--repo", filepath.Join(dir, "other")); code == 0 {
		t.Fatalf("--repo was accepted: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "other")); !os.IsNotExist(err) {
		t.Fatalf("--repo created an alternate path: %v", err)
	}
	if out, code := runCLI(t, "init", "other"); code == 0 {
		t.Fatalf("init path was accepted: %q", out)
	} else if !strings.Contains(out, "use TICKET_REPOSITORY or a selected scope to choose the target") {
		t.Fatalf("stale init path error: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "tickets")); !os.IsNotExist(err) {
		t.Fatalf("rejected init created tickets: %v", err)
	}
}

func TestTicketRootSelectsRepository(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("TICKET_REPOSITORY", "")
	t.Setenv("TICKET_ROOT", filepath.Join("tickets", "module-a"))
	if out, code := runCLIHuman(t, "init"); code != 0 || !strings.Contains(out, "module-a") {
		t.Fatalf("init selected root: exit=%d out=%q", code, out)
	}
	created, code := runCLIHuman(t, "create", "Module work", "Implement module work")
	if code != 0 {
		t.Fatalf("create selected root: exit=%d out=%q", code, created)
	}
	id := strings.Fields(created)[1]
	if _, err := os.Stat(filepath.Join(dir, "tickets", "module-a", id, "TASK.md")); err != nil {
		t.Fatalf("selected ticket root missing ticket: %v", err)
	}
}

func TestTicketRepositorySelectsRepositoryAndWinsOverLegacyRoot(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("TICKET_REPOSITORY", filepath.Join("tickets", "module-a"))
	t.Setenv("TICKET_ROOT", filepath.Join("tickets", "legacy"))
	if out, code := runCLIHuman(t, "init"); code != 0 || !strings.Contains(out, "module-a") {
		t.Fatalf("init selected repository: exit=%d out=%q", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "tickets", "module-a", "config.json")); err != nil {
		t.Fatalf("repository root missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tickets", "legacy")); !os.IsNotExist(err) {
		t.Fatalf("legacy root unexpectedly initialized: %v", err)
	}
}

func TestDuplicateFlag(t *testing.T) {
	out, code := runCLI(t, "list", "--limit", "5", "--limit", "6")
	if code == 0 {
		t.Fatalf("expected duplicate flag error: %q", out)
	}
	m := exactlyOneJSONObject(t, out)
	if m["error"].(map[string]any)["code"] != "invalid_argument" {
		t.Fatalf("code=%v", m["error"])
	}
}

func TestUnknownCommand(t *testing.T) {
	out, code := runCLI(t, "frobnicate")
	if code == 0 {
		t.Fatalf("expected unknown command: %q", out)
	}
	m := exactlyOneJSONObject(t, out)
	if m["error"].(map[string]any)["code"] != "invalid_argument" {
		t.Fatalf("code=%v", m["error"])
	}
}

func TestBareTicketIDUsesShowCommand(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Bare show", "Show the ticket."))
	id := created["id"].(string)
	shown := exactlyOneJSONObject(t, mustCLI(t, id, "--full"))
	if shown["id"] != id || shown["title"] != "Bare show" {
		t.Fatalf("bare ticket show: %v", shown)
	}
	if !strings.Contains(shown["body"].(string), "Show the ticket.") {
		t.Fatalf("bare ticket body: %v", shown["body"])
	}
}

func TestBareTicketShowsAuthoritativeCurrentSummary(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("TICKET_CURRENT", "")
	t.Setenv("TICKET_REPOSITORY", "")
	t.Setenv("TICKET_ROOT", "")
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Current summary", "Show current context."))["id"].(string)
	if _, code := runCLIHuman(t, "show", id); code != 0 {
		t.Fatalf("select current ticket: exit=%d", code)
	}
	if out, code := runCLIHuman(t); code != 0 || out != id+"  open  Current summary\n" {
		t.Fatalf("bare current summary: exit=%d out=%q", code, out)
	}
	mustCLI(t, "state", id, "rejected")
	if out, code := runCLIHuman(t); code != 0 || out != id+"  rejected  Current summary\n" {
		t.Fatalf("authoritative bare current summary: exit=%d out=%q", code, out)
	}
}

func TestTopLevelHelpAndVersionAliases(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("TICKET_CURRENT", "")
	t.Setenv("TICKET_REPOSITORY", "")
	t.Setenv("TICKET_ROOT", "")
	for _, args := range [][]string{{}, {"--help"}, {"-h"}} {
		out, code := runCLIHuman(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit=%d out=%q", args, code, out)
		}
		if out != topLevelHelp {
			t.Fatalf("%v: unexpected help:\n%s", args, out)
		}
	}
	for _, args := range [][]string{{"--version"}, {"-v"}} {
		out, code := runCLIHuman(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit=%d out=%q", args, code, out)
		}
		want := fmt.Sprintf("ticket %s (api %d, storage %d)\n", Version, APIVersion, StorageVersion)
		if out != want {
			t.Fatalf("%v: expected %q, got %q", args, want, out)
		}
	}
	for _, args := range [][]string{{"--help", "-j"}, {"-h", "--json"}, {"--version", "-j"}, {"-v", "--json"}} {
		out, code := runCLI(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit=%d out=%q", args, code, out)
		}
		exactlyOneJSONObject(t, out)
	}
	maintenance := strings.Index(topLevelHelp, "Maintenance:\n")
	if maintenance < 0 || maintenance < strings.Index(topLevelHelp, "Review:\n") || strings.Contains(topLevelHelp, "Repository:\n") {
		t.Fatalf("maintenance is not the final help group: %q", topLevelHelp)
	}
	if !strings.Contains(topLevelHelp[maintenance:], "  check      Validate the repository\n") {
		t.Fatalf("check is not in maintenance: %q", topLevelHelp)
	}
	for _, line := range []string{
		"  ready      Inspect eligible work\n",
		"  next       Select the next eligible ticket\n",
		"  wait       Wait for eligible work\n",
		"  watch      Watch live ticket activity\n",
	} {
		if !strings.Contains(topLevelHelp, line) {
			t.Fatalf("%q is missing from top-level help: %q", line, topLevelHelp)
		}
	}
	if strings.Contains(topLevelHelp, "[open|review]") {
		t.Fatalf("top-level help should not expose queue markers: %q", topLevelHelp)
	}
	if strings.Contains(topLevelHelp, "Interactive shell:") || strings.Contains(topLevelHelp, "-i, --interactive") {
		t.Fatalf("top-level help should not document interactive mode: %q", topLevelHelp)
	}
	out, code := runCLI(t, "help")
	if code != 0 {
		t.Fatalf("JSON top-level help: exit=%d out=%q", code, out)
	}
	var summary struct {
		Commands []struct {
			Name string `json:"name"`
		} `json:"commands"`
	}
	if m := exactlyOneJSONObject(t, out); m["commands"] == nil {
		t.Fatalf("JSON top-level help has no commands: %v", m)
	}
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("JSON top-level help: %v", err)
	}
	readyFound := false
	for _, command := range summary.Commands {
		if command.Name == "ready" {
			readyFound = true
		}
	}
	if !readyFound {
		t.Fatal("ready is missing from JSON top-level help")
	}
}

func TestHelp(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("TICKET_CURRENT", "")
	t.Setenv("TICKET_REPOSITORY", "")
	t.Setenv("TICKET_ROOT", "")
	if out, code := runCLIHuman(t, "help"); code != 0 || out != topLevelHelp {
		t.Fatalf("human help: exit=%d out=%q", code, out)
	}
	for _, args := range [][]string{{"help", "create"}, {"create", "-h"}, {"list", "--help"}} {
		out, code := runCLIHuman(t, args...)
		if code != 0 || !strings.Contains(out, "- ") {
			t.Fatalf("human %v: exit=%d out=%q", args, code, out)
		}
		if !strings.HasPrefix(out, "\n") || !strings.Contains(out, "\n\nExamples:\n") || !strings.HasSuffix(out, "\n\n") {
			t.Fatalf("human %v lacks spacious layout: %q", args, out)
		}
	}
	if out, code := runCLI(t, "help", "grep"); code != 0 || !strings.Contains(out, "grep") {
		t.Fatalf("grep help: exit=%d out=%q", code, out)
	}
	for _, args := range [][]string{{"help", "ready"}, {"ready", "-h"}, {"ready", "--help"}} {
		out, code := runCLIHuman(t, args...)
		if code != 0 || !strings.Contains(out, "ready - Inspect eligible work without claiming.") {
			t.Fatalf("ready help: args=%v exit=%d out=%q", args, code, out)
		}
		jsonOut, jsonCode := runCLI(t, args...)
		if jsonCode != 0 || exactlyOneJSONObject(t, jsonOut)["command"] != "ready" {
			t.Fatalf("JSON ready help: args=%v exit=%d out=%q", args, jsonCode, jsonOut)
		}
	}
	out, code := runCLIHuman(t, "help", "delete")
	if code != 0 || !strings.Contains(out, "delete - Permanently delete tickets.") || strings.Contains(strings.ToLower(out), "folder") || strings.Contains(out, "--actor") {
		t.Fatalf("delete help: exit=%d out=%q", code, out)
	}
	for _, args := range [][]string{
		{"help"},
		{"help", "list"},
		{"list", "--help"},
	} {
		out, code := runCLI(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit=%d out=%q", args, code, out)
		}
		if len(out) == 0 {
			t.Fatalf("%v: empty output", args)
		}
	}
}

func TestCommandHelpUsesCompactUsageBeforeFlags(t *testing.T) {
	for _, command := range []string{
		"create", "delete", "bump", "list", "grep", "show", "edit", "submit",
		"hold", "review", "open", "status", "path", "update", "claim", "release", "actor",
		"close", "approve", "reject", "ready", "next", "wait", "info", "help",
	} {
		out, code := runCLIHuman(t, "help", command)
		if code != 0 {
			t.Fatalf("%s help: exit=%d out=%q", command, code, out)
		}
		usage := strings.Index(out, "Usage:\n")
		options := strings.Index(out, "Options:\n")
		if usage < 0 || options < 0 || usage > options || strings.Contains(out, "Flags:") || strings.Contains(out, "Positional:") {
			t.Fatalf("%s help does not use compact usage before options: %q", command, out)
		}
	}
	out, code := runCLIHuman(t, "help", "list")
	if code != 0 || !strings.Contains(out, "ticket list [options] [STATE...|ID...]\n") {
		t.Fatalf("list usage: exit=%d out=%q", code, out)
	}
	for command, want := range map[string]string{
		"ready":   "ticket ready [options] [open|review]",
		"next":    "ticket next [options] [open|review]",
		"wait":    "ticket wait [options] [open|review]",
		"show":    "ticket show [options] [ID]",
		"status":  "ticket status [options] [ID]",
		"edit":    "ticket edit [options] [ID]",
		"path":    "ticket path [options] [ID]",
		"bump":    "ticket bump [options] [ID]",
		"update":  "ticket update [options] [ID]",
		"claim":   "ticket claim [options] [ID]",
		"release": "ticket release [options] [ID]",
		"actor":   "ticket actor [options]",
		"info":    "ticket info [options]",
		"submit":  "ticket submit [options] [ID]",
		"hold":    "ticket hold [options] [ID]",
		"open":    "ticket open [options] [ID] [HANDOFF]",
		"reject":  "ticket reject [options] [ID] [OUTCOME]",
		"approve": "ticket approve [options] [ID...|review|all]",
		"close":   "ticket close [options] [ID...|STATE...|all]",
	} {
		out, code := runCLIHuman(t, "help", command)
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("%s usage: exit=%d want=%q out=%q", command, code, want, out)
		}
	}
}

func TestGlobalOptionsHelpAndDebugPlacement(t *testing.T) {
	t.Chdir(t.TempDir())
	out, code := runCLIHuman(t, "help", "options")
	if code != 0 {
		t.Fatalf("options help: exit=%d out=%q", code, out)
	}
	for _, want := range []string{"-c, --config", "--scope", "-i, --interactive", "-j, --json", "-h, --help", "--debug", "stack trace", "$ ticket --config ~/.config/ticket/config.json list"} {
		if !strings.Contains(out, want) {
			t.Fatalf("options help missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "$ ticket -i") || strings.Contains(out, "$ ticket --interactive") {
		t.Fatalf("options help should not list interactive examples: %q", out)
	}
	optionsSection := strings.SplitN(out, "Options:\n", 2)
	if len(optionsSection) != 2 || !strings.HasPrefix(optionsSection[1], "  --scope") {
		t.Fatalf("options help should list --scope first: %q", out)
	}
	if strings.Contains(out, "-j -i") || strings.Contains(out, "promptless") || strings.Contains(out, "newline-delimited") {
		t.Fatalf("options help should not document interactive JSON streaming: %q", out)
	}
	jsonOut, code := runCLI(t, "help", "options")
	if code != 0 {
		t.Fatalf("JSON options help: exit=%d out=%q", code, jsonOut)
	}
	options := exactlyOneJSONObject(t, jsonOut)
	if options["command"] != "options" {
		t.Fatalf("JSON options help command: %v", options)
	}
	if _, code := runCLIHuman(t, "--debug", "help", "options"); code != 0 {
		t.Fatalf("leading --debug was not accepted")
	}
	if _, code := runCLIHuman(t, "help", "options", "--debug"); code != 0 {
		t.Fatalf("command-local --debug was not accepted")
	}
}

func TestCommandHelpKeepsUniversalFlagsTogetherAtEnd(t *testing.T) {
	for _, command := range []string{"create", "edit", "grep", "check", "actor"} {
		out, code := runCLIHuman(t, "help", command)
		if code != 0 {
			t.Fatalf("%s help: exit=%d out=%q", command, code, out)
		}
		options := strings.SplitN(out, "Options:\n", 2)
		if len(options) != 2 {
			t.Fatalf("%s help has no options section: %q", command, out)
		}
		section := strings.SplitN(options[1], "\n\nExamples:", 2)[0]
		lines := strings.Split(strings.TrimSuffix(section, "\n"), "\n")
		if len(lines) < 2 || !strings.Contains(lines[len(lines)-2], "-j, --json") ||
			!strings.Contains(lines[len(lines)-1], "-h, --help") {
			t.Fatalf("%s help does not end options with -j and -h: %q", command, section)
		}
	}
}

func TestCommandHelpMovesShortOptionsToBottom(t *testing.T) {
	out, code := runCLIHuman(t, "help", "close")
	if code != 0 {
		t.Fatalf("close help: exit=%d out=%q", code, out)
	}
	options := strings.SplitN(out, "Options:\n", 2)
	if len(options) != 2 {
		t.Fatalf("close help has no options section: %q", out)
	}
	section := strings.SplitN(options[1], "\n\nExamples:", 2)[0]
	lines := strings.Split(strings.TrimSuffix(section, "\n"), "\n")
	shortStart := -1
	for i, line := range lines {
		if strings.Contains(line, ", --") {
			shortStart = i
			break
		}
	}
	if shortStart < 0 {
		t.Fatalf("close help has no short options: %q", section)
	}
	for _, line := range lines[shortStart:] {
		if !strings.Contains(line, ", --") {
			t.Fatalf("long-only option follows short option: %q", section)
		}
	}
}

func TestActorHelpDescribesIdentityDiscovery(t *testing.T) {
	out, code := runCLI(t, "help", "actor")
	if code != 0 || strings.Contains(out, "--actor") || !strings.Contains(out, "Show the effective actor identity.") {
		t.Fatalf("actor help: exit=%d out=%q", code, out)
	}
}

func TestMessageOptionsPrecedeJSONInHelp(t *testing.T) {
	for _, command := range []string{"submit", "open", "close", "approve", "reject", "release"} {
		out, code := runCLIHuman(t, "help", command)
		if code != 0 {
			t.Fatalf("%s help: exit=%d out=%q", command, code, out)
		}
		message := strings.Index(out, "-m, --message")
		jsonFlag := strings.Index(out, "-j, --json")
		if message < 0 || jsonFlag < 0 || message > jsonFlag {
			t.Fatalf("%s message option is not before JSON: %q", command, out)
		}
	}
}

func TestEditorHelpFollowsExamples(t *testing.T) {
	out, code := runCLIHuman(t, "help", "edit")
	if code != 0 {
		t.Fatalf("edit help: exit=%d out=%q", code, out)
	}
	want := "Examples:\n  $ ticket edit\n\nEditor:\n  TICKET_EDITOR, config.editor, VISUAL, EDITOR, then the platform default.\n  Defaults to vi on Unix and notepad.exe on Windows.\n\n"
	if !strings.Contains(out, want) {
		t.Fatalf("editor note is not spaced after examples: %q", out)
	}
}

func TestHumanDefaultAndJSONMode(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out, code := runCLIHuman(t, "init")
	if code != 0 || out != "initialized ./tickets\n" {
		t.Fatalf("human init: exit=%d out=%q", code, out)
	}
	out, code = runCLIHuman(t, "create", "Fix parser", "Parse reliably")
	if code != 0 || !strings.HasPrefix(out, "created 20") {
		t.Fatalf("positional create: exit=%d out=%q", code, out)
	}
	fields := strings.Fields(out)
	if len(fields) < 2 {
		t.Fatalf("create output: %q", out)
	}
	out, code = runCLIHuman(t, "list")
	if code != 0 || !strings.Contains(out, "Fix parser") || !strings.Contains(out, "STATE") ||
		!strings.Contains(out, "open") || !strings.Contains(out, fields[1]) {
		t.Fatalf("human list: exit=%d out=%q", code, out)
	}
	out, code = runCLI(t, "list")
	if code != 0 {
		t.Fatalf("json list: exit=%d out=%q", code, out)
	}
	if _, ok := exactlyOneJSONObject(t, out)["items"]; !ok {
		t.Fatalf("json list: %q", out)
	}
	grepResult := exactlyOneJSONObject(t, mustCLI(t, "grep", "Fix parser"))
	grepItems, ok := grepResult["items"].([]any)
	if !ok || len(grepItems) != 1 || grepItems[0].(map[string]any)["id"] != fields[1] {
		t.Fatalf("grep result: %v", grepResult)
	}
	stdout, stderr, code := runCLIHumanError(t, "list", "--human")
	if code == 0 || stdout != "" || !strings.Contains(stderr, "Unknown flag --human.") {
		t.Fatalf("removed --human: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestHumanShowUsesSpaciousCanonicalSectionHeadings(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	created, code := runCLIStdin(t, "## Objective\n\nDo the work.\n\n## Handoff\n\nContinue the work.\n", "create", "Readable show")
	if code != 0 {
		t.Fatalf("create: exit=%d out=%q", code, created)
	}
	id := exactlyOneJSONObject(t, created)["id"].(string)
	show, code := runCLIHuman(t, "show", id)
	want := id + " Readable show\n\nstate:      open\npriority:   P2\n\n## Objective\n\nDo the work.\n\n## Handoff\n\nContinue the work.\n\n"
	if code != 0 || show != want {
		t.Fatalf("human show: exit=%d\nout=%q\nwant=%q", code, show, want)
	}
}

func TestSCMLifecycleWrapsRepositoryCommands(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	binDir := filepath.Join(dir, "bin")
	installFakeSCM(t, binDir, "scm-lifecycle")
	logPath := filepath.Join(dir, "scm.log")
	t.Setenv("SCM_LOG", logPath)
	t.Setenv("TICKET_SCM", "git")
	t.Setenv("TICKET_SCM_MODE", "sync")

	created := exactlyOneJSONObject(t, mustCLI(t, "create", "SCM ticket", "Exercise SCM lifecycle."))
	if _, err := os.Stat(filepath.Join(dir, "tickets", created["id"].(string), "TASK.md")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 9 || !strings.HasSuffix(lines[0], "|rev-parse --show-toplevel") ||
		strings.HasSuffix(lines[1], "|rev-parse --abbrev-ref --symbolic-full-name @{u}") == false ||
		!strings.HasSuffix(lines[2], "|pull --ff-only") ||
		strings.TrimSpace(lines[3]) != strings.TrimSuffix(lines[0], "rev-parse --show-toplevel")+"add -- "+created["id"].(string)+"/TASK.md" ||
		!strings.Contains(lines[4], "|diff --cached --quiet -- "+created["id"].(string)+"/TASK.md") ||
		!strings.Contains(lines[5], "|commit --only -m ticket: create ") ||
		!strings.HasSuffix(lines[5], " -- "+created["id"].(string)+"/TASK.md") ||
		!strings.HasSuffix(lines[6], "|rev-parse --show-toplevel") ||
		!strings.HasSuffix(lines[7], "|rev-parse --abbrev-ref --symbolic-full-name @{u}") ||
		!strings.HasSuffix(lines[8], "|push -- origin HEAD:refs/heads/main") {
		t.Fatalf("create SCM lifecycle: %q", string(data))
	}
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	claimed := exactlyOneJSONObject(t, mustCLI(t, "next", "--claim", "--actor", "agent"))
	claimedItem := claimed["item"].(map[string]any)
	if claimedItem["id"] != created["id"] || claimedItem["assignee"] != "agent" {
		t.Fatalf("SCM next claim: %v", claimed)
	}
	data, err = os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines = strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 9 || !strings.HasSuffix(lines[0], "|rev-parse --show-toplevel") ||
		!strings.HasSuffix(lines[1], "|rev-parse --abbrev-ref --symbolic-full-name @{u}") ||
		!strings.HasSuffix(lines[2], "|pull --ff-only") ||
		strings.TrimSpace(lines[3]) != strings.TrimSuffix(lines[0], "rev-parse --show-toplevel")+"add -- "+created["id"].(string)+"/TASK.md" ||
		!strings.Contains(lines[4], "|diff --cached --quiet -- "+created["id"].(string)+"/TASK.md") ||
		!strings.Contains(lines[5], "|commit --only -m ticket: next ") ||
		!strings.HasSuffix(lines[5], " -- "+created["id"].(string)+"/TASK.md") ||
		!strings.HasSuffix(lines[6], "|rev-parse --show-toplevel") ||
		!strings.HasSuffix(lines[7], "|rev-parse --abbrev-ref --symbolic-full-name @{u}") ||
		!strings.HasSuffix(lines[8], "|push -- origin HEAD:refs/heads/main") {
		t.Fatalf("next SCM lifecycle: %q", string(data))
	}
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := runCLI(t, "list"); code != 0 {
		t.Fatalf("list: exit=%d out=%q", code, out)
	}
	data, err = os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); !strings.HasSuffix(got, "|pull --ff-only") || strings.Count(got, "\n") != 2 {
		t.Fatalf("read-only SCM lifecycle: %q", got)
	}
}

func TestSCMRetryPublishesPendingMutationAfterPushFailure(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Retry SCM", "Publish the pending claim."))
	id := created["id"].(string)
	binDir := filepath.Join(dir, "bin")
	installFakeSCM(t, binDir, "scm-push-retry")
	logPath := filepath.Join(dir, "scm.log")
	t.Setenv("SCM_LOG", logPath)
	t.Setenv("SCM_STATE", filepath.Join(dir, "scm-committed"))
	t.Setenv("TICKET_SCM", "git")
	t.Setenv("TICKET_SCM_MODE", "sync")
	t.Setenv("FAIL_PUSH", "1")
	if out, code := runCLI(t, "claim", id, "--actor", "agent"); code == 0 || errCode(t, out) != "io_error" {
		t.Fatalf("initial push failure: exit=%d out=%q", code, out)
	}
	t.Setenv("FAIL_PUSH", "0")
	if out, code := runCLI(t, "claim", id, "--actor", "agent"); code != 0 {
		t.Fatalf("retry claim: exit=%d out=%q", code, out)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 17 || lines[0] != "rev-parse --show-toplevel" ||
		lines[1] != "rev-parse --abbrev-ref --symbolic-full-name @{u}" || lines[2] != "pull --ff-only" ||
		lines[3] != "add -- "+id+"/TASK.md" || lines[4] != "diff --cached --quiet -- "+id+"/TASK.md" ||
		!strings.HasPrefix(lines[5], "commit --only -m ticket: claim ") ||
		lines[5] != "commit --only -m ticket: claim "+id+" -- "+id+"/TASK.md" ||
		lines[6] != "rev-parse --show-toplevel" || lines[7] != "rev-parse --abbrev-ref --symbolic-full-name @{u}" || lines[8] != "push -- origin HEAD:refs/heads/main" ||
		lines[9] != "rev-parse --show-toplevel" || lines[10] != "rev-parse --abbrev-ref --symbolic-full-name @{u}" ||
		lines[11] != "pull --ff-only" || lines[12] != "add -- "+id+"/TASK.md" ||
		lines[13] != "diff --cached --quiet -- "+id+"/TASK.md" ||
		lines[14] != "rev-parse --show-toplevel" || lines[15] != "rev-parse --abbrev-ref --symbolic-full-name @{u}" || lines[16] != "push -- origin HEAD:refs/heads/main" {
		t.Fatalf("pending publish lifecycle: %q", string(data))
	}
}

func TestSCMRetryCommitsPendingMutationAfterCommitFailure(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Retry commit", "Recover a pending local commit."))
	id := created["id"].(string)
	binDir := filepath.Join(dir, "bin")
	installFakeSCM(t, binDir, "scm-commit-retry")
	logPath := filepath.Join(dir, "scm.log")
	t.Setenv("SCM_LOG", logPath)
	t.Setenv("SCM_STATE", filepath.Join(dir, "scm-committed"))
	t.Setenv("TICKET_SCM", "git")
	t.Setenv("TICKET_SCM_MODE", "sync")
	t.Setenv("FAIL_COMMIT", "1")
	if out, code := runCLI(t, "claim", id, "--actor", "agent"); code == 0 || errCode(t, out) != "io_error" {
		t.Fatalf("initial commit failure: exit=%d out=%q", code, out)
	}
	t.Setenv("FAIL_COMMIT", "0")
	if out, code := runCLI(t, "claim", id, "--actor", "agent"); code != 0 {
		t.Fatalf("retry claim: exit=%d out=%q", code, out)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 15 || lines[0] != "rev-parse --show-toplevel" ||
		lines[1] != "rev-parse --abbrev-ref --symbolic-full-name @{u}" || lines[2] != "pull --ff-only" ||
		lines[3] != "add -- "+id+"/TASK.md" || lines[4] != "diff --cached --quiet -- "+id+"/TASK.md" ||
		lines[5] != "commit --only -m ticket: claim "+id+" -- "+id+"/TASK.md" ||
		lines[6] != "rev-parse --show-toplevel" || lines[7] != "rev-parse --abbrev-ref --symbolic-full-name @{u}" ||
		lines[8] != "pull --ff-only" || lines[9] != "add -- "+id+"/TASK.md" ||
		lines[10] != "diff --cached --quiet -- "+id+"/TASK.md" ||
		lines[11] != "commit --only -m ticket: claim "+id+" -- "+id+"/TASK.md" ||
		lines[12] != "rev-parse --show-toplevel" || lines[13] != "rev-parse --abbrev-ref --symbolic-full-name @{u}" || lines[14] != "push -- origin HEAD:refs/heads/main" {
		t.Fatalf("pending commit lifecycle: %q", string(data))
	}
}

func TestSCMReloadsConfigAfterSynchronization(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	binDir := filepath.Join(dir, "bin")
	installFakeSCM(t, binDir, "scm-reload-config")
	t.Setenv("TICKET_SCM", "git")
	t.Setenv("TICKET_SCM_MODE", "sync")
	if out, code := runCLI(t, "list"); code == 0 || errCode(t, out) != "unsupported_version" {
		t.Fatalf("post-sync config validation: exit=%d out=%q", code, out)
	}
}

func TestSCMRejectsPostSyncLocalReplacement(t *testing.T) {
	for _, kind := range []string{"symlink", "regular"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if out, code := runCLI(t, "init"); code != 0 {
				t.Fatalf("init: exit=%d out=%q", code, out)
			}
			sentinel := filepath.Join(dir, "sentinel")
			if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			installFakeSCM(t, filepath.Join(dir, "bin"), "scm-replace-local")
			t.Setenv("SCM_REPLACE_KIND", kind)
			t.Setenv("SCM_SENTINEL", sentinel)
			t.Setenv("TICKET_SCM", "git")
			t.Setenv("TICKET_SCM_MODE", "sync")
			out, code := runCLI(t, "list")
			wantCode := "invalid_repository"
			if runtime.GOOS == "windows" {
				wantCode = "io_error"
			}
			if code == 0 || errCode(t, out) != wantCode {
				t.Fatalf("post-sync %s replacement: exit=%d out=%q", kind, code, out)
			}
			if got, err := os.ReadFile(sentinel); err != nil || string(got) != "keep" {
				t.Fatalf("sentinel changed: %q (%v)", got, err)
			}
		})
	}
}

func TestSCMTrackedLocalReplacementDoesNotCreateRuntimeLock(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	installFakeSCM(t, filepath.Join(dir, "bin"), "scm-replace-local-dir")
	t.Setenv("TICKET_SCM", "git")
	t.Setenv("TICKET_SCM_MODE", "sync")
	out, code := runCLI(t, "list")
	wantCode := "invalid_repository"
	if runtime.GOOS == "windows" {
		wantCode = "io_error"
	}
	if code == 0 || errCode(t, out) != wantCode {
		t.Fatalf("tracked local replacement: exit=%d out=%q", code, out)
	}
	if runtime.GOOS == "windows" {
		if _, err := os.Lstat(filepath.Join(dir, "tickets", ".local", "lock")); err != nil {
			t.Fatalf("live Windows lock disappeared after blocked replacement: %v", err)
		}
		return
	}
	if _, err := os.Lstat(filepath.Join(dir, "tickets", ".local", "lock")); !os.IsNotExist(err) {
		t.Fatalf("post-sync validation created a runtime lock: %v", err)
	}
}

func TestSCMRealGitRejectsUpstreamLocalSymlinkWithoutMarkerWrites(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	remote := filepath.Join(base, "remote.git")
	local := filepath.Join(base, "local")
	upstream := filepath.Join(base, "upstream")
	external := filepath.Join(base, "external")
	for _, dir := range []string{local, external} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.InitRoot(filepath.Join(local, "tickets")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "sentinel"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	runGit(base, "init", "--bare", remote)
	runGit(local, "init")
	runGit(local, "config", "user.email", "ticket@example.invalid")
	runGit(local, "config", "user.name", "Ticket Test")
	runGit(local, "add", ".")
	runGit(local, "commit", "-m", "initial")
	runGit(local, "branch", "-M", "main")
	runGit(local, "remote", "add", "origin", remote)
	runGit(local, "push", "-u", "origin", "main")
	runGit(remote, "symbolic-ref", "HEAD", "refs/heads/main")
	runGit(base, "clone", remote, upstream)
	runGit(upstream, "config", "user.email", "ticket@example.invalid")
	runGit(upstream, "config", "user.name", "Ticket Test")
	if err := os.RemoveAll(filepath.Join(upstream, "tickets", ".local")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(upstream, "tickets", ".local")); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	runGit(upstream, "add", "-f", "tickets/.local")
	runGit(upstream, "commit", "-m", "replace local runtime state")
	runGit(upstream, "push")

	t.Chdir(local)
	t.Setenv("TICKET_SCM", "git")
	t.Setenv("TICKET_SCM_MODE", "sync")
	_, code := runCLIHuman(t, "create", "Must not publish", "The sync boundary must fail closed.")
	if code == 0 {
		t.Fatal("SCM replacement unexpectedly allowed a mutation")
	}
	if got, err := os.ReadFile(filepath.Join(external, "sentinel")); err != nil || string(got) != "keep" {
		t.Fatalf("external marker changed: %q (%v)", got, err)
	}
	for _, entry := range []string{"current", "change"} {
		if _, err := os.Lstat(filepath.Join(external, entry)); !os.IsNotExist(err) {
			t.Fatalf("out-of-root marker %s was written: %v", entry, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(local, "tickets"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.Contains(entry.Name(), "-") {
			t.Fatalf("mutation continued after replacement: %s", entry.Name())
		}
	}
}

func TestSCMUpdateFailurePreventsMutationInBothOutputModes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	binDir := filepath.Join(dir, "bin")
	installFakeSCM(t, binDir, "scm-update-failure")
	t.Setenv("TICKET_SCM", "git")
	t.Setenv("TICKET_SCM_MODE", "sync")
	if out, code := runCLI(t, "create", "Blocked by SCM", "Should not publish."); code == 0 || errCode(t, out) != "io_error" {
		t.Fatalf("JSON update failure: exit=%d out=%q", code, out)
	} else {
		errorObject := exactlyOneJSONObject(t, out)["error"].(map[string]any)
		if details, ok := errorObject["details"].(map[string]any); ok && details["mutation_applied"] == true {
			t.Fatalf("pre-mutation failure falsely reports applied mutation: %v", errorObject)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "tickets"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "-") && entry.IsDir() {
			t.Fatalf("mutation proceeded after SCM failure: %s", entry.Name())
		}
	}
	stdout, stderr, code := runCLIHumanError(t, "create", "Human SCM failure", "Should also not publish.")
	if code == 0 || stdout != "" || !strings.Contains(stderr, "remote unavailable") {
		t.Fatalf("human update failure: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestSCMFailureAfterCreateReportsAppliedMutation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	binDir := filepath.Join(dir, "bin")
	installFakeSCM(t, binDir, "scm-push-retry")
	t.Setenv("SCM_LOG", filepath.Join(dir, "scm.log"))
	t.Setenv("SCM_STATE", filepath.Join(dir, "scm-committed"))
	t.Setenv("FAIL_PUSH", "1")
	t.Setenv("TICKET_SCM", "git")
	t.Setenv("TICKET_SCM_MODE", "sync")
	out, code := runCLI(t, "create", "SCM create failure", "Inspect the persisted ticket.")
	if code == 0 || errCode(t, out) != "io_error" {
		t.Fatalf("create push failure: exit=%d out=%q", code, out)
	}
	errorObject := exactlyOneJSONObject(t, out)["error"].(map[string]any)
	if !strings.Contains(errorObject["message"].(string), "git push failed") {
		t.Fatalf("create failure message: %v", errorObject)
	}
	details := errorObject["details"].(map[string]any)
	id, _ := details["id"].(string)
	if details["mutation_applied"] != true || id == "" {
		t.Fatalf("create failure details: %v", errorObject)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if view["id"] != id || view["state"] != "open" {
		t.Fatalf("created ticket after SCM failure: %v", view)
	}
}

func TestInteractiveCreateSCMFailureReportsCreatedTicket(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	mode := "scm-push-retry-editor-write"
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), mode)
	installFakeSCM(t, filepath.Join(dir, "bin"), mode)
	t.Setenv("EDITOR", editor)
	t.Setenv("TEST_EDITOR_BODY", "---\nstate: hold\npriority: 2\n---\n# Interactive persisted\n\n## Objective\n\nKeep the created ticket visible after SCM failure.\n")
	t.Setenv("SCM_LOG", filepath.Join(dir, "scm.log"))
	t.Setenv("SCM_STATE", filepath.Join(dir, "scm-committed"))
	t.Setenv("FAIL_PUSH", "1")
	t.Setenv("TICKET_SCM", "git")
	t.Setenv("TICKET_SCM_MODE", "sync")
	t.Setenv("TICKET_CURRENT", "external-current")

	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}}
	err := createWithEditor(ctx, domain.CreateOptions{Title: "Interactive persisted", Priority: 2})
	ce, ok := err.(*contract.Error)
	if !ok || ce.Code != contract.ErrIOError {
		t.Fatalf("interactive SCM failure: %v", err)
	}
	if strings.Contains(ce.Message, "No ticket was created") || !strings.Contains(ce.Message, "Reconcile SCM persistence") {
		t.Fatalf("interactive SCM failure message: %q", ce.Message)
	}
	if ce.Details["mutation_applied"] != true {
		t.Fatalf("interactive SCM failure details: %v", ce.Details)
	}
	id, ok := ce.Details["id"].(string)
	if !ok || id == "" {
		t.Fatalf("interactive SCM failure ID: %v", ce.Details)
	}
	if _, err := os.Stat(filepath.Join(dir, "tickets", id, "TASK.md")); err != nil {
		t.Fatalf("created ticket was not preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tickets", ".local", "current")); !os.IsNotExist(err) {
		t.Fatalf("local current marker after SCM failure: %v", err)
	}
	if !strings.Contains(ce.Message, "TICKET_CURRENT still selects external-current") {
		t.Fatalf("SCM failure lost environment-current diagnostic: %q", ce.Message)
	}
}

func TestSessionEditorCreateSCMFailureSelectsCreatedTicket(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	previous := exactlyOneJSONObject(t, mustCLI(t, "create", "Persistent marker", "Keep this human selection."))["id"].(string)
	if _, code := runCLIHuman(t, "show", previous); code != 0 {
		t.Fatalf("select persistent current: exit=%d", code)
	}
	marker := filepath.Join(dir, "tickets", ".local", "current")
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	mode := "scm-push-retry-editor-write"
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), mode)
	installFakeSCM(t, filepath.Join(dir, "bin"), mode)
	t.Setenv("EDITOR", editor)
	t.Setenv("TEST_EDITOR_BODY", "---\nstate: hold\npriority: 2\n---\n# Session persisted\n\n## Objective\n\nSelect the created ticket after SCM failure.\n")
	t.Setenv("SCM_LOG", filepath.Join(dir, "scm.log"))
	t.Setenv("SCM_STATE", filepath.Join(dir, "scm-committed"))
	t.Setenv("FAIL_PUSH", "1")
	t.Setenv("TICKET_SCM", "git")
	t.Setenv("TICKET_SCM_MODE", "sync")
	t.Setenv("TICKET_CURRENT", "external-current")

	session := &sessionState{current: previous}
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}, session: session}
	err = createWithEditor(ctx, domain.CreateOptions{Title: "Session persisted", Priority: 2})
	ce, ok := err.(*contract.Error)
	if !ok || ce.Code != contract.ErrIOError {
		t.Fatalf("session SCM failure: %v", err)
	}
	id, ok := ce.Details["id"].(string)
	if !ok || id == "" || ce.Details["mutation_applied"] != true {
		t.Fatalf("session SCM failure details: %v", ce.Details)
	}
	if session.current != id {
		t.Fatalf("session current=%q want created ticket %q", session.current, id)
	}
	if !strings.Contains(ce.Message, "Session current ticket is now "+id) || strings.Contains(ce.Message, "TICKET_CURRENT") {
		t.Fatalf("session SCM failure diagnostic: %q", ce.Message)
	}
	after, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("session SCM failure changed persistent current: before=%q after=%q", before, after)
	}
}

func TestSCMFailureAfterSubmitReportsAppliedMutation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "SCM submit failure", "Inspect the submitted ticket."))["id"].(string)
	if out, code := runCLI(t, "claim", id, "--actor", "worker"); code != 0 {
		t.Fatalf("claim: exit=%d out=%q", code, out)
	}
	binDir := filepath.Join(dir, "bin")
	installFakeSCM(t, binDir, "scm-push-retry")
	t.Setenv("SCM_LOG", filepath.Join(dir, "scm.log"))
	t.Setenv("SCM_STATE", filepath.Join(dir, "scm-committed"))
	t.Setenv("FAIL_PUSH", "1")
	t.Setenv("TICKET_SCM", "git")
	t.Setenv("TICKET_SCM_MODE", "sync")
	out, code := runCLI(t, "submit", id, "--actor", "worker")
	if code == 0 || errCode(t, out) != "io_error" {
		t.Fatalf("submit push failure: exit=%d out=%q", code, out)
	}
	errorObject := exactlyOneJSONObject(t, out)["error"].(map[string]any)
	if !strings.Contains(errorObject["message"].(string), "git push failed") {
		t.Fatalf("submit failure message: %v", errorObject)
	}
	details := errorObject["details"].(map[string]any)
	if details["mutation_applied"] != true || details["id"] != id {
		t.Fatalf("submit failure details: %v", errorObject)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if view["id"] != id || view["state"] != "review" {
		t.Fatalf("submitted ticket after SCM failure: %v", view)
	}
}

func TestSCMBatchFailureReportsChangedTicket(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	first := exactlyOneJSONObject(t, mustCLI(t, "create", "Completed first", "Keep the first ticket completed."))["id"].(string)
	second := exactlyOneJSONObject(t, mustCLI(t, "create", "Open second", "Keep the second ticket open."))["id"].(string)
	if first > second {
		first, second = second, first
	}
	if out, code := runCLI(t, "close", first); code != 0 {
		t.Fatalf("close first: exit=%d out=%q", code, out)
	}
	binDir := filepath.Join(dir, "bin")
	installFakeSCM(t, binDir, "scm-push-retry")
	t.Setenv("SCM_LOG", filepath.Join(dir, "scm.log"))
	t.Setenv("SCM_STATE", filepath.Join(dir, "scm-committed"))
	t.Setenv("FAIL_PUSH", "1")
	t.Setenv("TICKET_SCM", "git")
	t.Setenv("TICKET_SCM_MODE", "sync")
	out, code := runCLI(t, "close", first, second)
	if code == 0 || errCode(t, out) != "io_error" {
		t.Fatalf("batch push failure: exit=%d out=%q", code, out)
	}
	errorObject := exactlyOneJSONObject(t, out)["error"].(map[string]any)
	details := errorObject["details"].(map[string]any)
	if details["mutation_applied"] != true || details["id"] != second {
		t.Fatalf("batch failure details: %v", errorObject)
	}
	firstView := exactlyOneJSONObject(t, mustCLI(t, "show", first))
	secondView := exactlyOneJSONObject(t, mustCLI(t, "show", second))
	if firstView["state"] != "completed" || secondView["state"] != "completed" {
		t.Fatalf("batch states after SCM failure: first=%v second=%v", firstView, secondView)
	}
}

func TestCreateStateDependsOnObjective(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	titleOnly, code := runCLIHuman(t, "create", "Title only")
	if code != 0 {
		t.Fatalf("title-only create: exit=%d out=%q", code, titleOnly)
	}
	titleOnlyID := strings.Fields(titleOnly)[1]
	titleOnlyView := exactlyOneJSONObject(t, mustCLI(t, "show", titleOnlyID))
	if titleOnlyView["state"] != "hold" {
		t.Fatalf("title-only state=%v want hold", titleOnlyView["state"])
	}
	withObjective, code := runCLIHuman(t, "create", "Has objective", "Do the work")
	if code != 0 {
		t.Fatalf("objective create: exit=%d out=%q", code, withObjective)
	}
	withObjectiveID := strings.Fields(withObjective)[1]
	withObjectiveView := exactlyOneJSONObject(t, mustCLI(t, "show", withObjectiveID))
	if withObjectiveView["state"] != "open" {
		t.Fatalf("objective state=%v want open", withObjectiveView["state"])
	}
}

func TestCreateAndNewReadObjectiveFromTrailingDash(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	out, code := runCLIStdin(t, "Objective supplied to create.\nSecond line.\n", "create", "Create from stdin", "-")
	if code != 0 {
		t.Fatalf("create from stdin: exit=%d out=%q", code, out)
	}
	created := exactlyOneJSONObject(t, out)
	createID := created["id"].(string)
	createView := exactlyOneJSONObject(t, mustCLI(t, "show", createID, "--full"))
	if !strings.Contains(createView["body"].(string), "Objective supplied to create.\nSecond line.") {
		t.Fatalf("create objective missing: %v", createView["body"])
	}

	out, code = runCLIStdin(t, "Objective supplied to new.\n", "new", "New from stdin", "-")
	if code != 0 {
		t.Fatalf("new from stdin: exit=%d out=%q", code, out)
	}
	created = exactlyOneJSONObject(t, out)
	newID := created["id"].(string)
	newView := exactlyOneJSONObject(t, mustCLI(t, "show", newID, "--full"))
	if !strings.Contains(newView["body"].(string), "Objective supplied to new.") {
		t.Fatalf("new objective missing: %v", newView["body"])
	}
}

func TestNextSelectsAndOptionallyClaimsReadyWork(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("TICKET_ACTOR", "")
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Next me", "Do the next work."))
	id := created["id"].(string)

	selected := exactlyOneJSONObject(t, mustCLI(t, "next"))
	item := selected["item"].(map[string]any)
	if item["id"] != id || item["title"] != "Next me" {
		t.Fatalf("next selection: %v", selected)
	}
	if _, ok := item["assignee"]; ok {
		t.Fatalf("read-only next assigned work: %v", item)
	}

	claimed := exactlyOneJSONObject(t, mustCLI(t, "next", "--claim", "--actor", "agent"))
	claimedItem := claimed["item"].(map[string]any)
	if claimedItem["id"] != id || claimedItem["assignee"] != "agent" {
		t.Fatalf("next claim: %v", claimed)
	}
	show := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if show["state"] != "open" || show["assignee"] != "agent" {
		t.Fatalf("claimed ticket: %v", show)
	}
	empty := exactlyOneJSONObject(t, mustCLI(t, "next"))
	if empty["item"] != nil {
		t.Fatalf("expected empty frontier: %v", empty)
	}
	if out, code := runCLI(t, "next", "--claim"); code == 0 || errCode(t, out) != "missing_actor" {
		t.Fatalf("missing actor: exit=%d out=%q", code, out)
	}
}

func TestCreateObjectiveArgumentAndEdit(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	out, code := runCLI(t, "create", "Edit me", "Describe the edit")
	if code != 0 {
		t.Fatalf("create: exit=%d out=%q", code, out)
	}
	id := exactlyOneJSONObject(t, out)["id"].(string)
	show := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	if !strings.Contains(show["body"].(string), "Describe the edit") {
		t.Fatalf("objective missing from created ticket: %v", show["body"])
	}

	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-append")
	t.Setenv("TEST_EDITOR_BODY", "\n## Handoff\nEdited.\n")
	t.Setenv("EDITOR", editor)
	edited, editCode := runCLIHuman(t, "edit", id)
	if editCode != 0 || edited != "edited "+id+"\n" {
		t.Fatalf("edit result: exit=%d output=%q", editCode, edited)
	}
	show = exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	if !strings.Contains(show["body"].(string), "Edited.") {
		t.Fatalf("editor change missing: %v", show["body"])
	}
	created, code := runCLIHuman(t, "create", "-e")
	if code != 0 || !strings.HasPrefix(created, "created ") {
		t.Fatalf("create -e: exit=%d out=%q", code, created)
	}
	newID := strings.Fields(created)[1]
	newBody := exactlyOneJSONObject(t, mustCLI(t, "show", newID, "--full"))["body"].(string)
	if !strings.Contains(newBody, "# New ticket\n\n") || !strings.Contains(newBody, "- State: hold\n") || !strings.Contains(newBody, "## Objective\n\n\n") {
		t.Fatalf("editor template: %q", newBody)
	}
}

func TestNewAndAddWithoutArgumentsOpenEditor(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	contents := "# Filled in title\n\n- State: open\n- Priority: P2\n\n## Objective\n\nFill in the objective.\n"
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-write")
	t.Setenv("TEST_EDITOR_BODY", contents)
	t.Setenv("EDITOR", editor)
	for _, command := range []string{"new", "add"} {
		out, code := runCLIHuman(t, command)
		if code != 0 || !strings.HasPrefix(out, "created ") {
			t.Fatalf("%s without arguments: exit=%d out=%q", command, code, out)
		}
		id := strings.Fields(out)[1]
		show := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
		if !strings.Contains(show["body"].(string), "# Filled in title\n") || !strings.Contains(show["body"].(string), "Fill in the objective.") {
			t.Fatalf("edited %s ticket: %v", command, show["body"])
		}
	}
}

func TestEditorValidationCanRetryWithoutLosingDraft(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	countPath := filepath.Join(dir, "editor-count")
	invalid := "---\nstate: hold\npriority: 2\n---\n# Broken title\n\n# Accidental heading\n\n## Objective\n\nIncomplete.\n"
	valid := "---\nstate: hold\npriority: 2\n---\n# Repaired title\n\n## Objective\n\nReady to continue.\n"
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-retry")
	t.Setenv("TEST_EDITOR_INVALID", invalid)
	t.Setenv("TEST_EDITOR_VALID", valid)
	t.Setenv("EDITOR", editor)
	t.Setenv("EDITOR_COUNT", countPath)
	out, stderr, code := runCLIHumanStdin(t, "y\n", "new", "Retry this edit", "-e")
	if code != 0 || !strings.HasPrefix(out, "created ") {
		t.Fatalf("retrying invalid edit: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	if !strings.Contains(stderr, "Only one H1 heading is allowed") || !strings.Contains(stderr, "Continue editing? [Y/n]") {
		t.Fatalf("validation prompt lacks explanation: %q", stderr)
	}
	fields := strings.Fields(out)
	if len(fields) < 2 {
		t.Fatalf("create output: %q", out)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", fields[1], "--full"))
	if !strings.Contains(view["body"].(string), "# Repaired title") || !strings.Contains(view["body"].(string), "Ready to continue.") {
		t.Fatalf("repaired draft was not saved: %v", view["body"])
	}
}

func TestEditorValidationAbortRestoresOriginal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	invalid := "---\nstate: hold\npriority: 2\n---\n# Broken title\n\n# Accidental heading\n"
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-write")
	t.Setenv("TEST_EDITOR_BODY", invalid)
	t.Setenv("EDITOR", editor)
	out, stderr, code := runCLIHumanStdin(t, "n\n", "new", "Abort this edit", "-e")
	if code == 0 || out != "" || !strings.Contains(stderr, "Only one H1 heading is allowed") {
		t.Fatalf("aborting invalid edit: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	list := exactlyOneJSONObject(t, mustCLI(t, "list", "all"))
	items := list["items"].([]any)
	if len(items) != 0 || !strings.Contains(stderr, "Edited draft preserved at:") {
		t.Fatalf("aborted create list: %v", list)
	}
}

func TestFailedInteractiveCreateClearsCurrentTicket(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	previous := exactlyOneJSONObject(t, mustCLI(t, "create", "Previous current", "Keep the pointer testable."))["id"].(string)
	invalid := "---\nstate: hold\npriority: 2\n---\n# Broken title\n\n# Accidental heading\n"
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-write")
	t.Setenv("TEST_EDITOR_BODY", invalid)
	t.Setenv("EDITOR", editor)
	_, stderr, code := runCLIHumanStdin(t, "n\n", "new", "Abort this edit", "-e")
	if code == 0 || !strings.Contains(stderr, "No ticket was created. Current ticket is unset.") {
		t.Fatalf("aborted create did not report cleared current: exit=%d stderr=%q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "tickets", ".local", "current")); !os.IsNotExist(err) {
		t.Fatalf("current marker survived aborted create: %v", err)
	}
	if _, code := runCLIHuman(t, "status"); code == 0 {
		t.Fatalf("aborted create left a usable current ticket: id=%s exit=%d", previous, code)
	}

	t.Setenv("EDITOR", installTestHelper(t, filepath.Join(dir, "failing-editor"), "editor-fail"))
	t.Setenv("TEST_EDITOR_BODY", "")
	_, stderr, code = runCLIHumanError(t, "new", "Failed editor", "-e")
	if code == 0 || !strings.Contains(stderr, "No ticket was created. Current ticket is unset.") {
		t.Fatalf("failed editor did not report cleared current: exit=%d stderr=%q", code, stderr)
	}
}

func TestObjectiveEditLineStartsAfterBlankSeparator(t *testing.T) {
	ticket, err := domain.ParseTicketFile("20260913-00001", domain.RenderNew("Title", 2, nil, "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := objectiveEditLine(ticket), ticket.Sections["objective"].HeadingLine+2; got != want {
		t.Fatalf("objective editor line=%d want %d", got, want)
	}
}

func TestNewEditorStartsOnObjectiveContentLine(t *testing.T) {
	data := domain.RenderNewForEditor("New ticket", 2, nil, "", nil, nil)
	if !strings.Contains(string(data), "- State: open\n") || !strings.Contains(string(data), "## Objective\n\n\n") {
		t.Fatalf("empty editor template lacks a separate Objective content line:\n%s", data)
	}
	ticket, err := domain.ParseTicketFile("", data)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := objectiveEditLine(ticket), ticket.Sections["objective"].HeadingLine+2; got != want {
		t.Fatalf("new editor line=%d want %d", got, want)
	}
}

func TestHumanCommandsUseCurrentTicketButJSONStaysExplicit(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Current ticket"))["id"].(string)
	if _, code := runCLIHuman(t, "show", id); code != 0 {
		t.Fatalf("select current ticket: exit=%d", code)
	}
	status, code := runCLIHuman(t, "status")
	if code != 0 || !strings.Contains(status, id) {
		t.Fatalf("status without ID: exit=%d out=%q", code, status)
	}
	if out, code := runCLI(t, "show"); code == 0 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("JSON show without ID: exit=%d out=%q", code, out)
	}
	current, err := os.ReadFile(filepath.Join(dir, "tickets", ".local", "current"))
	if err != nil || strings.TrimSpace(string(current)) != id {
		t.Fatalf("current ticket state: %q %v", current, err)
	}
}

func TestWorkflowCommandsThroughCLI(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	out, code := runCLIStdin(t, `{"title":"Workflow","sections":{"objective":"Do it.","acceptance":"It works."}}`, "create", "--input", "-")
	if code != 0 {
		t.Fatalf("create: exit=%d out=%q", code, out)
	}
	id := exactlyOneJSONObject(t, out)["id"].(string)
	if out, code = runCLI(t, "claim", id, "--actor", "alice"); code != 0 {
		t.Fatalf("claim: exit=%d out=%q", code, out)
	}
	if out, code = runCLI(t, "submit", id, "--actor", "alice"); code != 0 {
		t.Fatalf("submit: exit=%d out=%q", code, out)
	}
	review := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if review["state"] != "review" || review["assignee"] != nil {
		t.Fatalf("submitted state: %v", review)
	}
	listed := exactlyOneJSONObject(t, mustCLI(t, "list", "review"))
	if items := listed["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != id {
		t.Fatalf("review list: %v", listed)
	}
	if out, code = runCLI(t, "claim", id, "--actor", "reviewer"); code != 0 {
		t.Fatalf("review claim: exit=%d out=%q", code, out)
	}
	if out, code = runCLI(t, "accept", id, "--actor", "reviewer"); code != 0 {
		t.Fatalf("accept review: exit=%d out=%q", code, out)
	}
	signoff := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if signoff["state"] != "signoff" || signoff["assignee"] != nil {
		t.Fatalf("signoff state: %v", signoff)
	}
	if out, code = runCLI(t, "close", id); code != 0 {
		t.Fatalf("close signoff: exit=%d out=%q", code, out)
	}
	directOut, code := runCLIStdin(t, `{"title":"Direct close","sections":{"objective":"Do it.","acceptance":"It works."}}`, "create", "--input", "-")
	if code != 0 {
		t.Fatalf("direct create: exit=%d out=%q", code, directOut)
	}
	directID := exactlyOneJSONObject(t, directOut)["id"].(string)
	if out, code = runCLI(t, "submit", directID); code != 0 {
		t.Fatalf("direct submit: exit=%d out=%q", code, out)
	}
	if out, code = runCLI(t, "close", directID); code != 0 {
		t.Fatalf("direct review close: exit=%d out=%q", code, out)
	}
	if out, code = runCLI(t, "open", id, "--handoff", "Reopen for follow-up"); code != 0 {
		t.Fatalf("open completed ticket: exit=%d out=%q", code, out)
	}
	opened := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if opened["state"] != "open" || opened["assignee"] != nil {
		t.Fatalf("opened ticket: %v", opened)
	}
	if out, code = runCLI(t, "help", "open"); code != 0 || !strings.Contains(out, "Open a ticket") {
		t.Fatalf("open help: exit=%d out=%q", code, out)
	}
	if out, code = runCLI(t, "help", "submit"); code != 0 || !strings.Contains(out, "Submit an open ticket") {
		t.Fatalf("submit help: exit=%d out=%q", code, out)
	}
	if out, code = runCLI(t, "help", "accept"); code != 0 || !strings.Contains(out, "Approve review tickets") {
		t.Fatalf("accept help: exit=%d out=%q", code, out)
	}
	for _, obsolete := range []string{"resume", "return", "reopen", "complete"} {
		if out, code := runCLI(t, obsolete, id); code == 0 || errCode(t, out) != "invalid_argument" {
			t.Fatalf("obsolete command %q accepted: exit=%d out=%q", obsolete, code, out)
		}
	}
	batchIDs := []string{}
	for _, title := range []string{"Batch one", "Batch two"} {
		created, code := runCLIStdin(t, `{"title":"`+title+`","sections":{"objective":"Do it.","acceptance":"It works."}}`, "create", "--input", "-")
		if code != 0 {
			t.Fatalf("batch create: exit=%d out=%q", code, created)
		}
		batchIDs = append(batchIDs, exactlyOneJSONObject(t, created)["id"].(string))
	}
	for _, batchID := range batchIDs {
		if out, code := runCLI(t, "submit", batchID); code != 0 {
			t.Fatalf("batch submit: exit=%d out=%q", code, out)
		}
	}
	approved := exactlyOneJSONObject(t, mustCLI(t, "approve", "all"))
	if len(approved["items"].([]any)) != 2 {
		t.Fatalf("approve all: %v", approved)
	}
	for _, batchID := range batchIDs {
		if out, code := runCLI(t, "close", batchID); code != 0 {
			t.Fatalf("close %s: exit=%d out=%q", batchID, code, out)
		}
	}
}

func TestCloseAcceptsMultipleTargets(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	create := func(title string) string {
		out, code := runCLIStdin(t, `{"title":"`+title+`","sections":{"objective":"Do the work."}}`, "create", "--input", "-")
		if code != 0 {
			t.Fatalf("create: exit=%d out=%q", code, out)
		}
		return exactlyOneJSONObject(t, out)["id"].(string)
	}
	first, second := create("first"), create("second")
	batch := exactlyOneJSONObject(t, mustCLI(t, "close", second+", "+first))
	items, ok := batch["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("comma close result: %v", batch)
	}
	for _, item := range items {
		row := item.(map[string]any)
		if row["state"] != "completed" || row["changed"] != true {
			t.Fatalf("comma close item: %v", row)
		}
	}
	third := create("third")
	all := exactlyOneJSONObject(t, mustCLI(t, "close", "*"))
	allItems, ok := all["items"].([]any)
	if !ok || len(allItems) != 1 || allItems[0].(map[string]any)["id"] != third {
		t.Fatalf("wildcard close result: %v", all)
	}
	fourth := create("fourth")
	all = exactlyOneJSONObject(t, mustCLI(t, "close", "-a"))
	allItems, ok = all["items"].([]any)
	if !ok || len(allItems) != 1 || allItems[0].(map[string]any)["id"] != fourth {
		t.Fatalf("-a close result: %v", all)
	}
}

func TestRejectAcceptsPositionalOutcome(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Reject positional", "Decide whether to keep this ticket."))
	id := created["id"].(string)

	rejected := exactlyOneJSONObject(t, mustCLI(t, "reject", id, "duplicate"))
	if rejected["id"] != id || rejected["state"] != "rejected" || rejected["changed"] != true {
		t.Fatalf("positional rejection result: %v", rejected)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	sections := view["sections"].(map[string]any)
	outcome := sections["outcome"].(map[string]any)
	if view["state"] != "rejected" || outcome["text"] != "duplicate" {
		t.Fatalf("positional rejection outcome: %v", view)
	}

	second := exactlyOneJSONObject(t, mustCLI(t, "create", "Reject conflict", "Keep the outcome source unambiguous."))["id"].(string)
	path := filepath.Join(dir, "tickets", second, "TASK.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if out, code := runCLI(t, "reject", second, "one", "--outcome", "two"); code == 0 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("positional and flag outcome: exit=%d out=%q", code, out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected mixed outcome sources changed the ticket")
	}
}

func TestHumanListConveniencesAndStatus(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	today := time.Now().UTC().Format("2006-01-02")
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	create := func(title string, priority string, sections string) string {
		args := []string{"create", "--title", title, "--priority", priority}
		out, code := runCLIStdin(t, sections, args...)
		if code != 0 {
			t.Fatalf("create %q: exit=%d out=%q", title, code, out)
		}
		return exactlyOneJSONObject(t, out)["id"].(string)
	}
	readyID := create("A very long title that should be truncated in the human table because it exceeds the fixed title column width", "1", "## Objective\n\nDo the work.\n\n## Acceptance\n\n- [ ] It works.\n")
	claimed := exactlyOneJSONObject(t, mustCLI(t, "claim", readyID, "--actor", "worker-a"))
	if claimed["changed"] != true {
		t.Fatalf("claim: %v", claimed)
	}
	completedID := create("Completed task", "2", "## Objective\n\nDone.\n\n## Acceptance\n\n- [ ] Done.\n")
	if out, code := runCLI(t, "submit", completedID); code != 0 {
		t.Fatalf("submit completed fixture: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "approve", completedID); code != 0 {
		t.Fatalf("approve completed fixture: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "close", completedID, "--outcome", "Shipped", "--actor", "worker-a"); code != 0 {
		t.Fatalf("close: exit=%d out=%q", code, out)
	}
	openID := create("Open task", "3", "## Objective\n\nOpen.\n\n## Acceptance\n\n- [ ] Open.\n")

	for _, args := range [][]string{{"list", "all"}, {"ls", "all"}, {"list", "-s", "open"}, {"ls", "-s", "open"}} {
		out, code := runCLIHuman(t, args...)
		if code != 0 {
			t.Fatalf("list %v: exit=%d out=%q", args, code, out)
		}
		if !strings.Contains(out, "STATE") || !strings.Contains(out, readyID) || !strings.Contains(out, openID) {
			t.Fatalf("list %v missing expected rows: %q", args, out)
		}
		if args[len(args)-1] == "open" && strings.Contains(out, completedID) {
			t.Fatalf("list %v included completed ticket: %q", args, out)
		}
	}
	listOut, code := runCLIHuman(t, "list", "all")
	if code != 0 || !strings.Contains(listOut, "completed") || !strings.Contains(listOut, "@worker-a") ||
		!strings.Contains(listOut, "…") ||
		!strings.HasPrefix(listOut, fmt.Sprintf("%-10s %-4s %-14s %-42s %s\n", "STATE", "PRI", "ID", "TITLE", "ASSIGNEE")) {
		t.Fatalf("human list table: exit=%d out=%q", code, listOut)
	}

	statusOut, code := runCLIHuman(t, "status", readyID)
	if code != 0 || !strings.Contains(statusOut, readyID) || !strings.Contains(statusOut, "state:      open") ||
		!strings.Contains(statusOut, "priority:   P1") || !strings.Contains(statusOut, "assignee:   worker-a") ||
		!strings.Contains(statusOut, "created:    "+today) || !strings.Contains(statusOut, "modified:   ") ||
		strings.Contains(statusOut, "readiness:") || strings.Contains(statusOut, "blocked by:") || strings.Contains(statusOut, "assigned") {
		t.Fatalf("human status: exit=%d out=%q", code, statusOut)
	}
	openStatus, code := runCLIHuman(t, "status", openID)
	if code != 0 || strings.Contains(openStatus, "readiness:") || strings.Contains(openStatus, "blocked by:") {
		t.Fatalf("unassigned status: exit=%d out=%q", code, openStatus)
	}
	statusJSON, code := runCLI(t, "status", readyID)
	if code != 0 {
		t.Fatalf("json status: exit=%d out=%q", code, statusJSON)
	}
	status := exactlyOneJSONObject(t, statusJSON)
	if status["id"] != readyID || status["title"] == nil || status["created"] != today {
		t.Fatalf("json status: %v", status)
	}
	if _, ok := status["readiness"]; ok {
		t.Fatalf("status unexpectedly includes readiness: %v", status)
	}
	modified, ok := status["modified"].(string)
	if !ok {
		t.Fatalf("status missing modified timestamp: %v", status)
	}
	if _, err := time.Parse(time.RFC3339, modified); err != nil {
		t.Fatalf("modified timestamp=%q: %v", modified, err)
	}
	if _, ok := status["body"]; ok {
		t.Fatalf("status unexpectedly includes body: %v", status)
	}

	if out, code := runCLI(t, "list", "unknown"); code == 0 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("unknown positional state accepted: exit=%d out=%q", code, out)
	}
}

func TestTicketActorEnvironmentFallbackAndFlagOverride(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	t.Setenv("TICKET_ACTOR", "environment-worker")
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Environment actor", "Use the configured actor."))
	id := created["id"].(string)
	claimed := exactlyOneJSONObject(t, mustCLI(t, "claim", id))
	if claimed["changed"] != true || claimed["assignee"] != "environment-worker" {
		t.Fatalf("claim from TICKET_ACTOR: %v", claimed)
	}
	out, code := runCLIStdin(t, `{"set":{"title":"Updated by environment actor"}}`, "update", id, "--input", "-")
	if code != 0 {
		t.Fatalf("update from TICKET_ACTOR: exit=%d out=%q", code, out)
	}
	updated := exactlyOneJSONObject(t, out)
	if updated["changed"] != true {
		t.Fatalf("update from TICKET_ACTOR: %v", updated)
	}
	released := exactlyOneJSONObject(t, mustCLI(t, "release", id))
	if released["changed"] != true {
		t.Fatalf("release from TICKET_ACTOR: %v", released)
	}
	workflow := exactlyOneJSONObject(t, mustCLI(t, "create", "Workflow actor", "Use the configured actor for workflow moves."))
	workflowID := workflow["id"].(string)
	exactlyOneJSONObject(t, mustCLI(t, "claim", workflowID))
	submitted := exactlyOneJSONObject(t, mustCLI(t, "submit", workflowID))
	if submitted["state"] != "review" {
		t.Fatalf("submit from TICKET_ACTOR: %v", submitted)
	}

	flagged := exactlyOneJSONObject(t, mustCLI(t, "create", "Explicit actor", "Use the flag actor."))
	flaggedID := flagged["id"].(string)
	claimed = exactlyOneJSONObject(t, mustCLI(t, "claim", flaggedID, "--actor", "flag-worker"))
	if claimed["changed"] != true || claimed["assignee"] != "flag-worker" {
		t.Fatalf("explicit actor override: %v", claimed)
	}
}

func TestListAliases(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLIHuman(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	create := func(title string) string {
		out, code := runCLIHuman(t, "create", title, "Work on "+title)
		if code != 0 {
			t.Fatalf("create %q: exit=%d out=%q", title, code, out)
		}
		return strings.Fields(out)[1]
	}
	first := create("first")
	second := create("second")
	third := create("third")
	for i, id := range []string{first, second, third} {
		stamp := time.Unix(int64(i+1), 0)
		if err := os.Chtimes(filepath.Join(dir, "tickets", id, "TASK.md"), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}

	all, code := runCLIHuman(t, "ls", "-a")
	if code != 0 || !strings.Contains(all, first) || !strings.Contains(all, third) {
		t.Fatalf("ls -a: exit=%d out=%q", code, all)
	}
	for _, args := range [][]string{{"ls", "-la"}, {"list", "-la"}, {"-la"}} {
		out, code := runCLIHuman(t, args...)
		if code != 0 || !strings.Contains(out, first) || !strings.Contains(out, third) {
			t.Fatalf("%v: exit=%d out=%q", args, code, out)
		}
	}
	defaultSorted, code := runCLIHuman(t, "list", "-a")
	if code != 0 {
		t.Fatalf("default human list: exit=%d out=%q", code, defaultSorted)
	}
	assertListOrder(t, defaultSorted, third, second, first)
	if long, code := runCLIHuman(t, "ls", "-l"); code != 0 || !strings.Contains(long, "STATE") {
		t.Fatalf("ls -l: exit=%d out=%q", code, long)
	}
	idSorted, code := runCLIHuman(t, "ls", "-lt")
	if code != 0 {
		t.Fatalf("ls -lt: exit=%d out=%q", code, idSorted)
	}
	assertListOrder(t, idSorted, third, second, first)
	modifiedSorted, code := runCLIHuman(t, "ls", "-ltu")
	if code != 0 {
		t.Fatalf("ls -ltu: exit=%d out=%q", code, modifiedSorted)
	}
	assertListOrder(t, modifiedSorted, third, second, first)
	allModified, code := runCLIHuman(t, "ls", "-ltua")
	if code != 0 {
		t.Fatalf("ls -ltua: exit=%d out=%q", code, allModified)
	}
	assertListOrder(t, allModified, third, second, first)

	jsonOut, code := runCLI(t, "ls", "-a")
	if code != 0 {
		t.Fatalf("json ls -a: exit=%d out=%q", code, jsonOut)
	}
	result := exactlyOneJSONObject(t, jsonOut)
	if _, ok := result["sort"]; ok {
		t.Fatalf("formatting sort leaked into JSON: %v", result)
	}
	allItems := result["items"].([]any)
	window := exactlyOneJSONObject(t, mustCLI(t, "list", "all", "--limit", "1", "--offset", "1"))
	windowItems := window["items"].([]any)
	if len(allItems) != 3 || len(windowItems) != 1 ||
		windowItems[0].(map[string]any)["id"] != allItems[1].(map[string]any)["id"] || window["more"] != true {
		t.Fatalf("offset window: all=%v window=%v", allItems, window)
	}
}

func TestListAcceptsMultipleStatesPositionallyAndByFlag(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	held := exactlyOneJSONObject(t, mustCLI(t, "create", "Held ticket"))["id"].(string)
	open := exactlyOneJSONObject(t, mustCLI(t, "create", "Open ticket", "Do the work."))["id"].(string)

	assertStates := func(args ...string) {
		t.Helper()
		result := exactlyOneJSONObject(t, mustCLI(t, args...))
		items := result["items"].([]any)
		if len(items) != 2 || result["more"] != false {
			t.Fatalf("state list %v: %v", args, result)
		}
		seen := map[string]string{}
		for _, item := range items {
			row := item.(map[string]any)
			seen[row["id"].(string)] = row["state"].(string)
		}
		if seen[held] != "hold" || seen[open] != "open" {
			t.Fatalf("state list %v: %v", args, result)
		}
	}
	assertStates("list", "hold", "open", "--limit", "10")
	assertStates("list", "--state", "hold", "open", "--limit", "10")
	assertStates("list", "--state", "hold", "--state", "open", "--limit", "10")
}

func TestBareHumanListShowsAllNonterminalTickets(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	var ids []string
	for i := 0; i < 23; i++ {
		out, code := runCLIHuman(t, "create", fmt.Sprintf("Current work %02d", i))
		if code != 0 {
			t.Fatalf("create %d: exit=%d out=%q", i, code, out)
		}
		ids = append(ids, strings.Fields(out)[1])
	}
	if _, code := runCLI(t, "close", ids[0]); code != 0 {
		t.Fatalf("close terminal fixture: exit=%d", code)
	}
	if _, code := runCLI(t, "reject", ids[1], "duplicate"); code != 0 {
		t.Fatalf("reject terminal fixture: exit=%d", code)
	}

	out, code := runCLIHuman(t, "list")
	if code != 0 {
		t.Fatalf("bare human list: exit=%d out=%q", code, out)
	}
	for _, id := range ids[2:] {
		if !strings.Contains(out, id) {
			t.Fatalf("bare human list omitted nonterminal %s: %q", id, out)
		}
	}
	if strings.Contains(out, ids[0]) || strings.Contains(out, ids[1]) {
		t.Fatalf("bare human list included terminal tickets: %q", out)
	}

	allOutput, code := runCLIHuman(t, "list", "all")
	if code != 0 {
		t.Fatalf("human all list: exit=%d out=%q", code, allOutput)
	}
	for _, id := range ids {
		if !strings.Contains(allOutput, id) {
			t.Fatalf("human all list omitted %s: %q", id, allOutput)
		}
	}
}

func TestHumanShowDoesNotAutoDetectMarkdownRenderer(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLIHuman(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	created, code := runCLIHuman(t, "create", "Renderer test")
	if code != 0 {
		t.Fatalf("create: exit=%d out=%q", code, created)
	}
	id := strings.Fields(created)[1]
	renderer := filepath.Join(dir, "glow")
	if err := os.WriteFile(renderer, []byte("not invoked"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, code := runCLIHuman(t, "show", id, "--full")
	if code != 0 || !strings.Contains(out, "# Renderer test") || strings.Contains(out, "rendered markdown") {
		t.Fatalf("plain show with PATH renderer: exit=%d out=%q", code, out)
	}
	jsonOut, code := runCLI(t, "show", id, "--full")
	if code != 0 || !strings.Contains(jsonOut, "# Renderer test") {
		t.Fatalf("JSON show changed by renderer: exit=%d out=%q", code, jsonOut)
	}
}

func assertListOrder(t *testing.T, output string, want ...string) {
	t.Helper()
	var got []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[2] != "ID" && strings.HasPrefix(fields[1], "P") {
			got = append(got, fields[2])
		}
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("list order=%v want=%v output=%q", got, want, output)
	}
}

func TestCreatePipedBodyAndAliases(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	body := "## Objective\n\nRead from stdin.\n\n## Acceptance\n\n- [ ] Preserve the body.\n"
	out, code := runCLIStdin(t, body, "create", "Piped ticket")
	if code != 0 {
		t.Fatalf("piped create: exit=%d out=%q", code, out)
	}
	id := exactlyOneJSONObject(t, out)["id"].(string)
	show := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	createdBody := show["body"].(string)
	if !strings.Contains(createdBody, "# Piped ticket\n") || !strings.Contains(createdBody, "Read from stdin.") ||
		!strings.Contains(createdBody, "- [ ] Preserve the body.") {
		t.Fatalf("piped body was not preserved: %q", createdBody)
	}
	if strings.Count(createdBody, "# Piped ticket") != 1 {
		t.Fatalf("managed title was duplicated: %q", createdBody)
	}

	// Empty piped input keeps the existing minimal-ticket behavior.
	out, code = runCLIStdin(t, "", "create", "Empty body")
	if code != 0 {
		t.Fatalf("empty piped create: exit=%d out=%q", code, out)
	}

	// The aliases use the same implementation and body handling.
	for _, command := range []string{"new", "add"} {
		out, code = runCLIStdin(t, "## Objective\n\nAlias body.\n", command, "Alias ticket")
		if code != 0 {
			t.Fatalf("%s: exit=%d out=%q", command, code, out)
		}
		aliasID := exactlyOneJSONObject(t, out)["id"].(string)
		if !strings.Contains(exactlyOneJSONObject(t, mustCLI(t, "show", aliasID, "--full"))["body"].(string), "Alias body.") {
			t.Fatalf("%s body missing", command)
		}
	}
	if out, code = runCLI(t, "ls"); code != 0 || !strings.Contains(out, "\"items\"") {
		t.Fatalf("ls: exit=%d out=%q", code, out)
	}

	for _, body := range []string{
		"# nested H1\n",
	} {
		out, code = runCLIStdin(t, body, "create", "Invalid body")
		if code == 0 || errCode(t, out) != "invalid_argument" {
			t.Fatalf("invalid piped body accepted: exit=%d out=%q", code, out)
		}
	}
	// Duplicate semantic sections are retained as a warning and do not make
	// an otherwise valid ticket unreadable.
	out, code = runCLIStdin(t, "## Objective\none\n\n## Objective\ntwo\n", "create", "Duplicate sections")
	if code != 0 {
		t.Fatalf("duplicate section should be nonfatal: exit=%d out=%q", code, out)
	}

	// Heading-like content inside a supported fence remains ordinary content.
	out, code = runCLIStdin(t, "```\n# not a title\n## not a section\n```\n\n## Objective\nSafe.\n", "create", "Fenced body")
	if code != 0 {
		t.Fatalf("fenced body: exit=%d out=%q", code, out)
	}

	// Structured JSON input and positional/stdin-body creation are ambiguous.
	out, code = runCLIStdin(t, `{"title":"json"}`, "create", "positional", "--input", "-")
	if code == 0 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("ambiguous create accepted: exit=%d out=%q", code, out)
	}
}

// End-to-end: init, create, list, show, path in one repository.
func TestEndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out, code := runCLI(t, "init")
	if code != 0 {
		t.Fatalf("init: %q (exit %d)", out, code)
	}
	m := exactlyOneJSONObject(t, out)
	if m["created"] != true {
		t.Fatalf("init: %v", m)
	}
	out, code = runCLIStdin(t, `{"title":"First ticket","priority":1,"tags":["demo"],"sections":{"objective":"Implement the ticket."}}`, "create", "--input", "-")
	if code != 0 {
		t.Fatalf("create: %q (exit %d)", out, code)
	}
	m = exactlyOneJSONObject(t, out)
	id, _ := m["id"].(string)
	if id == "" || !strings.HasPrefix(id, "20") {
		t.Fatalf("create: %v", m)
	}
	if m["changed"] != true {
		t.Fatalf("create changed: %v", m)
	}
	// list shows the new ticket.
	out, code = runCLI(t, "list")
	if code != 0 {
		t.Fatalf("list: %q (exit %d)", out, code)
	}
	m = exactlyOneJSONObject(t, out)
	items, _ := m["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list items: %v", m)
	}
	// show by shorthand (first 6 chars).
	out, code = runCLI(t, "show", id[:6])
	if code != 0 {
		t.Fatalf("show: %q (exit %d)", out, code)
	}
	m = exactlyOneJSONObject(t, out)
	if m["id"] != id {
		t.Fatalf("show: %v", m)
	}
	if _, ok := m["attachment_path"]; ok {
		t.Fatalf("show advertised absent attachments: %v", m)
	}
	attachments := filepath.Join(dir, "tickets", id, "attachments")
	if err := os.Mkdir(attachments, 0o755); err != nil {
		t.Fatal(err)
	}
	out, code = runCLI(t, "show", id)
	if code != 0 {
		t.Fatalf("show with attachments: %q (exit %d)", out, code)
	}
	m = exactlyOneJSONObject(t, out)
	wantAttachmentPath := filepath.ToSlash(filepath.Join("tickets", id, "attachments"))
	if m["attachment_path"] != wantAttachmentPath {
		t.Fatalf("attachment path=%v want %q", m["attachment_path"], wantAttachmentPath)
	}
	// A symlinked attachments entry is exposed lexically; the harness owns
	// the policy for following its target.
	linkedTarget := filepath.Join(dir, "tickets", id, "real-attachments")
	if err := os.Mkdir(linkedTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(attachments); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(linkedTarget, attachments); err == nil {
		out, code = runCLI(t, "show", id)
		if code != 0 {
			t.Fatalf("show with linked attachments: %q (exit %d)", out, code)
		}
		m = exactlyOneJSONObject(t, out)
		wantLinkedPath := filepath.ToSlash(filepath.Join("tickets", id, "attachments"))
		if m["attachment_path"] != wantLinkedPath {
			t.Fatalf("linked attachment path=%v want %q", m["attachment_path"], wantLinkedPath)
		}
		outsideTarget := filepath.Join(t.TempDir(), "outside-attachments")
		if err := os.Mkdir(outsideTarget, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(attachments); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsideTarget, attachments); err != nil {
			t.Fatalf("outside symlink setup: %v", err)
		}
		out, code = runCLI(t, "show", id)
		if code != 0 {
			t.Fatalf("show with external linked attachments: %q (exit %d)", out, code)
		}
		m = exactlyOneJSONObject(t, out)
		if m["attachment_path"] != wantLinkedPath {
			t.Fatalf("external linked attachment path=%v want %q", m["attachment_path"], wantLinkedPath)
		}
	} else if err := os.Mkdir(attachments, 0o755); err != nil {
		t.Logf("symlink test skipped: %v", err)
	}
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)
	out, code = runCLI(t, "show", id)
	if code != 0 {
		t.Fatalf("nested show with attachments: %q (exit %d)", out, code)
	}
	m = exactlyOneJSONObject(t, out)
	if _, ok := m["attachment_path"]; ok {
		t.Fatalf("nested invocation exposed attachment path outside cwd: %v", m)
	}
	t.Chdir(dir)
	// An explicitly configured ticket root outside the working directory is
	// not exposed as an attachment path that the harness cannot safely reach.
	externalRoot := filepath.Join(t.TempDir(), "tickets")
	t.Setenv("TICKET_ROOT", externalRoot)
	if out, code = runCLI(t, "init"); code != 0 {
		t.Fatalf("external init: exit=%d out=%q", code, out)
	}
	external := exactlyOneJSONObject(t, mustCLI(t, "create", "External root"))
	externalID := external["id"].(string)
	if err := os.Mkdir(filepath.Join(externalRoot, externalID, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, code = runCLI(t, "show", externalID)
	if code != 0 {
		t.Fatalf("external show: exit=%d out=%q", code, out)
	}
	m = exactlyOneJSONObject(t, out)
	if _, ok := m["attachment_path"]; ok {
		t.Fatalf("external root exposed attachment path: %v", m)
	}
	t.Setenv("TICKET_ROOT", "")
	// path.
	out, code = runCLI(t, "path", id)
	if code != 0 {
		t.Fatalf("path: %q (exit %d)", out, code)
	}
	m = exactlyOneJSONObject(t, out)
	if m["path"] != id+"/TASK.md" {
		t.Fatalf("path: %v", m)
	}
	// Human output is the default.
	out, code = runCLIHuman(t, "list")
	if code != 0 {
		t.Fatalf("list: %q (exit %d)", out, code)
	}
	if strings.Contains(out, `"`) {
		t.Fatalf("human output contains JSON: %q", out)
	}
	stdout, stderr, code := runCLIHumanError(t, "create", "first", "--title", "second")
	if code == 0 || stdout != "" || !strings.Contains(stderr, "either a positional title or --title") {
		t.Fatalf("title conflict: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	// repo_not_found outside a repository.
	empty := t.TempDir()
	t.Chdir(empty)
	out, code = runCLI(t, "list")
	if code == 0 {
		t.Fatalf("expected repo_not_found: %q", out)
	}
	m = exactlyOneJSONObject(t, out)
	if m["error"].(map[string]any)["code"] != "repo_not_found" {
		t.Fatalf("code=%v", m["error"])
	}
}

// C02: --input strict JSON: duplicate keys, unknown keys, trailing
// values, bad JSON all map to stable error codes.
func TestCreateInputStrict(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: %q", out)
	}
	cases := []struct {
		name string
		body string
		code string
	}{
		{"dup key", `{"title":"a","title":"b"}`, "invalid_json"},
		{"unknown key", `{"title":"a","nonsense":1}`, "invalid_argument"},
		{"trailing value", `{"title":"a"} garbage`, "invalid_json"},
		{"bad json", `{`, "invalid_json"},
		{"not object", `[]`, "invalid_json"},
		{"bad section key", `{"title":"a","sections":{"nope":"x"}}`, "invalid_argument"},
	}
	for _, c := range cases {
		out, code := runCLIStdin(t, c.body, "create", "--input", "-")
		if code == 0 {
			t.Fatalf("%s: expected failure: %q", c.name, out)
		}
		m := exactlyOneJSONObject(t, out)
		got, _ := m["error"].(map[string]any)["code"].(string)
		if got != c.code {
			t.Fatalf("%s: code=%s want %s (%q)", c.name, got, c.code, out)
		}
	}
	if out, code := runCLI(t, "create", "--input", filepath.Join(dir, "input.json")); code == 0 {
		t.Fatalf("file input was accepted: %q", out)
	}
	// Valid input works.
	out, code := runCLIStdin(t, `{"title":"from input","sections":{"objective":"from stdin"}}`, "create", "--input", "-")
	if code != 0 {
		t.Fatalf("valid input: %q (exit %d)", out, code)
	}
}
