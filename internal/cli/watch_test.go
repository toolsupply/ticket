package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/toolsupply/ticket/internal/store"
)

func TestWatchReadyFollowsStartupMarkerCorrection(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	config, err := store.LoadConfig(filepath.Join(dir, "tickets"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var output bytes.Buffer
	ctx := &commandContext{cwd: dir, stdout: &output, globalOpts: globalOpts{json: true}}
	markerReads, snapshots := 0, 0
	ops := watchLoopOps{
		changeState: func(string) (string, error) {
			markerReads++
			if markerReads == 1 {
				return "before", nil
			}
			return "after", nil
		},
		fingerprint: func(string) ([32]byte, error) { return [32]byte{}, nil },
		snapshot: func(string, string) (map[string]watchSnapshot, error) {
			snapshots++
			return map[string]watchSnapshot{}, nil
		},
		ready: func(repositoryID string) error {
			if snapshots != 2 || markerReads != 2 {
				t.Fatalf("READY before correction completed: snapshots=%d markerReads=%d", snapshots, markerReads)
			}
			if err := emitWatchReady(ctx, repositoryID); err != nil {
				return err
			}
			close(done)
			return nil
		},
	}
	err = runWatchLoopWith(ctx, watchOptions{Ready: true}, done,
		watchLoopTiming{marker: time.Hour, fingerprint: time.Hour}, ops)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("READY records=%q", output.String())
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("decode READY: %v", err)
	}
	if record["type"] != "ready" || record["repository_id"] != config.ID || len(record) != 2 {
		t.Fatalf("READY record=%v, repository ID=%q", record, config.ID)
	}
}

func TestWatchMutationBeforeReadyIsVisibleToReconciliation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	var output bytes.Buffer
	ctx := &commandContext{cwd: dir, stdout: &output, globalOpts: globalOpts{json: true}}
	done := make(chan struct{})
	mutationID := ""
	mutated := false
	ops := watchLoopOps{
		changeState: store.ChangeState,
		fingerprint: watchMetadataFingerprint,
		snapshot: func(cwd, root string) (map[string]watchSnapshot, error) {
			if !mutated {
				mutationID = exactlyOneJSONObject(t, mustCLI(t, "create", "Before READY", "Visible to reconciliation."))["id"].(string)
				mutated = true
			}
			return watchSnapshotTickets(cwd, root)
		},
		ready: func(repositoryID string) error {
			if err := emitWatchReady(ctx, repositoryID); err != nil {
				return err
			}
			close(done)
			return nil
		},
	}
	if err := runWatchLoopWith(ctx, watchOptions{Ready: true}, done,
		watchLoopTiming{marker: time.Hour, fingerprint: time.Hour}, ops); err != nil {
		t.Fatal(err)
	}
	reconciled, err := watchSnapshotTickets(dir, filepath.Join(dir, "tickets"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reconciled[mutationID]; !ok {
		t.Fatalf("post-READY reconciliation missed pre-READY mutation %s", mutationID)
	}
}

func TestWatchReadyWriteFailureStopsBeforeObservation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	done := make(chan struct{})
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}, liveWriter: failingWatchWriter{}, globalOpts: globalOpts{json: true}}
	called := false
	ops := watchLoopOps{
		changeState: func(string) (string, error) { return "marker", nil },
		fingerprint: func(string) ([32]byte, error) { return [32]byte{}, nil },
		snapshot:    func(string, string) (map[string]watchSnapshot, error) { return map[string]watchSnapshot{}, nil },
		ready:       func(repositoryID string) error { called = true; return emitWatchReady(ctx, repositoryID) },
	}
	err := runWatchLoopWith(ctx, watchOptions{Ready: true}, done,
		watchLoopTiming{marker: time.Hour, fingerprint: time.Hour}, ops)
	if !called || err == nil || !strings.Contains(err.Error(), "write watch readiness") {
		t.Fatalf("ready called=%v error=%v", called, err)
	}
}

func TestWatchStartupFailureDoesNotEmitReady(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	var output bytes.Buffer
	ctx := &commandContext{cwd: dir, stdout: &output, globalOpts: globalOpts{json: true}}
	readyCalls := 0
	ops := watchLoopOps{
		changeState: func(string) (string, error) { return "marker", nil },
		fingerprint: func(string) ([32]byte, error) { return [32]byte{}, nil },
		snapshot:    func(string, string) (map[string]watchSnapshot, error) { return nil, errors.New("snapshot failed") },
		ready:       func(string) error { readyCalls++; return nil },
	}
	err := runWatchLoopWith(ctx, watchOptions{Ready: true}, make(chan struct{}),
		watchLoopTiming{marker: time.Hour, fingerprint: time.Hour}, ops)
	if err == nil || readyCalls != 0 || output.Len() != 0 {
		t.Fatalf("startup error=%v READY calls=%d output=%q", err, readyCalls, output.String())
	}
}

