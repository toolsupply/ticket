package cli

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionCurrentIsIsolatedAndAuthoritative(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Session current", "Read the current ticket."))
	id := created["id"].(string)
	if _, code := runCLIHuman(t, "show", id); code != 0 {
		t.Fatalf("select current ticket: exit=%d", code)
	}
	marker := filepath.Join(dir, "tickets", ".local", "current")
	if err := os.Remove(marker); err != nil {
		t.Fatalf("remove one-shot marker: %v", err)
	}

	first := newSessionExecutor()
	var output bytes.Buffer
	if err := first.dispatch([]string{"show", id}, &output); err != nil {
		t.Fatalf("session show: %v", err)
	}
	if first.currentTicket() != id || !strings.Contains(output.String(), id) {
		t.Fatalf("session selection: current=%q output=%q", first.currentTicket(), output.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("session wrote persistent current marker: %v", err)
	}

	output.Reset()
	if err := first.dispatch([]string{"status"}, &output); err != nil {
		t.Fatalf("session status: %v", err)
	}
	if !strings.Contains(output.String(), id) {
		t.Fatalf("session status did not use in-memory current: %q", output.String())
	}

	second := newSessionExecutor()
	if err := second.dispatch([]string{"status"}, &output); err == nil || !strings.Contains(err.Error(), "no current ticket") {
		t.Fatalf("isolated session status error: %v", err)
	}

	if err := first.dispatch([]string{"-j", "status"}, &output); err == nil || !strings.Contains(err.Error(), "requires an ID in JSON mode") {
		t.Fatalf("session JSON status error: %v", err)
	}
}

func TestSessionCurrentReadsTicketAgainForEachCommand(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Session reread", "Read authoritative state."))
	id := created["id"].(string)
	if _, code := runCLIHuman(t, "show", id); code != 0 {
		t.Fatalf("select current ticket: exit=%d", code)
	}
	if err := os.Remove(filepath.Join(dir, "tickets", ".local", "current")); err != nil {
		t.Fatal(err)
	}

	instance := newSessionExecutor()
	var output bytes.Buffer
	if err := instance.dispatch([]string{"show", id}, &output); err != nil {
		t.Fatalf("session show: %v", err)
	}
	output.Reset()
	if out, code := runCLI(t, "state", id, "rejected"); code != 0 {
		t.Fatalf("external state change: exit=%d out=%q", code, out)
	}
	if err := instance.dispatch([]string{"status"}, &output); err != nil {
		t.Fatalf("session reread: %v", err)
	}
	if !strings.Contains(output.String(), "rejected") {
		t.Fatalf("session used stale ticket data: %q", output.String())
	}
}

func TestSessionBindsRepositoryAndRejectsSwitchingFlags(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	a := filepath.Join(dir, "repo-a")
	b := filepath.Join(dir, "repo-b")
	c := filepath.Join(dir, "repo-c")
	t.Setenv("TICKET_SCOPE", "")
	t.Setenv("TICKET_REPOSITORY", a)
	mustCLI(t, "init")
	aTicket := exactlyOneJSONObject(t, mustCLI(t, "create", "Repository A", "Stay in repository A."))["id"].(string)

	instance := newSessionExecutor()
	var output bytes.Buffer
	if err := instance.dispatch([]string{"show", aTicket}, &output); err != nil {
		t.Fatalf("initial session show: %v", err)
	}

	t.Setenv("TICKET_REPOSITORY", b)
	mustCLI(t, "init")
	exactlyOneJSONObject(t, mustCLI(t, "create", "Repository B", "Use a different repository."))
	output.Reset()
	if err := instance.dispatch([]string{"show", aTicket}, &output); err != nil {
		t.Fatalf("session crossed repository boundary: %v", err)
	}
	if !strings.Contains(output.String(), "Repository A") {
		t.Fatalf("session used changed repository: %q", output.String())
	}

	t.Setenv("TICKET_REPOSITORY", c)
	output.Reset()
	if err := instance.dispatch([]string{"init"}, &output); err != nil {
		t.Fatalf("session init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(c, "config.json")); !os.IsNotExist(err) {
		t.Fatalf("session init escaped bound repository: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a, "config.json")); err != nil {
		t.Fatalf("bound repository was not used by session init: %v", err)
	}

	output.Reset()
	if err := instance.dispatch([]string{"create", "--title", "--scope"}, &output); err != nil {
		t.Fatalf("flag-like title was rejected: %v", err)
	}
	if err := instance.dispatch([]string{"create", "--title", "--", "--scope", "other"}, &output); err == nil {
		t.Fatal("actual scope flag was accepted after a flag value")
	}

	for _, args := range [][]string{
		{"show", aTicket, "--scope", "other"},
		{"show", aTicket, "--config", "other.json"},
		{"show", aTicket, "-i"},
	} {
		if err := instance.dispatch(args, &output); err == nil {
			t.Fatalf("session accepted boundary-changing args %v", args)
		}
	}
}

func TestSessionDoesNotConsumeStdinPayloads(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Session input", "Protect session input."))["id"].(string)
	instance := newSessionExecutor()
	var output bytes.Buffer

	oldStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString("following command\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdin = reader
	defer func() {
		os.Stdin = oldStdin
		_ = reader.Close()
	}()

	if err := instance.dispatch([]string{"create", "No implicit body"}, &output); err != nil {
		t.Fatalf("session create: %v", err)
	}
	remaining, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(remaining) != "following command\n" {
		t.Fatalf("session create consumed following input: %q", remaining)
	}

	for _, args := range [][]string{
		{"create", "--input", "-"},
		{"update", id, "--input", "-"},
		{"append", id, "-"},
		{"release", id, "--input", "-", "--actor", "agent"},
		{"reassign", id, "agent", "--input", "-"},
		{"reject", id, "--input", "-", "--actor", "agent"},
	} {
		if err := instance.dispatch(args, &output); err == nil || !strings.Contains(err.Error(), "cannot consume input from the session stream") {
			t.Fatalf("session accepted stdin payload %v: %v", args, err)
		}
	}
	if err := instance.dispatchWithInput([]string{"-j", "update", id, "--input", "-"}, &output, strings.NewReader("")); err == nil || !strings.Contains(err.Error(), "Invalid --input JSON") {
		t.Fatalf("explicit empty session input was not read: %v", err)
	}
}

func TestSessionExplicitInputSupportsAllStdinCommandForms(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	t.Setenv("TICKET_ACTOR", "agent")

	instance := newSessionExecutor()
	dispatchJSON := func(args []string, input io.Reader) map[string]any {
		var output bytes.Buffer
		args = append([]string{"-j"}, args...)
		if err := instance.dispatchWithInput(args, &output, input); err != nil {
			t.Fatalf("session %v: %v", args, err)
		}
		return exactlyOneJSONObject(t, output.String())
	}
	dispatchJSONNoInput := func(args ...string) map[string]any {
		return dispatchJSON(args, nil)
	}

	created := dispatchJSON([]string{"create", "--input", "-"}, strings.NewReader(`{"title":"Session structured","sections":{"objective":"Read this body."}}`))
	first := created["id"].(string)
	trailing := dispatchJSON([]string{"create", "Session trailing", "-"}, strings.NewReader("Read this objective.\n"))
	second := trailing["id"].(string)
	third := dispatchJSONNoInput("create", "Session close")
	thirdID := third["id"].(string)

	updated := dispatchJSON([]string{"update", first, "--input", "-"}, strings.NewReader(`{"set":{"title":"Session updated"}}`))
	if updated["id"] != first || updated["changed"] != true {
		t.Fatalf("session update result: %v", updated)
	}
	if claimed := dispatchJSONNoInput("claim", first); claimed["assignee"] != "agent" {
		t.Fatalf("session claim result: %v", claimed)
	}
	released := dispatchJSON([]string{"release", first, "--input", "-"}, strings.NewReader(`{"handoff":"Released from session."}`))
	if released["id"] != first || released["assignee"] != nil {
		t.Fatalf("session release result: %v", released)
	}
	rejected := dispatchJSON([]string{"reject", second, "--input", "-"}, strings.NewReader(`{"outcome":"duplicate"}`))
	if rejected["id"] != second || rejected["state"] != "rejected" {
		t.Fatalf("session reject result: %v", rejected)
	}
	closed := dispatchJSON([]string{"close", thirdID, "--input", "-"}, strings.NewReader(`{"outcome":"completed by session"}`))
	if closed["id"] != thirdID || closed["state"] != "closed" {
		t.Fatalf("session close result: %v", closed)
	}
}

func TestSessionExplicitInputDoesNotFallbackToProcessStdin(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	instance := newSessionExecutor()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString("process stdin must remain untouched\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = reader
	defer func() {
		os.Stdin = oldStdin
		_ = reader.Close()
	}()

	var output bytes.Buffer
	input := strings.NewReader(`{"title":"Injected input","sections":{"objective":"Use only the invocation reader."}}`)
	if err := instance.dispatchWithInput([]string{"-j", "create", "--input", "-"}, &output, input); err != nil {
		t.Fatalf("session explicit input: %v", err)
	}
	if got := exactlyOneJSONObject(t, output.String())["id"]; got == nil || got == "" {
		t.Fatalf("session explicit input result: %v", got)
	}
	remaining, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(remaining) != "process stdin must remain untouched\n" {
		t.Fatalf("session explicit input consumed process stdin: %q", remaining)
	}
}

func TestEditorRetryUsesProvidedReader(t *testing.T) {
	input := bufio.NewReader(strings.NewReader("y\nnext command\n"))
	if !retryEditor(errors.New("invalid draft"), input) {
		t.Fatal("retry answer was not accepted")
	}
	next, err := input.ReadString('\n')
	if err != nil || next != "next command\n" {
		t.Fatalf("retry reader consumed following input: %q %v", next, err)
	}
}
