package cli

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/store"
)

type promptWriter struct {
	ready chan<- struct{}
	data  bytes.Buffer
}

type promptEventWriter struct {
	mu     sync.Mutex
	data   bytes.Buffer
	events chan string
}

func (w *promptEventWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	n, err := w.data.Write(data)
	w.mu.Unlock()
	w.events <- string(data)
	return n, err
}

func waitForPromptEvent(t *testing.T, events <-chan string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			if strings.Contains(event, "> ") {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for interactive prompt")
		}
	}
}

func (w *promptWriter) Write(data []byte) (int, error) {
	if w.ready != nil {
		close(w.ready)
		w.ready = nil
	}
	return w.data.Write(data)
}

func unsetEnvironment(t *testing.T, name string) {
	t.Helper()
	value, ok := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(name, value)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}

func runShell(t *testing.T, input string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString(input); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var out, errOut bytes.Buffer
	argv := append([]string{"-i"}, args...)
	code = runInteractiveIO(argv, &out, reader, &errOut)
	return out.String(), errOut.String(), code
}

func TestInteractiveCommandLineTokenizationAcrossPlatforms(t *testing.T) {
	tests := []struct {
		name string
		line string
		want []string
	}{
		{name: "linux", line: `create "/home/alice/My Tickets" 'Linux objective'`, want: []string{"create", "/home/alice/My Tickets", "Linux objective"}},
		{name: "macos", line: `show "/Users/Ada Lovelace/Tickets/current"`, want: []string{"show", "/Users/Ada Lovelace/Tickets/current"}},
		{name: "windows", line: `create "C:\Users\Ada Lovelace\Tickets" "Keep C:\work\ticket literal"`, want: []string{"create", `C:\Users\Ada Lovelace\Tickets`, `Keep C:\work\ticket literal`}},
		{name: "empty quoted argument", line: `create ""`, want: []string{"create", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitCommandLine(tt.line)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
				t.Fatalf("split=%q want %q", got, tt.want)
			}
		})
	}
	if _, err := splitCommandLine(`show "unterminated`); err == nil || !strings.Contains(err.Error(), "unterminated quote") {
		t.Fatalf("unterminated quote error: %v", err)
	}
}

func TestInteractiveShellPreservesWindowsStyleBackslashes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	unsetEnvironment(t, "TICKET_CURRENT")

	out, stderr, code := runShell(t, `create "C:\Users\Ada Lovelace\Tickets" "Keep the Windows path literal."`+"\nstatus\nexit\n")
	if code != 0 || !strings.Contains(out, `C:\Users\Ada Lovelace\Tickets`) {
		t.Fatalf("Windows-style shell argument: code=%d stdout=%q stderr=%q", code, out, stderr)
	}
}

func TestInteractiveShellSeedsCurrentAndKeepsPersistentMarkerUntouched(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Shell current", "Exercise the shell."))["id"].(string)
	if _, code := runCLIHuman(t, "show", id); code != 0 {
		t.Fatalf("select current ticket: exit=%d", code)
	}
	unsetEnvironment(t, "TICKET_CURRENT")
	markerPath := filepath.Join(dir, "tickets", ".local", "current")
	before, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}

	out, stderr, code := runShell(t, "status\nexit\n")
	if code != 0 || !strings.Contains(out, id) || !strings.Contains(stderr, "ticket "+id+" [open]> ") {
		t.Fatalf("seeded shell: code=%d stdout=%q stderr=%q", code, out, stderr)
	}
	after, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("shell changed persistent current marker: before=%q after=%q", before, after)
	}
}

func TestInteractiveShellInvalidEnvironmentCurrentDoesNotFallBack(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	exactlyOneJSONObject(t, mustCLI(t, "create", "Marker current", "Environment must win."))
	t.Setenv("TICKET_CURRENT", "missing-ticket")

	out, stderr, code := runShell(t, "exit\n")
	if code != 0 || out != "" || !strings.Contains(stderr, "TICKET_CURRENT is not a resolvable ticket") || !strings.Contains(stderr, "ticket [no current]> ") {
		t.Fatalf("invalid environment seed: code=%d stdout=%q stderr=%q", code, out, stderr)
	}
}