func TestWatchReadyKeepsPostStartupEventsObservable(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	started := make(chan struct{})
	changeRead := 0
	snapshotCount := 0
	output := &watchNotifyBuffer{writes: make(chan struct{}, 4)}
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}, liveWriter: output, globalOpts: globalOpts{json: true}}
	done := make(chan struct{})
	ops := watchLoopOps{
		changeState: func(string) (string, error) {
			changeRead++
			if changeRead <= 2 {
				return "baseline", nil
			}
			if changeRead == 3 {
				close(started)
			}
			return "changed", nil
		},
		fingerprint: func(string) ([32]byte, error) { return [32]byte{}, nil },
		snapshot: func(string, string) (map[string]watchSnapshot, error) {
			snapshotCount++
			if snapshotCount == 1 {
				return map[string]watchSnapshot{}, nil
			}
			return map[string]watchSnapshot{"20260929-00001": {ID: "20260929-00001", Title: "After ready", State: "open"}}, nil
		},
		ready: func(id string) error { return emitWatchReady(ctx, id) },
	}
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- runWatchLoopWith(ctx, watchOptions{Ready: true}, done,
			watchLoopTiming{marker: time.Millisecond, fingerprint: time.Hour}, ops)
	}()
	select {
	case <-output.writes:
	case <-time.After(2 * time.Second):
		close(done)
		t.Fatal("watch did not emit READY")
	}
	if !strings.Contains(output.String(), `"type":"ready"`) {
		close(done)
		t.Fatalf("first watch output was not READY: %q", output.String())
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		close(done)
		t.Fatal("watch did not begin post-READY observation")
	}
	select {
	case <-output.writes:
	case <-time.After(2 * time.Second):
		close(done)
		t.Fatal("watch did not emit post-READY event")
	}
	close(done)
	if err := <-watchDone; err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"type":"ready"`) || !strings.Contains(lines[1], `"event":"created"`) {
		t.Fatalf("watch output=%q, want one READY then one ordinary created event", output.String())
	}
}

type watchNotifyBuffer struct {
	mu     sync.Mutex
	b      bytes.Buffer
	writes chan struct{}
}

func (w *watchNotifyBuffer) Write(data []byte) (int, error) {
	w.mu.Lock()
	n, err := w.b.Write(data)
	w.mu.Unlock()
	w.writes <- struct{}{}
	return n, err
}

func (w *watchNotifyBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func TestWatchReadyIsOptInAndRequiresJSON(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	stdout, stderr, code := runCLIHumanError(t, "watch", "--ready")
	if code == 0 || !strings.Contains(stderr, "requires JSON mode") || stdout != "" {
		t.Fatalf("human watch --ready: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	help, code := runCLIHuman(t, "help", "watch")
	if code != 0 || !strings.Contains(help, "--ready") || !strings.Contains(help, "ticket watch -j --ready") {
		t.Fatalf("watch help: exit=%d output=%q", code, help)
	}
	defaultDone := make(chan struct{})
	close(defaultDone)
	var output bytes.Buffer
	ctx := &commandContext{cwd: dir, stdout: &output, globalOpts: globalOpts{json: true}, done: defaultDone}
	if err := cmdWatch(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("ordinary JSON watch emitted output: %q", output.String())
	}
	readyDone := make(chan struct{})
	close(readyDone)
	ctx = &commandContext{cwd: dir, stdout: &output, done: readyDone}
	if err := cmdWatch(ctx, []string{"--ready", "-j"}); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("already-cancelled opt-in JSON watch emitted output=%q", output.String())
	}
}

type failingWatchWriter struct{}

func (failingWatchWriter) Write([]byte) (int, error) { return 0, errors.New("closed output") }

func TestDeriveWatchEventsUsesConservativeSemanticKinds(t *testing.T) {
	old := watchSnapshot{ID: "20260921-00001", Title: "Before", State: "open", Handoff: "old"}
	now := old
	now.State = "review"
	now.Handoff = "new"
	events := diffWatchEvent(old, now)
	if len(events) != 2 || events[0].Event != "submitted" || events[0].From != "open" || events[0].To != "review" || events[1].Event != "handoff-updated" {
		t.Fatalf("events=%+v", events)
	}
}

func TestWatchMetadataFingerprintIsDeterministicAndTracksMetadataChanges(t *testing.T) {
	stamp := time.Date(2026, time.September, 29, 12, 34, 56, 125_000_000, time.FixedZone("offset", 3600))
	base := []store.WatchMetadata{
		{Path: "20260929-00001", Type: os.ModeDir, Size: 64, ModTime: stamp},
		{Path: "20260929-00001/TASK.md", Type: 0, Size: 9, ModTime: stamp},
	}
	fingerprint := fingerprintWatchMetadata(base)
	if again := fingerprintWatchMetadata(append([]store.WatchMetadata(nil), base...)); again != fingerprint {
		t.Fatalf("stable metadata fingerprint changed: %x != %x", fingerprint, again)
	}
	if utc := fingerprintWatchMetadata([]store.WatchMetadata{
		{Path: "20260929-00001", Type: os.ModeDir, Size: 64, ModTime: stamp.UTC()},
		{Path: "20260929-00001/TASK.md", Type: 0, Size: 9, ModTime: stamp.UTC()},
	}); utc != fingerprint {
		t.Fatalf("equivalent timezone changed fingerprint: %x != %x", fingerprint, utc)
	}

	changed := []struct {
		name    string
		records []store.WatchMetadata
	}{
		{"added", append(append([]store.WatchMetadata(nil), base...), store.WatchMetadata{
			Path: "20260929-00002", Type: os.ModeDir, Size: 64, ModTime: stamp,
		})},
		{"removed", []store.WatchMetadata{base[0]}},
		{"renamed", []store.WatchMetadata{base[0], {Path: "20260929-00001/RENAMED.md", Type: 0, Size: 9, ModTime: stamp}}},
		{"edited", []store.WatchMetadata{base[0], {Path: base[1].Path, Type: base[1].Type, Size: base[1].Size, ModTime: stamp.Add(250 * time.Millisecond)}}},
	}
	for _, change := range changed {
		name, records := change.name, change.records
		t.Run(name, func(t *testing.T) {
			if got := fingerprintWatchMetadata(records); got == fingerprint {
				t.Fatalf("%s metadata retained fingerprint %x", name, got)
			}
		})
	}
}

func TestWatchFingerprintPhaseIsStableAndRepositorySpecific(t *testing.T) {
	interval := 30 * time.Second
	first := watchFingerprintPhase("repository-a", interval)
	if first < 0 || first >= interval {
		t.Fatalf("phase=%s, want within [0, %s)", first, interval)
	}
	if again := watchFingerprintPhase("repository-a", interval); again != first {
		t.Fatalf("same repository phase changed: %s != %s", again, first)
	}
	if other := watchFingerprintPhase("repository-b", interval); other == first {
		t.Fatalf("distinct repositories shared phase %s", first)
	}
	buckets := make(map[int]bool)
	for i := 0; i < 12; i++ {
		identity := fmt.Sprintf("repository-%02d", i)
		buckets[int(watchFingerprintPhase(identity, interval)/(interval/6))] = true
	}
	if len(buckets) < 4 {
		t.Fatalf("repository phases occupied only %d of 6 interval buckets", len(buckets))
	}
	if zero := watchFingerprintPhase("repository-a", 0); zero != 0 {
		t.Fatalf("nonpositive interval phase=%s, want zero", zero)
	}
}

func TestWatchMetadataFingerprintExcludesChangeMarker(t *testing.T) {
	root := t.TempDir()
	id := "20260929-00001"
	if err := os.Mkdir(filepath.Join(root, id), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, id, "TASK.md"), []byte("ticket"), 0o600); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(root, ".local")
	if err := os.Mkdir(local, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(local, "change")
	if err := os.WriteFile(marker, []byte("first marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := watchMetadataFingerprint(root)
	if err != nil {
		t.Fatalf("fingerprint before marker change: %v", err)
	}
	if err := os.WriteFile(marker, []byte("a different marker value"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := watchMetadataFingerprint(root)
	if err != nil {
		t.Fatalf("fingerprint after marker change: %v", err)
	}
	if before != after {
		t.Fatalf(".local/change activity changed fingerprint: %x != %x", before, after)
	}
}

func TestWatchMetadataFingerprintTracksMalformedTicketDirectoriesOnly(t *testing.T) {
	root := t.TempDir()
	canonicalID := "20260929-00001"
	if err := os.Mkdir(filepath.Join(root, canonicalID), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, canonicalID, "TASK.md"), []byte("ticket"), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint := func() [32]byte {
		t.Helper()
		value, err := watchMetadataFingerprint(root)
		if err != nil {
			t.Fatalf("fingerprint watch metadata: %v", err)
		}
		return value
	}
	baseline := fingerprint()
	malformed := filepath.Join(root, "20260929-0000x")
	if err := os.Mkdir(malformed, 0o755); err != nil {
		t.Fatal(err)
	}
	added := fingerprint()
	if added == baseline {
		t.Fatal("adding a malformed ticket-looking directory did not change the fingerprint")
	}
	if err := os.Remove(malformed); err != nil {
		t.Fatal(err)
	}
	if removed := fingerprint(); removed != baseline {
		t.Fatalf("removing malformed ticket-looking directory did not restore fingerprint: %x != %x", removed, baseline)
	}

	for _, path := range []string{"README.md", filepath.Join(".local", "change"), filepath.Join(store.ArchiveDirName, "20260929-00002", "TASK.md")} {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte("ignored runtime or archive state"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if unrelated := fingerprint(); unrelated != baseline {
		t.Fatalf("unrelated, runtime, or archive entries changed fingerprint: %x != %x", unrelated, baseline)
	}
}

func TestWatchMetadataFingerprintFailureIsDistinct(t *testing.T) {
	if _, err := watchMetadataFingerprint(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing repository returned an unchanged fingerprint instead of an error")
	}
}

func TestWatchLifecycleAliasesAndClosedEvent(t *testing.T) {
	if !watchStateFilter("closed") || !watchStateFilter("completed") {
		t.Fatal("watch did not accept canonical and legacy closed filters")
	}
	if watchStateFilter("all") == false || watchStateFilter("unknown") {
		t.Fatal("watch state filter handling changed")
	}
	event, message := stateWatchEvent("signoff", "closed")
	if event != "closed" || message != "closed" {
		t.Fatalf("close watch event: event=%q message=%q", event, message)
	}
}

func TestDeriveWatchEventsCoversTicketChangesAndUnknownEdits(t *testing.T) {
	base := watchSnapshot{ID: "20260921-00001", Title: "Ticket", State: "open", FileBytes: []byte("old")}
	tests := []struct {
		name   string
		mutate func(*watchSnapshot)
		want   string
	}{
		{"created", nil, "created"},
		{"edited", func(s *watchSnapshot) { s.Title = "Renamed" }, "edited"},
		{"claimed", func(s *watchSnapshot) { s.Assignee = "coder" }, "claimed"},
		{"relationship", func(s *watchSnapshot) { s.Parent = "20260920-00001" }, "child-added"},
		{"dependency", func(s *watchSnapshot) { s.DependsOn = []string{"20260920-00002"} }, "dependency-changed"},
		{"work-log", func(s *watchSnapshot) { s.WorkLog = "entry" }, "work-log-updated"},
		{"unknown", func(s *watchSnapshot) { s.FileBytes = []byte("new") }, "updated"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := base
			if test.mutate != nil {
				test.mutate(&current)
				events := diffWatchEvent(base, current)
				if len(events) != 1 || events[0].Event != test.want {
					t.Fatalf("events=%+v, want %q", events, test.want)
				}
				return
			}
			events := deriveWatchEvents(nil, map[string]watchSnapshot{base.ID: current})
			if len(events) != 1 || events[0].Event != test.want {
				t.Fatalf("events=%+v, want %q", events, test.want)
			}
		})
	}
}

func TestWatchEventFiltersUseTicketMetadataAndEventOR(t *testing.T) {
	old := watchSnapshot{ID: "20260921-00001", State: "review"}
	current := old
	current.Assignee = "luna"
	current.Tags = []string{"backend", "urgent"}
	current.Parent = "20260920-00001"
	events := diffWatchEvent(old, current)
	var event watchEvent
	for _, candidate := range events {
		if candidate.Event == "claimed" {
			event = candidate
			break
		}
	}
	if event.Actor == nil || *event.Actor != "luna" {
		t.Fatalf("derived actor=%+v events=%+v", events, events)
	}
	if !watchEventMatches(event, watchOptions{
		Actor: "luna", Ticket: event.Ticket, State: "review", Tags: []string{"backend"},
		WithoutTags: []string{"frontend"}, Parent: event.parent, Events: []string{"claimed", "submitted"},
	}) {
		t.Fatal("matching watch filters rejected event")
	}
	for _, options := range []watchOptions{
		{Actor: "astra"},
		{Ticket: "20260921-00002"},
		{State: "open"},
		{Tags: []string{"frontend"}},
		{WithoutTags: []string{"urgent"}},
		{Parent: "20260920-00002"},
		{Events: []string{"approved"}},
	} {
		if watchEventMatches(event, options) {
			t.Fatalf("nonmatching filters accepted event: %+v", options)
		}
	}
	unknown := event
	unknown.Actor = nil
	if !watchEventMatches(unknown, watchOptions{Actor: "-"}) {
		t.Fatal("unknown actor filter rejected unknown actor")
	}
}

func TestWatchTagDefaultsMergeWithExplicitFilters(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Chdir(dir)
	setTestHome(t, home)
	t.Setenv("TICKET_SCOPE", "workers")
	t.Setenv("TICKET_WORK_TAGS", "networking")
	configPath := filepath.Join(home, ".ticket", "config.json")
	writeUserConfig(t, configPath, writeJSONConfig(t, map[string]any{
		"scopes": map[string]any{
			"workers": map[string]any{
				"repository": filepath.Join(dir, "tickets"),
				"work_tags":  []string{"windows"},
			},
		},
	}))
	if out, code := runCLI(t, "--config", configPath, "--scope", "workers", "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	ctx := &commandContext{cwd: dir, globalOpts: globalOpts{
		configPath: configPath, configExplicit: true, scopeName: "workers", scopeExplicit: true,
	}}
	if err := ctx.check(); err != nil {
		t.Fatalf("load scope: %v", err)
	}
	options := watchOptions{Tags: []string{"urgent"}, WithoutTags: []string{"frontend"}}
	if err := applyWatchTagFilters(ctx, &options); err != nil {
		t.Fatalf("apply filters: %v", err)
	}
	if got, want := options.Tags, []string{"networking", "urgent", "windows"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("watch work tags=%v, want %v", got, want)
	}
	if got, want := options.WithoutTags, []string{"frontend"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("watch without tags=%v, want %v", got, want)
	}
}

func TestWatchRejectsInvalidTagFilters(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	for _, args := range [][]string{{"watch", "--tag", "Bad Tag"}, {"watch", "--without-tag", "Bad Tag"}} {
		out, code := runCLI(t, args...)
		if code == 0 || errCode(t, out) != "invalid_argument" {
			t.Fatalf("args=%v accepted: exit=%d out=%q", args, code, out)
		}
		if !strings.Contains(out, "must match [a-z0-9]") {
			t.Fatalf("args=%v missing standard tag validation: %q", args, out)
		}
	}
}

func TestWatchActorAttributionStaysConservativeAcrossSnapshots(t *testing.T) {
	withHistory := watchSnapshot{
		ID: "20260921-00001", State: "open", WorkLog: "- 2026-09-20T00:00:00Z luna: created",
	}
	created := deriveWatchEvents(nil, map[string]watchSnapshot{withHistory.ID: withHistory})
	if len(created) != 1 || created[0].Actor != nil {
		t.Fatalf("created actor=%+v, want unknown", created)
	}
	deleted := deriveWatchEvents(map[string]watchSnapshot{withHistory.ID: withHistory}, nil)
	if len(deleted) != 1 || deleted[0].Actor != nil {
		t.Fatalf("deleted actor=%+v, want unknown", deleted)
	}

	old := watchSnapshot{ID: withHistory.ID, State: "open", Assignee: "luna"}
	submitted := old
	submitted.State = "review"
	events := diffWatchEvent(old, submitted)
	if len(events) != 1 || events[0].Event != "submitted" || events[0].Actor == nil || *events[0].Actor != "luna" {
		t.Fatalf("submitted events=%+v", events)
	}
	approved := watchSnapshot{ID: withHistory.ID, State: "review", Assignee: "luna"}
	signoff := approved
	signoff.State = "signoff"
	events = diffWatchEvent(approved, signoff)
	if len(events) != 1 || events[0].Event != "approved" || events[0].Actor == nil || *events[0].Actor != "luna" {
		t.Fatalf("approved events=%+v", events)
	}
}

func TestWatchJSONRenderingIsOneFramedSafeEvent(t *testing.T) {
	var output bytes.Buffer
	event := watchEvent{
		Time: time.Date(2026, time.September, 21, 3, 4, 5, 0, time.UTC), Ticket: "20260921-00001",
		Title: "\x1b[31munsafe\nname", Event: "edited", Message: "changed",
	}
	if err := emitWatchEvent(&commandContext{stdout: &output, globalOpts: globalOpts{json: true}}, event); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "\n") != 1 {
		t.Fatalf("JSON output framing=%q", output.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(output.String()), &decoded); err != nil {
		t.Fatalf("JSON output=%q: %v", output.String(), err)
	}
	if decoded["title"] != event.Title || decoded["actor"] != nil {
		t.Fatalf("decoded event=%v", decoded)
	}
}

func TestWatchHumanRenderingSanitizesTitleAndMessage(t *testing.T) {
	var output bytes.Buffer
	event := watchEvent{Time: time.Date(2026, time.September, 21, 3, 4, 5, 0, time.UTC), Ticket: "20260921-00001", Title: "\x1b[31munsafe\nname", Event: "edited", Message: "line\r\nmessage"}
	if err := emitWatchEvent(&commandContext{stdout: &output}, event); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(output.String(), "\x1b\r") || strings.Count(output.String(), "\n") != 1 {
		t.Fatalf("unsafe control sequence reached human output: %q", output.String())
	}
}

func TestWatchStartsFromNowAndDoesNotWriteMarkers(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	marker := dir + "/tickets/.local/current"
	before, err := readOptionalWatchFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	output := &synchronizedWatchBuffer{signal: make(chan struct{})}
	done := make(chan struct{})
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}, liveWriter: output}
	watchDone := make(chan error, 1)
	go func() { watchDone <- runWatchLoop(ctx, watchOptions{}, done) }()
	time.Sleep(50 * time.Millisecond)
	mustCLI(t, "create", "Watched ticket", "Emit a created event.")
	select {
	case <-output.signal:
	case <-time.After(2 * time.Second):
		close(done)
		t.Fatalf("watch emitted no event")
	}
	close(done)
	if err := <-watchDone; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "created") {
		t.Fatalf("watch output=%q", output.String())
	}
	after, err := readOptionalWatchFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("watch changed current marker: before=%q after=%q", before, after)
	}
}

func TestWatchDetectsMetadataChangeWithoutChangeMarker(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Before", "Watch an external metadata-visible edit."))["id"].(string)
	taskPath := filepath.Join(dir, "tickets", id, "TASK.md")
	markerPath := filepath.Join(dir, "tickets", ".local", "change")
	markerBefore, err := readOptionalWatchFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	output := &synchronizedWatchBuffer{signal: make(chan struct{})}
	done := make(chan struct{})
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}, liveWriter: output}
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- runWatchLoopWith(ctx, watchOptions{}, done,
			watchLoopTiming{marker: 10 * time.Millisecond, fingerprint: 40 * time.Millisecond}, watchLoopOps{
				changeState: store.ChangeState, fingerprint: watchMetadataFingerprint, snapshot: watchSnapshotTickets,
			})
	}()
	time.Sleep(50 * time.Millisecond)

	contents, err := os.ReadFile(taskPath)
	if err != nil {
		close(done)
		<-watchDone
		t.Fatal(err)
	}
	updated := strings.Replace(string(contents), "# Before", "# After!", 1)
	if updated == string(contents) {
		close(done)
		<-watchDone
		t.Fatal("could not find ticket title to update")
	}
	if err := os.WriteFile(taskPath, []byte(updated), 0o600); err != nil {
		close(done)
		<-watchDone
		t.Fatal(err)
	}
	stamp := time.Now().Add(time.Second)
	if err := os.Chtimes(taskPath, stamp, stamp); err != nil {
		close(done)
		<-watchDone
		t.Fatal(err)
	}
	select {
	case <-output.signal:
	case <-time.After(2 * time.Second):
		close(done)
		<-watchDone
		t.Fatalf("watch emitted no event for metadata-visible edit; output=%q", output.String())
	}
	close(done)
	if err := <-watchDone; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "edited") {
		t.Fatalf("watch output=%q, want edited event", output.String())
	}
	markerAfter, err := readOptionalWatchFile(markerPath)
	if err != nil || markerBefore != markerAfter {
		t.Fatalf("direct edit changed marker: before=%q after=%q err=%v", markerBefore, markerAfter, err)
	}
}

func TestWatchIdlePollsDoNotRefreshSnapshots(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	var snapshots, markerReads, fingerprintReads atomic.Int32
	fingerprintObserved := make(chan struct{})
	var initialFingerprint, periodicFingerprint [32]byte
	ops := watchLoopOps{
		changeState: func(root string) (string, error) {
			markerReads.Add(1)
			return store.ChangeState(root)
		},
		fingerprint: func(root string) ([32]byte, error) {
			fingerprint, err := watchMetadataFingerprint(root)
			if err != nil {
				return [32]byte{}, err
			}
			switch fingerprintReads.Add(1) {
			case 1:
				initialFingerprint = fingerprint
			case 2:
				periodicFingerprint = fingerprint
				close(fingerprintObserved)
			}
			return fingerprint, nil
		},
		snapshot: func(cwd, root string) (map[string]watchSnapshot, error) {
			snapshots.Add(1)
			return watchSnapshotTickets(cwd, root)
		},
	}
	done := make(chan struct{})
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}}
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- runWatchLoopWith(ctx, watchOptions{}, done,
			watchLoopTiming{marker: 5 * time.Millisecond, fingerprint: 25 * time.Millisecond}, ops)
	}()
	select {
	case <-fingerprintObserved:
	case <-time.After(time.Second):
		close(done)
		<-watchDone
		t.Fatal("watch did not perform a post-startup fingerprint observation")
	}
	close(done)
	if err := <-watchDone; err != nil {
		t.Fatal(err)
	}
	if got := snapshots.Load(); got != 1 {
		t.Fatalf("idle loop built %d snapshots, want startup snapshot only", got)
	}
	if markerReads.Load() <= fingerprintReads.Load() {
		t.Fatalf("marker reads=%d fingerprint reads=%d, want fixed marker polling between fingerprint observations", markerReads.Load(), fingerprintReads.Load())
	}
	if got := fingerprintReads.Load(); got < 2 {
		t.Fatalf("fingerprint reads=%d, want startup and at least one periodic observation", got)
	}
	if initialFingerprint != periodicFingerprint {
		t.Fatalf("unchanged metadata fingerprint changed: %x != %x", initialFingerprint, periodicFingerprint)
	}
	if ctx.stdout.Len() != 0 {
		t.Fatalf("idle watch emitted output: %q", ctx.stdout.String())
	}
}

func TestWatchDetectsOutOfBandTicketDirectoryMove(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	sourceID := exactlyOneJSONObject(t, mustCLI(t, "create", "Template", "Set up a canonical active ticket for an out-of-band move."))["id"].(string)
	root := filepath.Join(dir, "tickets")
	markerPath := filepath.Join(root, ".local", "change")
	markerBefore, err := readOptionalWatchFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	var snapshots atomic.Int32
	startupSnapshot := make(chan struct{})
	var startupOnce sync.Once
	ops := watchLoopOps{
		changeState: store.ChangeState,
		fingerprint: watchMetadataFingerprint,
		snapshot: func(cwd, root string) (map[string]watchSnapshot, error) {
			result, err := watchSnapshotTickets(cwd, root)
			if snapshots.Add(1) == 1 {
				startupOnce.Do(func() { close(startupSnapshot) })
			}
			return result, err
		},
	}
	output := &synchronizedWatchBuffer{signal: make(chan struct{})}
	done := make(chan struct{})
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}, liveWriter: output, globalOpts: globalOpts{json: true}}
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- runWatchLoopWith(ctx, watchOptions{}, done,
			watchLoopTiming{marker: 100 * time.Millisecond, fingerprint: 20 * time.Millisecond}, ops)
	}()
	select {
	case <-startupSnapshot:
	case <-time.After(time.Second):
		close(done)
		<-watchDone
		t.Fatal("watch did not establish startup snapshot")
	}

	addedID := "20990101-00001"
	if err := os.Rename(filepath.Join(root, sourceID), filepath.Join(root, addedID)); err != nil {
		close(done)
		<-watchDone
		t.Fatal(err)
	}
	select {
	case <-output.signal:
	case <-time.After(time.Second):
		close(done)
		<-watchDone
		t.Fatalf("watch emitted no event for out-of-band active ticket move; output=%q", output.String())
	}
	close(done)
	if err := <-watchDone; err != nil {
		t.Fatalf("watch stopped during structural refresh: %v", err)
	}
	if snapshots.Load() != 2 {
		t.Fatalf("snapshot count=%d, want startup plus one structural refresh", snapshots.Load())
	}
	markerAfter, err := readOptionalWatchFile(markerPath)
	if err != nil || markerBefore != markerAfter {
		t.Fatalf("direct directory move changed marker: before=%q after=%q err=%v", markerBefore, markerAfter, err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("structural move emitted %d events, want one created and one deleted event: %q", len(lines), output.String())
	}
	events := make(map[string]string, len(lines))
	for _, line := range lines {
		var event struct {
			Event  string `json:"event"`
			Ticket string `json:"ticket"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode watch event %q: %v", line, err)
		}
		events[event.Event] = event.Ticket
	}
	if events["created"] != addedID || events["deleted"] != sourceID || len(events) != 2 {
		t.Fatalf("watch events=%v, want created %s and deleted %s only", events, addedID, sourceID)
	}
}

