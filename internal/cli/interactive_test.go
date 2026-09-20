package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/store"
)

type interactiveResult struct {
	out  string
	code int
	err  error
}

func writeBlockingEditor(t *testing.T, dir, body string) (editor, started, release string) {
	t.Helper()
	started = filepath.Join(dir, "editor-started")
	release = filepath.Join(dir, "editor-release")
	editor = installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-block")
	t.Setenv("EDITOR_STARTED", started)
	t.Setenv("EDITOR_RELEASE", release)
	t.Setenv("TEST_EDITOR_BODY", body)
	return editor, started, release
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func releaseEditor(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInteractiveEditReleasesRepositoryLock(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	a := exactlyOneJSONObject(t, mustCLI(t, "create", "Edit A", "Edit the first ticket."))["id"].(string)
	b := exactlyOneJSONObject(t, mustCLI(t, "create", "Edit B", "Claim the second ticket."))["id"].(string)
	editor, started, release := writeBlockingEditor(t, dir, "\n## Handoff\nEdited after releasing the lock.\n")
	t.Setenv("EDITOR", editor)
	defer releaseEditor(t, release)

	result := make(chan interactiveResult, 1)
	go func() {
		var out bytes.Buffer
		code := Run([]string{"edit", a}, &out)
		result <- interactiveResult{out: out.String(), code: code}
	}()
	waitForFile(t, started)

	st, err := store.Open(dir, store.OpenOptions{})
	if err != nil {
		t.Fatalf("second operation could not acquire lock: %v", err)
	}
	if _, err := domain.Claim(st, b, domain.ClaimOptions{Actor: "agent"}); err != nil {
		st.Close()
		t.Fatalf("second operation while editor open: %v", err)
	}
	st.Close()

	releaseEditor(t, release)
	got := <-result
	if got.code != 0 || got.out != "edited "+a+"\n" {
		t.Fatalf("interactive edit result: code=%d out=%q err=%v", got.code, got.out, got.err)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", a, "--full"))
	if !strings.Contains(view["body"].(string), "Edited after releasing the lock.") {
		t.Fatalf("edited ticket was not published: %v", view["body"])
	}
}

func TestInteractiveEditRefusesConcurrentChangeAndPreservesDraft(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Concurrent edit", "Protect concurrent changes."))["id"].(string)
	editor, started, release := writeBlockingEditor(t, dir, "\n## Handoff\nEdited draft.\n")
	t.Setenv("EDITOR", editor)
	defer releaseEditor(t, release)

	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}}
	result := make(chan error, 1)
	go func() { result <- editWithEditor(ctx, id) }()
	waitForFile(t, started)

	st, err := store.Open(dir, store.OpenOptions{})
	if err != nil {
		t.Fatalf("concurrent mutation lock: %v", err)
	}
	if _, err := domain.Update(st, id, domain.UpdateOptions{Sections: map[string]string{"handoff": "Concurrent live change."}}); err != nil {
		st.Close()
		t.Fatalf("concurrent mutation: %v", err)
	}
	st.Close()
	releaseEditor(t, release)
	err = <-result
	var ce *contract.Error
	if !errors.As(err, &ce) || ce.Code != contract.ErrConflict {
		t.Fatalf("concurrent edit error=%v", err)
	}
	if !strings.Contains(ce.Message, "Edited draft preserved at:") {
		t.Fatalf("concurrent edit did not preserve draft: %v", ce)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	body := view["body"].(string)
	if !strings.Contains(body, "Concurrent live change.") || strings.Contains(body, "Edited draft.") {
		t.Fatalf("concurrent edit overwrote live content: %q", body)
	}
}

func TestInteractiveEditRequiresOwnershipBeforeEditor(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Owned edit", "Keep edit ownership enforced."))["id"].(string)
	mustCLI(t, "claim", id, "--actor", "alice")
	path := filepath.Join(dir, "tickets", id, "TASK.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-block")
	t.Setenv("EDITOR", editor)
	t.Setenv("TICKET_ACTOR", "bob")
	if stdout, stderr, code := runCLIHumanError(t, "edit", id); code != 4 || stdout != "" || !strings.Contains(stderr, "assigned to another actor") {
		t.Fatalf("different-owner edit: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if fileExists(filepath.Join(dir, "editor-started")) {
		t.Fatal("editor started for a ticket owned by another actor")
	}
	t.Setenv("TICKET_ACTOR", "")
	if stdout, stderr, code := runCLIHumanError(t, "edit", id); code != 2 || stdout != "" || !strings.Contains(stderr, "actor is required") {
		t.Fatalf("no-actor edit: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected edits changed TASK.md")
	}
}

func TestInteractiveEditRechecksOwnershipBeforePublication(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Ownership race", "Keep the newer owner safe."))["id"].(string)
	editor, started, release := writeBlockingEditor(t, dir, "\n## Handoff\nEdited draft must not publish.\n")
	t.Setenv("EDITOR", editor)
	t.Setenv("TICKET_ACTOR", "worker")
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}}
	result := make(chan error, 1)
	go func() { result <- editWithEditor(ctx, id) }()
	waitForFile(t, started)

	st, err := store.Open(dir, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := domain.Claim(st, id, domain.ClaimOptions{Actor: "other"}); err != nil {
		st.Close()
		t.Fatalf("concurrent claim: %v", err)
	}
	st.Close()
	releaseEditor(t, release)
	err = <-result
	var ce *contract.Error
	if !errors.As(err, &ce) || ce.Code != contract.ErrAlreadyClaimed {
		t.Fatalf("ownership race error=%v", err)
	}
	if !strings.Contains(ce.Message, "Edited draft preserved at:") {
		t.Fatalf("ownership race did not preserve draft: %v", ce)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	body := view["body"].(string)
	if view["assignee"] != "other" || strings.Contains(body, "Edited draft must not publish.") {
		t.Fatalf("ownership race changed newer ticket: %v", view)
	}
}

func TestInteractiveEditAllowsAssignedActor(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Owned edit allowed", "Allow the assigned actor to edit."))["id"].(string)
	mustCLI(t, "claim", id, "--actor", "worker")
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-append")
	t.Setenv("EDITOR", editor)
	t.Setenv("TEST_EDITOR_BODY", "\n## Handoff\nEdited by owner.\n")
	t.Setenv("TICKET_ACTOR", "worker")
	if out, code := runCLIHuman(t, "edit", id); code != 0 || !strings.Contains(out, "edited "+id) {
		t.Fatalf("assigned-owner edit: exit=%d out=%q", code, out)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	if !strings.Contains(view["body"].(string), "Edited by owner.") {
		t.Fatalf("assigned-owner edit was not published: %v", view)
	}
}

func TestInteractiveNewIsInvisibleUntilPublication(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	editor, started, release := writeBlockingEditor(t, dir, "\n## Objective\nPublished after editing.\n")
	t.Setenv("EDITOR", editor)
	defer releaseEditor(t, release)
	result := make(chan interactiveResult, 1)
	go func() {
		var out bytes.Buffer
		result <- interactiveResult{code: Run([]string{"new", "Hidden ticket", "-e"}, &out), out: out.String()}
	}()
	waitForFile(t, started)

	st, err := store.Open(dir, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := domain.List(st, domain.ListOptions{State: "all", Limit: 100})
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 0 {
		t.Fatalf("draft was visible before publication: %+v", listed.Items)
	}
	releaseEditor(t, release)
	got := <-result
	if got.code != 0 || !strings.HasPrefix(got.out, "created ") {
		t.Fatalf("new editor result: code=%d out=%q", got.code, got.out)
	}
	st, err = store.Open(dir, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	listed, err = domain.List(st, domain.ListOptions{State: "all", Limit: 100})
	st.Close()
	if err != nil || len(listed.Items) != 1 || listed.Items[0].Title == nil || *listed.Items[0].Title != "Hidden ticket" {
		t.Fatalf("published new ticket: err=%v items=%+v", err, listed.Items)
	}
}

func TestInteractiveEditorFailurePreservesDraftAndLiveTicket(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Editor failure", "Keep the live ticket safe."))["id"].(string)
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-fail")
	t.Setenv("TEST_EDITOR_BODY", "\n## Handoff\nPartial draft.\n")
	t.Setenv("EDITOR", editor)
	stdout, stderr, code := runCLIHumanError(t, "edit", id)
	if code == 0 || stdout != "" || !strings.Contains(stderr, "Editor failed") || !strings.Contains(stderr, "Edited draft preserved at:") {
		t.Fatalf("editor failure: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	body := view["body"].(string)
	if strings.Contains(body, "Partial draft.") || !strings.Contains(body, "Keep the live ticket safe.") {
		t.Fatalf("editor failure changed live ticket: %q", body)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "tickets", ".local"))
	if err != nil {
		t.Fatal(err)
	}
	foundDraft := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "draft-") {
			foundDraft = true
			break
		}
	}
	if !foundDraft {
		t.Fatalf("editor draft was not preserved: %v", entries)
	}
}