func TestInteractiveShellUsesDispatcherAndRecoversFromErrors(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Shell dispatch", "Use ordinary argv dispatch."))["id"].(string)
	unsetEnvironment(t, "TICKET_CURRENT")

	out, stderr, code := runShell(t, "show \""+id+"\"\nshow \"unterminated\nstatus\nexit\n")
	if code != 0 || !strings.Contains(out, "Shell dispatch") || !strings.Contains(out, id) || !strings.Contains(stderr, "unterminated quote") {
		t.Fatalf("shell dispatch/recovery: code=%d stdout=%q stderr=%q", code, out, stderr)
	}
}

func TestInteractiveShellRefreshesStateAndRetainsDeletedCurrent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Shell state", "Refresh state."))["id"].(string)
	unsetEnvironment(t, "TICKET_CURRENT")

	out, stderr, code := runShell(t, "state "+id+" rejected\ndelete "+id+"\nexit\n")
	if code != 0 || !strings.Contains(out, "rejected") || !strings.Contains(stderr, "ticket "+id+" [rejected]> ") || !strings.Contains(stderr, "ticket "+id+" [missing]> ") {
		t.Fatalf("shell state refresh/deletion: code=%d stdout=%q stderr=%q", code, out, stderr)
	}
}

func TestInteractiveShellDoesNotConsumeFollowingCreateCommand(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	unsetEnvironment(t, "TICKET_CURRENT")

	out, stderr, code := runShell(t, "create \"First shell ticket\"\nstatus\nexit\n")
	if code != 0 || !strings.Contains(out, "created ") || !strings.Contains(out, "First shell ticket") || strings.Contains(stderr, "Input stream") {
		t.Fatalf("shell create input ownership: code=%d stdout=%q stderr=%q", code, out, stderr)
	}
}

func TestInteractiveShellEditorCreateSelectsSessionCurrentOnly(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	previous := exactlyOneJSONObject(t, mustCLI(t, "create", "Persistent current", "Keep this marker."))["id"].(string)
	if _, code := runCLIHuman(t, "show", previous); code != 0 {
		t.Fatalf("select persistent current: exit=%d", code)
	}
	marker := filepath.Join(dir, "tickets", ".local", "current")
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	valid := "---\nstate: open\npriority: 2\n---\n# Shell editor created\n\n## Objective\n\nSelect only in the session.\n"
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-write")
	t.Setenv("EDITOR", editor)
	t.Setenv("TEST_EDITOR_BODY", valid)
	unsetEnvironment(t, "TICKET_CURRENT")

	out, stderr, code := runShell(t, "create -e \"Draft\"\nstatus\nexit\n")
	if code != 0 || !strings.Contains(out, "created ") || !strings.Contains(out, "Shell editor created") {
		t.Fatalf("shell editor create: code=%d stdout=%q stderr=%q", code, out, stderr)
	}
	fields := strings.Fields(out)
	if len(fields) < 2 || !strings.Contains(stderr, "ticket "+fields[1]+" [open]> ") {
		t.Fatalf("shell did not select editor-created ticket: stdout=%q stderr=%q", out, stderr)
	}
	after, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("shell editor create changed persistent current: before=%q after=%q", before, after)
	}
}