func TestWatchMetadataOnlyChangeRefreshesWithEmptyDiff(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	var snapshots, fingerprintReads atomic.Int32
	secondSnapshot := make(chan struct{})
	var secondOnce sync.Once
	ops := watchLoopOps{
		changeState: store.ChangeState,
		fingerprint: func(string) ([32]byte, error) {
			if fingerprintReads.Add(1) == 1 {
				return [32]byte{1}, nil
			}
			return [32]byte{2}, nil
		},
		snapshot: func(cwd, root string) (map[string]watchSnapshot, error) {
			if snapshots.Add(1) == 2 {
				secondOnce.Do(func() { close(secondSnapshot) })
			}
			return watchSnapshotTickets(cwd, root)
		},
	}
	output := &bytes.Buffer{}
	done := make(chan struct{})
	ctx := &commandContext{cwd: dir, stdout: output}
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- runWatchLoopWith(ctx, watchOptions{}, done,
			watchLoopTiming{marker: 100 * time.Millisecond, fingerprint: 20 * time.Millisecond}, ops)
	}()
	select {
	case <-secondSnapshot:
	case <-time.After(time.Second):
		close(done)
		<-watchDone
		t.Fatal("metadata-only change did not refresh snapshot")
	}
	close(done)
	if err := <-watchDone; err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("empty snapshot diff emitted output=%q", output.String())
	}
	if snapshots.Load() != 2 {
		t.Fatalf("snapshot count=%d, want one metadata refresh", snapshots.Load())
	}
}

