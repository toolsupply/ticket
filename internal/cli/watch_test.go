package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

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

func readOptionalWatchFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}