func TestInteractiveShellEditorEditSelectsSessionCurrentOnly(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	previous := exactlyOneJSONObject(t, mustCLI(t, "create", "Persistent edit current", "Keep this marker."))["id"].(string)
	target := exactlyOneJSONObject(t, mustCLI(t, "create", "Session edit target", "Edit and select this ticket."))["id"].(string)
	if _, code := runCLIHuman(t, "show", previous); code != 0 {
		t.Fatalf("select persistent current: exit=%d", code)
	}
	marker := filepath.Join(dir, "tickets", ".local", "current")
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-append")
	t.Setenv("EDITOR", editor)
	t.Setenv("TEST_EDITOR_BODY", "\n## Handoff\nEdited inside the shell.\n")
	unsetEnvironment(t, "TICKET_CURRENT")

	out, stderr, code := runShell(t, "edit "+target+"\nstatus\nexit\n")
	if code != 0 || !strings.Contains(out, "edited "+target) || !strings.Contains(out, target) ||
		!strings.Contains(stderr, "ticket "+target+" [open]> ") {
		t.Fatalf("shell editor edit: code=%d stdout=%q stderr=%q", code, out, stderr)
	}
	after, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("shell editor edit changed persistent current: before=%q after=%q", before, after)
	}
}

func TestInteractiveShellEditorValidationDoesNotConsumeFollowingCommand(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	previous := exactlyOneJSONObject(t, mustCLI(t, "create", "Previous session current", "Preserve this selection."))["id"].(string)
	if _, code := runCLIHuman(t, "show", previous); code != 0 {
		t.Fatalf("select persistent current: exit=%d", code)
	}
	marker := filepath.Join(dir, "tickets", ".local", "current")
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	unsetEnvironment(t, "TICKET_CURRENT")
	invalid := "---\nstate: open\npriority: 2\n---\n# Broken title\n\n# Accidental heading\n\n## Objective\n\nIncomplete.\n"
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-write")
	t.Setenv("EDITOR", editor)
	t.Setenv("TEST_EDITOR_BODY", invalid)

	out, stderr, code := runShell(t, "create -e \"Draft\"\nstatus\nversion\nexit\n")
	if code != 0 || !strings.Contains(out, previous) || !strings.Contains(out, "ticket "+Version+" (api") {
		t.Fatalf("shell command after invalid editor draft: code=%d stdout=%q stderr=%q", code, out, stderr)
	}
	if !strings.Contains(stderr, "Only one H1 heading is allowed") || !strings.Contains(stderr, "Session current ticket remains "+previous) ||
		strings.Contains(stderr, "Continue editing?") || strings.Contains(stderr, "TICKET_CURRENT") {
		t.Fatalf("shell editor validation behavior: %q", stderr)
	}
	after, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("failed shell editor create changed persistent current: before=%q after=%q", before, after)
	}
}

func TestInteractiveShellEditorFailurePreservesSessionCurrent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	previous := exactlyOneJSONObject(t, mustCLI(t, "create", "Previous editor current", "Preserve this selection."))["id"].(string)
	if _, code := runCLIHuman(t, "show", previous); code != 0 {
		t.Fatalf("select persistent current: exit=%d", code)
	}
	marker := filepath.Join(dir, "tickets", ".local", "current")
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-fail")
	t.Setenv("EDITOR", editor)
	unsetEnvironment(t, "TICKET_CURRENT")

	out, stderr, code := runShell(t, "new \"Failed editor create\" -e\nstatus\nexit\n")
	if code != 0 || !strings.Contains(out, previous) {
		t.Fatalf("shell command after editor failure: code=%d stdout=%q stderr=%q", code, out, stderr)
	}
	if !strings.Contains(stderr, "Editor failed") || !strings.Contains(stderr, "Session current ticket remains "+previous) || strings.Contains(stderr, "TICKET_CURRENT") {
		t.Fatalf("shell editor failure diagnostic: %q", stderr)
	}
	after, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("editor failure changed persistent current: before=%q after=%q", before, after)
	}
}

func TestInteractiveShellExecutesFinalLineAtEOF(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	unsetEnvironment(t, "TICKET_CURRENT")

	out, stderr, code := runShell(t, "version")
	if code != 0 || !strings.Contains(out, "ticket "+Version+" (api") {
		t.Fatalf("final shell line at EOF: code=%d stdout=%q stderr=%q", code, out, stderr)
	}
}