func TestWatchCoalescesMarkerAndFingerprintChanges(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	var snapshots atomic.Int32
	startupSnapshot := make(chan struct{})
	var startupOnce sync.Once
	ops := watchLoopOps{
		changeState: store.ChangeState,
		fingerprint: watchMetadataFingerprint,
		snapshot: func(cwd, root string) (map[string]watchSnapshot, error) {
			count := snapshots.Add(1)
			if count == 1 {
				startupOnce.Do(func() { close(startupSnapshot) })
			}
			return watchSnapshotTickets(cwd, root)
		},
	}
	output := &synchronizedWatchBuffer{signal: make(chan struct{})}
	done := make(chan struct{})
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}, liveWriter: output}
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- runWatchLoopWith(ctx, watchOptions{}, done,
			watchLoopTiming{marker: 8 * time.Millisecond, fingerprint: 20 * time.Millisecond}, ops)
	}()
	select {
	case <-startupSnapshot:
	case <-time.After(time.Second):
		close(done)
		<-watchDone
		t.Fatal("watch did not establish startup snapshot")
	}
	mustCLI(t, "create", "Coalesced", "Change marker and metadata in one mutation.")
	select {
	case <-output.signal:
	case <-time.After(time.Second):
		close(done)
		<-watchDone
		t.Fatal("watch emitted no event for coalesced changes")
	}
	time.Sleep(60 * time.Millisecond)
	close(done)
	if err := <-watchDone; err != nil {
		t.Fatal(err)
	}
	if got := snapshots.Load(); got != 2 {
		t.Fatalf("snapshots=%d, want startup plus one coalesced refresh", got)
	}
}

