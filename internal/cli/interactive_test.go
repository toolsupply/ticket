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

func TestInteractiveEditClaimsUnassignedTicketBeforeEditor(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Claim before edit", "Claim this ticket before opening the editor."))["id"].(string)
	editor, started, release := writeBlockingEditor(t, dir, "\n## Handoff\nEdited after claiming.\n")
	t.Setenv("EDITOR", editor)
	defer releaseEditor(t, release)

	result := make(chan int, 1)
	go func() {
		var out bytes.Buffer
		result <- Run([]string{"edit", id, "--actor", "worker"}, &out)
	}()
	waitForFile(t, started)
	st, err := store.Open(dir, store.OpenOptions{})
	if err != nil {
		t.Fatalf("open repository while editing: %v", err)
	}
	ticket, err := domain.ReadTicket(st, id)
	st.Close()
	if err != nil {
		t.Fatalf("read ticket while editing: %v", err)
	}
	if ticket.Assignee != "worker" {
		t.Fatalf("ticket was not claimed before editor: %q", ticket.Assignee)
	}
	releaseEditor(t, release)
	if code := <-result; code != 0 {
		t.Fatalf("edit after automatic claim failed: exit=%d", code)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	if view["assignee"] != nil {
		t.Fatalf("temporary editor claim was retained: %v", view)
	}
}

func TestInteractiveEditReleasesTemporaryReviewClaim(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Review edit", "Edit the review ticket."))["id"].(string)
	mustCLI(t, "submit", id)
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-append")
	t.Setenv("EDITOR", editor)
	t.Setenv("TEST_EDITOR_BODY", "\n## Handoff\nEdited during review.\n")
	t.Setenv("TICKET_ACTOR", "reviewer")
	if out, code := runCLIHuman(t, "edit", id); code != 0 || !strings.Contains(out, "edited "+id) {
		t.Fatalf("review edit: exit=%d out=%q", code, out)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	if view["assignee"] != nil || !strings.Contains(view["body"].(string), "Edited during review.") {
		t.Fatalf("review edit ownership or body: %v", view)
	}
}

func TestInteractiveEditPreservesUnassignedNonClaimableStates(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	holdID := exactlyOneJSONObject(t, mustCLI(t, "create", "Hold edit"))["id"].(string)
	blockedID := exactlyOneJSONObject(t, mustCLI(t, "create", "Blocked open edit", "Edit despite a blocker."))["id"].(string)
	runCLIStdinMust(t, `{"set":{"blocked_reason":"Waiting for an external dependency."}}`, "update", blockedID, "--input", "-")
	completedID := exactlyOneJSONObject(t, mustCLI(t, "create", "Completed edit", "Complete before editing."))["id"].(string)
	mustCLI(t, "submit", completedID)
	mustCLI(t, "close", completedID)
	t.Setenv("TICKET_ACTOR", "")
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-append")
	t.Setenv("EDITOR", editor)
	for _, test := range []struct {
		id   string
		body string
	}{
		{holdID, "Edited while held."},
		{blockedID, "Edited while blocked."},
		{completedID, "Edited after completion."},
	} {
		t.Setenv("TEST_EDITOR_BODY", "\n## Handoff\n"+test.body+"\n")
		out, stderr, code := runCLIHumanError(t, "edit", test.id)
		if code != 0 || !strings.Contains(out, "edited "+test.id) || stderr != "" {
			t.Fatalf("edit %s: exit=%d out=%q stderr=%q", test.id, code, out, stderr)
		}
		view := exactlyOneJSONObject(t, mustCLI(t, "show", test.id, "--full"))
		if view["assignee"] != nil || !strings.Contains(view["body"].(string), test.body) {
			t.Fatalf("non-claimable edit changed ownership or body: %v", view)
		}
	}
}

func TestInteractiveEditRefusesConcurrentChangeAndPreservesDraft(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Concurrent edit", "Protect concurrent changes."))["id"].(string)
	t.Setenv("TICKET_ACTOR", "user")
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
	if _, err := domain.Update(st, id, domain.UpdateOptions{Actor: "user", Sections: map[string]string{"handoff": "Concurrent live change."}}); err != nil {
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
	if view["assignee"] != nil || !strings.Contains(body, "Concurrent live change.") || strings.Contains(body, "Edited draft.") {
		t.Fatalf("concurrent edit overwrote live content: %q", body)
	}
}

func TestInteractiveEditWarnsForAnotherOwnerAndUsesUserFallback(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Owned edit", "Keep edit ownership enforced."))["id"].(string)
	mustCLI(t, "claim", id, "--actor", "alice")
	path := filepath.Join(dir, "tickets", id, "TASK.md")
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-append")
	t.Setenv("EDITOR", editor)
	t.Setenv("TICKET_ACTOR", "bob")
	t.Setenv("TEST_EDITOR_BODY", "\n## Handoff\nEdited by another actor.\n")
	if stdout, stderr, code := runCLIHumanError(t, "edit", id); code != 0 || !strings.Contains(stdout, "edited "+id) || !strings.Contains(stderr, "editing without changing ownership") {
		t.Fatalf("different-owner edit: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	if view["assignee"] != "alice" || !strings.Contains(view["body"].(string), "Edited by another actor.") {
		t.Fatalf("different-owner edit changed ownership or failed to edit: %v", view)
	}
	t.Setenv("TICKET_ACTOR", "")
	t.Setenv("TEST_EDITOR_BODY", "\n## Handoff\nEdited as user.\n")
	if stdout, stderr, code := runCLIHumanError(t, "edit", id); code != 0 || !strings.Contains(stdout, "edited "+id) || !strings.Contains(stderr, "actor=user") {
		t.Fatalf("no-actor edit: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(after, []byte("Edited as user.")) {
		t.Fatal("fallback user edit did not publish")
	}
}

func TestInteractiveEditRechecksOwnershipBeforePublication(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Ownership race", "Keep the newer owner safe."))["id"].(string)
	mustCLI(t, "claim", id, "--actor", "worker")
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
	if _, err := domain.Release(st, id, domain.ReleaseOptions{Actor: "worker"}); err != nil {
		st.Close()
		t.Fatalf("concurrent release: %v", err)
	}
	if _, err := domain.Claim(st, id, domain.ClaimOptions{Actor: "other"}); err != nil {
		st.Close()
		t.Fatalf("concurrent claim: %v", err)
	}
	st.Close()
	releaseEditor(t, release)
	err = <-result
	var ce *contract.Error
	if !errors.As(err, &ce) || ce.Code != contract.ErrConflict {
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
	if view["assignee"] != "worker" || !strings.Contains(view["body"].(string), "Edited by owner.") {
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
	if err != nil || len(listed.Items) != 1 || listed.Items[0].Title == nil || *listed.Items[0].Title != "Hidden ticket" || listed.Items[0].Assignee != nil {
		t.Fatalf("published new ticket: err=%v items=%+v", err, listed.Items)
	}
}

func TestInteractiveEditReleasesClaimAfterDraftValidationFailure(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Invalid edit", "Keep the live ticket safe."))["id"].(string)
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-write")
	t.Setenv("EDITOR", editor)
	t.Setenv("TICKET_ACTOR", "worker")
	t.Setenv("TEST_EDITOR_BODY", "---\nstate: open\npriority: 2\n---\n# Broken title\n\n# Accidental heading\n\n## Objective\n\nIncomplete.\n")
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}, session: &sessionState{}}
	if err := editWithEditor(ctx, id); err == nil {
		t.Fatal("invalid editor draft unexpectedly succeeded")
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	if view["assignee"] != nil {
		t.Fatalf("temporary editor claim was retained after validation failure: %v", view)
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
	if view["assignee"] != nil || strings.Contains(body, "Partial draft.") || !strings.Contains(body, "Keep the live ticket safe.") {
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