func TestInteractiveShellHasNoLockWhileWaitingForInput(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	ready := make(chan struct{})
	stderr := &promptWriter{ready: ready}
	var out bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- runInteractiveIO([]string{"-i"}, &out, reader, stderr) }()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("interactive shell did not render its prompt")
	}
	st, err := store.Open(dir, store.OpenOptions{})
	if err != nil {
		t.Fatalf("shell retained repository lock: %v", err)
	}
	st.Close()
	if _, err := writer.WriteString("exit\n"); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != 0 {
		t.Fatalf("shell exit: %d", code)
	}
}

func TestInteractivePromptDoesNotSynchronizeSCM(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	installFakeSCM(t, filepath.Join(dir, "bin"), "scm-lifecycle")
	logPath := filepath.Join(dir, "scm.log")
	t.Setenv("SCM_LOG", logPath)
	t.Setenv("TICKET_SCM", "git")
	t.Setenv("TICKET_SCM_MODE", "sync")
	unsetEnvironment(t, "TICKET_CURRENT")

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	ready := make(chan struct{})
	stderr := &promptWriter{ready: ready}
	var out bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- runInteractiveIO([]string{"-i"}, &out, reader, stderr) }()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("interactive shell did not render its prompt")
	}
	if data, err := os.ReadFile(logPath); err == nil {
		if len(data) != 0 {
			t.Fatalf("prompt synchronized SCM: %q", data)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := writer.WriteString("exit\n"); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != 0 {
		t.Fatalf("shell exit: %d", code)
	}
}

func TestInteractiveWaitStopsWhenSessionIsInterrupted(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	done := make(chan struct{})
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}, done: done}
	close(done)
	err := waitForNext(ctx, domain.NextOptions{})
	if !errors.Is(err, errSessionInterrupted) {
		t.Fatalf("wait interruption: %v", err)
	}
}

func testInteractivePromptInterruptDoesNotCancelNextWait(t *testing.T, realSignal bool) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	unsetEnvironment(t, "TICKET_CURRENT")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	stderr := &promptEventWriter{events: make(chan string, 16)}
	var out bytes.Buffer
	done := make(chan int, 1)
	interrupts := make(chan os.Signal, 2)
	if realSignal {
		go func() { done <- runInteractiveIO([]string{"-i"}, &out, reader, stderr) }()
	} else {
		executor := newSessionExecutor()
		if err := executor.bind(dir, globalOpts{}); err != nil {
			t.Fatal(err)
		}
		seedInteractiveCurrent(executor, stderr)
		go func() {
			done <- interactiveLoopWithInterrupts(executor, bufio.NewReader(reader), &out, stderr, interrupts)
		}()
	}
	waitForPromptEvent(t, stderr.events)

	sendInterrupt := func() error {
		if !realSignal {
			interrupts <- os.Interrupt
			return nil
		}
		process, err := os.FindProcess(os.Getpid())
		if err != nil {
			return err
		}
		return process.Signal(os.Interrupt)
	}
	if err := sendInterrupt(); err != nil {
		t.Fatal(err)
	}
	waitForPromptEvent(t, stderr.events)
	if _, err := writer.WriteString("wait\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-stderr.events:
		t.Fatalf("idle interrupt leaked into wait; unexpected stderr write %q", event)
	case code := <-done:
		t.Fatalf("shell exited while wait should be active: %d", code)
	case <-time.After(500 * time.Millisecond):
	}

	if err := sendInterrupt(); err != nil {
		t.Fatal(err)
	}
	waitForPromptEvent(t, stderr.events)
	if _, err := writer.WriteString("exit\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("shell exit: %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shell did not exit after interrupted wait")
	}
}

func TestInteractivePromptInterruptDoesNotCancelNextWait(t *testing.T) {
	testInteractivePromptInterruptDoesNotCancelNextWait(t, false)
}

func TestInteractivePromptRealSignalDoesNotCancelNextWait(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Go cannot send os.Interrupt to a Windows process")
	}
	testInteractivePromptInterruptDoesNotCancelNextWait(t, true)
}