func TestWatchFingerprintFailureDoesNotBlockMarkerRefresh(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	var snapshots, fingerprintReads atomic.Int32
	startupSnapshot := make(chan struct{})
	var startupOnce sync.Once
	ops := watchLoopOps{
		changeState: store.ChangeState,
		fingerprint: func(root string) ([32]byte, error) {
			if fingerprintReads.Add(1) > 1 {
				return [32]byte{}, os.ErrPermission
			}
			return watchMetadataFingerprint(root)
		},
		snapshot: func(cwd, root string) (map[string]watchSnapshot, error) {
			if snapshots.Add(1) == 1 {
				startupOnce.Do(func() { close(startupSnapshot) })
			}
			return watchSnapshotTickets(cwd, root)
		},
	}
	output := &synchronizedWatchBuffer{signal: make(chan struct{})}
	done := make(chan struct{})
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}, liveWriter: output}
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- runWatchLoopWith(ctx, watchOptions{}, done,
			watchLoopTiming{marker: 8 * time.Millisecond, fingerprint: 30 * time.Millisecond}, ops)
	}()
	select {
	case <-startupSnapshot:
	case <-time.After(time.Second):
		close(done)
		<-watchDone
		t.Fatal("watch did not establish startup snapshot")
	}
	mustCLI(t, "create", "Marker wins", "Marker refresh continues when fingerprint fails.")
	select {
	case <-output.signal:
	case <-time.After(time.Second):
		close(done)
		<-watchDone
		t.Fatal("fingerprint failure blocked marker-triggered event")
	}
	close(done)
	if err := <-watchDone; err != nil {
		t.Fatalf("watch stopped after fingerprint failure: %v", err)
	}
	if snapshots.Load() != 2 {
		t.Fatalf("snapshot count=%d, want marker refresh despite fingerprint failure", snapshots.Load())
	}
}

func TestWatchStartsWhenInitialFingerprintFails(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	var snapshots atomic.Int32
	ops := watchLoopOps{
		changeState: store.ChangeState,
		fingerprint: func(string) ([32]byte, error) { return [32]byte{}, os.ErrPermission },
		snapshot: func(cwd, root string) (map[string]watchSnapshot, error) {
			snapshots.Add(1)
			return watchSnapshotTickets(cwd, root)
		},
	}
	done := make(chan struct{})
	close(done)
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}}
	if err := runWatchLoopWith(ctx, watchOptions{}, done,
		watchLoopTiming{marker: 10 * time.Millisecond, fingerprint: 20 * time.Millisecond}, ops); err != nil {
		t.Fatalf("initial fingerprint failure stopped watch: %v", err)
	}
	if snapshots.Load() != 1 {
		t.Fatalf("initial snapshot count=%d, want 1", snapshots.Load())
	}
}

func TestWatchRecoversUnknownFingerprintWithAuthoritativeDiff(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Before", "Observe later recovery from an unknown fingerprint."))["id"].(string)
	taskPath := filepath.Join(dir, "tickets", id, "TASK.md")
	var snapshots, fingerprintReads atomic.Int32
	startupSnapshot := make(chan struct{})
	var startupOnce sync.Once
	ops := watchLoopOps{
		changeState: store.ChangeState,
		fingerprint: func(root string) ([32]byte, error) {
			if fingerprintReads.Add(1) == 1 {
				return [32]byte{}, os.ErrPermission
			}
			return watchMetadataFingerprint(root)
		},
		snapshot: func(cwd, root string) (map[string]watchSnapshot, error) {
			result, err := watchSnapshotTickets(cwd, root)
			if snapshots.Add(1) == 1 {
				startupOnce.Do(func() { close(startupSnapshot) })
			}
			return result, err
		},
	}
	output := &synchronizedWatchBuffer{signal: make(chan struct{})}
	done := make(chan struct{})
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}, liveWriter: output}
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- runWatchLoopWith(ctx, watchOptions{}, done,
			watchLoopTiming{marker: 100 * time.Millisecond, fingerprint: 20 * time.Millisecond}, ops)
	}()
	select {
	case <-startupSnapshot:
	case <-time.After(time.Second):
		close(done)
		<-watchDone
		t.Fatal("watch did not establish initial snapshot")
	}
	if output.String() != "" {
		close(done)
		<-watchDone
		t.Fatalf("startup emitted a synthetic event: %q", output.String())
	}
	contents, err := os.ReadFile(taskPath)
	if err != nil {
		close(done)
		<-watchDone
		t.Fatal(err)
	}
	updated := strings.Replace(string(contents), "# Before", "# After!", 1)
	if updated == string(contents) {
		close(done)
		<-watchDone
		t.Fatal("could not find ticket title to update")
	}
	if err := os.WriteFile(taskPath, []byte(updated), 0o600); err != nil {
		close(done)
		<-watchDone
		t.Fatal(err)
	}
	stamp := time.Now().Add(time.Second)
	if err := os.Chtimes(taskPath, stamp, stamp); err != nil {
		close(done)
		<-watchDone
		t.Fatal(err)
	}
	select {
	case <-output.signal:
	case <-time.After(time.Second):
		close(done)
		<-watchDone
		t.Fatal("recovered fingerprint hid out-of-band edit")
	}
	close(done)
	if err := <-watchDone; err != nil {
		t.Fatalf("watch stopped during fingerprint recovery: %v", err)
	}
	if !strings.Contains(output.String(), "edited") || snapshots.Load() < 2 {
		t.Fatalf("recovery output=%q snapshots=%d", output.String(), snapshots.Load())
	}
}

func TestWatchUsesPreSnapshotFingerprintCandidate(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Before", "Observe fingerprint movement during a refresh."))["id"].(string)
	taskPath := filepath.Join(dir, "tickets", id, "TASK.md")
	var snapshots, fingerprints atomic.Int32
	thirdSnapshot := make(chan struct{})
	var thirdOnce sync.Once
	snapshotErr := make(chan error, 1)
	reportSnapshotErr := func(err error) {
		if err == nil {
			return
		}
		select {
		case snapshotErr <- err:
		default:
		}
	}
	ops := watchLoopOps{
		changeState: store.ChangeState,
		fingerprint: func(string) ([32]byte, error) {
			n := fingerprints.Add(1)
			if n == 1 {
				return [32]byte{1}, nil
			}
			if n == 2 {
				return [32]byte{2}, nil
			}
			return [32]byte{3}, nil
		},
		snapshot: func(cwd, root string) (map[string]watchSnapshot, error) {
			result, err := watchSnapshotTickets(cwd, root)
			n := snapshots.Add(1)
			if n == 2 && err == nil {
				contents, readErr := os.ReadFile(taskPath)
				if readErr != nil {
					reportSnapshotErr(readErr)
				} else {
					updated := strings.Replace(string(contents), "# Before", "# After!", 1)
					if updated == string(contents) {
						reportSnapshotErr(fmt.Errorf("could not find ticket title to update"))
					} else if writeErr := os.WriteFile(taskPath, []byte(updated), 0o600); writeErr != nil {
						reportSnapshotErr(writeErr)
					} else {
						stamp := time.Now().Add(time.Second)
						reportSnapshotErr(os.Chtimes(taskPath, stamp, stamp))
					}
				}
			}
			if n == 3 {
				thirdOnce.Do(func() { close(thirdSnapshot) })
			}
			return result, err
		},
	}
	done := make(chan struct{})
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}}
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- runWatchLoopWith(ctx, watchOptions{}, done,
			watchLoopTiming{marker: 100 * time.Millisecond, fingerprint: 20 * time.Millisecond}, ops)
	}()
	select {
	case <-thirdSnapshot:
	case <-time.After(time.Second):
		close(done)
		<-watchDone
		t.Fatal("metadata change during refresh was hidden by a post-snapshot baseline")
	}
	close(done)
	if err := <-watchDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-snapshotErr:
		t.Fatalf("snapshot race mutation: %v", err)
	default:
	}
	if snapshots.Load() != 3 {
		t.Fatalf("snapshot count=%d, want initial plus two fingerprint refreshes", snapshots.Load())
	}
}

func TestWatchRetainsMarkerTransitionsAcrossRefresh(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	var marker atomic.Value
	marker.Store("marker-0")
	var snapshots, markerReads atomic.Int32
	startupReady := make(chan struct{})
	var startupOnce sync.Once
	thirdSnapshot := make(chan struct{})
	var thirdOnce sync.Once
	ops := watchLoopOps{
		changeState: func(string) (string, error) {
			value := marker.Load().(string)
			if markerReads.Add(1) == 2 {
				startupOnce.Do(func() { close(startupReady) })
			}
			return value, nil
		},
		fingerprint: func(string) ([32]byte, error) { return [32]byte{1}, nil },
		snapshot: func(string, string) (map[string]watchSnapshot, error) {
			n := snapshots.Add(1)
			if n == 2 {
				marker.Store("marker-2")
			}
			if n == 3 {
				thirdOnce.Do(func() { close(thirdSnapshot) })
			}
			return map[string]watchSnapshot{}, nil
		},
	}
	done := make(chan struct{})
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}}
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- runWatchLoopWith(ctx, watchOptions{}, done,
			watchLoopTiming{marker: 8 * time.Millisecond, fingerprint: 100 * time.Millisecond}, ops)
	}()
	select {
	case <-startupReady:
	case <-time.After(time.Second):
		close(done)
		<-watchDone
		t.Fatal("watch did not finish startup observation")
	}
	marker.Store("marker-1")
	select {
	case <-thirdSnapshot:
	case <-time.After(time.Second):
		close(done)
		<-watchDone
		t.Fatal("marker transition during refresh was silently advanced")
	}
	close(done)
	if err := <-watchDone; err != nil {
		t.Fatal(err)
	}
	if snapshots.Load() != 3 {
		t.Fatalf("snapshot count=%d, want both marker transitions refreshed", snapshots.Load())
	}
}

type synchronizedWatchBuffer struct {
	mu     sync.Mutex
	b      bytes.Buffer
	signal chan struct{}
	once   sync.Once
}

func (w *synchronizedWatchBuffer) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.b.Write(data)
	w.once.Do(func() { close(w.signal) })
	return n, err
}

func (w *synchronizedWatchBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func TestWatchCancellationBeforeWaitIsClean(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	done := make(chan struct{})
	close(done)
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}}
	started := time.Now()
	if err := runWatchLoop(ctx, watchOptions{}, done); err != nil {
		t.Fatalf("watch cancellation: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= watchPollInterval {
		t.Fatalf("watch cancellation waited for poll interval: %s", elapsed)
	}
}

func TestWatchIsUnavailableInInteractiveSessions(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	for _, args := range [][]string{{"watch"}, {"-j", "watch"}} {
		session := newSessionExecutor()
		if args[0] == "-j" {
			session.globalOpts.machineTransport = true
		}
		var output bytes.Buffer
		err := session.dispatch(args, &output)
		if err == nil || !strings.Contains(err.Error(), "unavailable in interactive sessions") {
			t.Fatalf("watch session args=%v error=%v output=%q", args, err, output.String())
		}
	}
}

func TestWatchUsesConfiguredScopeRoot(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	configPath := filepath.Join(dir, "scopes.json")
	repo := filepath.Join(dir, "named-tickets")
	writeUserConfig(t, configPath, writeJSONConfig(t, map[string]any{
		"scopes": map[string]any{"named": map[string]any{"repository": repo}},
	}))
	if out, code := runCLI(t, "--config", configPath, "--scope", "named", "init"); code != 0 {
		t.Fatalf("scoped init: exit=%d out=%q", code, out)
	}
	done := make(chan struct{})
	close(done)
	ctx := &commandContext{
		cwd: dir, stdout: &bytes.Buffer{},
		globalOpts: globalOpts{configPath: configPath, configExplicit: true, scopeName: "named", scopeExplicit: true},
	}
	if err := runWatchLoop(ctx, watchOptions{}, done); err != nil {
		t.Fatalf("scoped watch: %v", err)
	}
}

func TestWatchSynchronizesBeforeBackfillingLegacyRepositoryID(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	configPath := filepath.Join(dir, "tickets", "config.json")
	legacy := []byte("{\"format_version\":1}\n")
	if err := os.WriteFile(configPath, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "scm.log")
	installFakeSCM(t, filepath.Join(dir, "bin"), "scm-lifecycle")
	t.Setenv("SCM_LOG", logPath)
	t.Setenv("TICKET_SCM", "git")
	t.Setenv("TICKET_SCM_MODE", "sync")
	done := make(chan struct{})
	close(done)
	ctx := &commandContext{cwd: dir, stdout: &bytes.Buffer{}}
	if err := runWatchLoop(ctx, watchOptions{}, done); err != nil {
		t.Fatalf("watch startup: %v", err)
	}
	cfg, err := store.LoadConfig(filepath.Join(dir, "tickets"))
	if err != nil || cfg.ID == "" {
		t.Fatalf("watch did not backfill repository ID: config=%+v err=%v", cfg, err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	if strings.Index(log, "pull --ff-only") < 0 || strings.Index(log, "pull --ff-only") > strings.Index(log, "add -- config.json") {
		t.Fatalf("watch backfilled before SCM update: %q", log)
	}
}

func readOptionalWatchFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}
