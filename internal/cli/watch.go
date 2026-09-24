package cli

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/store"
)

const watchPollInterval = 250 * time.Millisecond

type watchEvent struct {
	Time    time.Time `json:"-"`
	Actor   *string   `json:"actor"`
	Ticket  string    `json:"ticket"`
	Title   string    `json:"title"`
	Event   string    `json:"event"`
	Message string    `json:"message,omitempty"`
	State   string    `json:"state"`
	From    string    `json:"from,omitempty"`
	To      string    `json:"to,omitempty"`
	tags    []string
	parent  string
}

type watchSnapshot struct {
	ID        string
	Title     string
	State     string
	Assignee  string
	Priority  int
	Tags      []string
	Parent    string
	DependsOn []string
	Objective string
	Handoff   string
	WorkLog   string
	FileBytes []byte
}

type watchOptions struct {
	Actor       string
	Ticket      string
	State       string
	Tags        []string
	WithoutTags []string
	Parent      string
	Events      []string
}

func cmdWatch(ctx *commandContext, args []string) error {
	p := ctx.newParser()
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var options watchOptions
	p.str("actor", &options.Actor)
	p.str("ticket", &options.Ticket)
	p.str("state", &options.State)
	p.repeat("tag", &options.Tags)
	p.repeat("without-tag", &options.WithoutTags)
	p.str("parent", &options.Parent)
	p.repeat("event", &options.Events)
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "watch")
	}
	if len(p.positionals) > 0 {
		return contract.NewError(contract.ErrInvalidArgument, "Command watch accepts no positional arguments.", nil)
	}
	if ctx.session != nil {
		return contract.NewError(contract.ErrInvalidArgument,
			"Command watch is unavailable in interactive sessions; run it as a standalone command.", nil)
	}
	if options.State != "" && !watchStateFilter(options.State) {
		return contract.NewError(contract.ErrInvalidArgument,
			"Flag --state expects open, hold, review, signoff, closed, rejected, or all.", nil)
	}
	if options.State != "" && options.State != "all" {
		normalized, _ := domain.NormalizeLifecycleState(options.State)
		options.State = normalized
	}
	if err := ctx.check(); err != nil {
		return err
	}
	if err := applyWatchTagFilters(ctx, &options); err != nil {
		return err
	}
	done := ctx.done
	var stop func()
	if done == nil {
		interrupts := make(chan os.Signal, 1)
		signal.Notify(interrupts, os.Interrupt)
		localDone := make(chan struct{})
		var doneOnce sync.Once
		done = localDone
		stop = func() { signal.Stop(interrupts); doneOnce.Do(func() { close(localDone) }) }
		go func() {
			select {
			case <-interrupts:
				doneOnce.Do(func() { close(localDone) })
			case <-localDone:
			}
		}()
	}
	if stop != nil {
		defer stop()
	}
	if !ctx.json {
		watchWrite(ctx, "Watching ticket activity. Press Ctrl-C to stop.\n")
	}
	return runWatchLoop(ctx, options, done)
}

func applyWatchTagFilters(ctx *commandContext, options *watchOptions) error {
	options.Tags = ctx.workTags(options.Tags)
	var err error
	if options.Tags, err = domain.NormalizeTags(options.Tags); err != nil {
		return err
	}
	if options.WithoutTags, err = domain.NormalizeTags(options.WithoutTags); err != nil {
		return err
	}
	return nil
}

func runWatchLoop(ctx *commandContext, options watchOptions, done <-chan struct{}) error {
	root, err := ctx.discoverRoot()
	if err != nil {
		return err
	}
	marker, err := store.ChangeState(root)
	if err != nil {
		return watchIOError(err)
	}
	before, err := watchSnapshotTickets(ctx.cwd, root)
	if err != nil {
		return err
	}
	// A mutation racing the initial read must not be mistaken for startup
	// history. Refresh once when the marker changed during the snapshot.
	if latest, readErr := store.ChangeState(root); readErr != nil {
		return watchIOError(readErr)
	} else if latest != marker {
		before, err = watchSnapshotTickets(ctx.cwd, root)
		if err != nil {
			return err
		}
		marker = latest
	}
	for {
		select {
		case <-done:
			return nil
		case <-time.After(watchPollInterval):
		}
		current, err := store.ChangeState(root)
		if err != nil {
			return watchIOError(err)
		}
		if current == marker {
			continue
		}
		after, err := watchSnapshotTickets(ctx.cwd, root)
		if err != nil {
			return err
		}
		for _, event := range deriveWatchEvents(before, after) {
			if !watchEventMatches(event, options) {
				continue
			}
			if err := emitWatchEvent(ctx, event); err != nil {
				return err
			}
		}
		before, marker = after, current
	}
}

func watchIOError(err error) error {
	return contract.NewError(contract.ErrIOError, "Cannot inspect ticket change marker: "+err.Error(), nil)
}

func watchSnapshotTickets(cwd, root string) (map[string]watchSnapshot, error) {
	st, err := store.Open(cwd, store.OpenOptions{Root: root})
	if err != nil {
		return nil, err
	}
	defer st.Close()
	result, err := domain.List(st, domain.ListOptions{States: []string{"all"}, Unlimited: true})
	if err != nil {
		return nil, err
	}
	snapshots := make(map[string]watchSnapshot, len(result.Items))
	for _, item := range result.Items {
		ticket, err := domain.ReadTicket(st, item.ID)
		if err != nil {
			return nil, err
		}
		snapshots[item.ID] = watchSnapshot{
			ID: item.ID, Title: ticket.Title, State: ticket.State,
			Assignee: ticket.Assignee, Priority: ticket.Priority,
			Tags: append([]string(nil), ticket.Tags...), Parent: ticket.Parent,
			DependsOn: append([]string(nil), ticket.DependsOn...),
			Objective: ticket.SectionText("objective"),
			Handoff:   ticket.SectionText("handoff"), WorkLog: ticket.SectionText("work_log"),
			FileBytes: append([]byte(nil), ticket.FileBytes...),
		}
	}
	return snapshots, nil
}

func deriveWatchEvents(before, after map[string]watchSnapshot) []watchEvent {
	ids := make([]string, 0, len(before)+len(after))
	seen := make(map[string]bool, len(before)+len(after))
	for id := range before {
		seen[id] = true
		ids = append(ids, id)
	}
	for id := range after {
		if !seen[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var events []watchEvent
	for _, id := range ids {
		old, hadOld := before[id]
		current, hasCurrent := after[id]
		switch {
		case !hadOld && hasCurrent:
			event := newWatchEvent(current, "created", "created")
			event.Actor = watchActor(nil, &current, event.Event)
			events = append(events, event)
		case hadOld && !hasCurrent:
			event := newWatchEvent(old, "deleted", "deleted")
			event.Actor = watchActor(&old, nil, event.Event)
			events = append(events, event)
		default:
			events = append(events, diffWatchEvent(old, current)...)
		}
	}
	return events
}

func newWatchEvent(ticket watchSnapshot, kind, message string) watchEvent {
	return watchEvent{
		Time: time.Now(), Ticket: ticket.ID, Title: ticket.Title, Event: kind,
		Message: message, State: ticket.State, tags: append([]string(nil), ticket.Tags...), parent: ticket.Parent,
	}
}

func watchStateFilter(state string) bool {
	if state == "all" {
		return true
	}
	_, ok := domain.NormalizeLifecycleState(state)
	return ok
}

func watchEventMatches(event watchEvent, options watchOptions) bool {
	if options.Actor != "" {
		if options.Actor == "-" {
			if event.Actor != nil {
				return false
			}
		} else if event.Actor == nil || *event.Actor != options.Actor {
			return false
		}
	}
	if options.Ticket != "" && event.Ticket != options.Ticket {
		return false
	}
	if options.State != "" && options.State != "all" && event.State != options.State {
		return false
	}
	if options.Parent != "" && event.parent != options.Parent {
		return false
	}
	for _, tag := range options.Tags {
		if !containsString(event.tags, tag) {
			return false
		}
	}
	for _, tag := range options.WithoutTags {
		if containsString(event.tags, tag) {
			return false
		}
	}
	if len(options.Events) > 0 {
		matched := false
		for _, kind := range options.Events {
			if event.Event == kind {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func diffWatchEvent(old, current watchSnapshot) []watchEvent {
	var events []watchEvent
	if old.State != current.State {
		kind, message := stateWatchEvent(old.State, current.State)
		event := newWatchEvent(current, kind, message)
		event.From, event.To = old.State, current.State
		events = append(events, event)
	}
	if old.Assignee != current.Assignee && old.State == current.State {
		kind, message := "edited", "assignee changed"
		if old.Assignee == "" {
			kind, message = "claimed", "claimed"
		} else if current.Assignee == "" {
			kind, message = "released", "released"
		}
		events = append(events, newWatchEvent(current, kind, message))
	}
	if old.Parent != current.Parent {
		kind, message := "child-added", "parent changed"
		if current.Parent == "" {
			kind, message = "child-removed", "parent removed"
		}
		events = append(events, newWatchEvent(current, kind, message))
	}
	if !sameStrings(old.DependsOn, current.DependsOn) {
		events = append(events, newWatchEvent(current, "dependency-changed", "dependencies changed"))
	}
	if old.Handoff != current.Handoff {
		events = append(events, newWatchEvent(current, "handoff-updated", "handoff updated"))
	}
	if old.WorkLog != current.WorkLog {
		events = append(events, newWatchEvent(current, "work-log-updated", "work log updated"))
	}
	if old.Title != current.Title || old.Priority != current.Priority || !sameStrings(old.Tags, current.Tags) || old.Objective != current.Objective {
		events = append(events, newWatchEvent(current, "edited", "edited"))
	}
	if len(events) == 0 && string(old.FileBytes) != string(current.FileBytes) {
		events = append(events, newWatchEvent(current, "updated", "updated"))
	}
	for i := range events {
		events[i].Actor = watchActor(&old, &current, events[i].Event)
	}
	return events
}

func watchActor(old, current *watchSnapshot, event string) *string {
	if old == nil || current == nil {
		return nil
	}
	if event == "claimed" && current != nil && current.Assignee != "" {
		actor := current.Assignee
		return &actor
	}
	if event == "released" && old != nil && old.Assignee != "" {
		actor := old.Assignee
		return &actor
	}
	if actor := appendedWorkLogActor(old.WorkLog, current.WorkLog); actor != "" {
		return &actor
	}
	if old.State != current.State && old.Assignee != "" {
		actor := old.Assignee
		return &actor
	}
	return nil
}

func appendedWorkLogActor(old, current string) string {
	if old == "" || !strings.HasPrefix(current, old) {
		return ""
	}
	return firstWorkLogActor(strings.TrimPrefix(current, old))
}

func firstWorkLogActor(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "- ")))
		if len(fields) < 2 || !strings.HasSuffix(fields[1], ":") {
			continue
		}
		actor := strings.TrimSuffix(fields[1], ":")
		if actor != "" {
			return actor
		}
	}
	return ""
}

func stateWatchEvent(from, to string) (string, string) {
	switch {
	case from == "open" && to == "review":
		return "submitted", "submitted for review"
	case from == "review" && to == "signoff":
		return "approved", "approved"
	case from == "signoff" && to == "closed":
		return "closed", "closed"
	case to == "rejected":
		return "rejected", "rejected"
	case to == "open":
		return "reopened", "reopened"
	case to == "hold":
		return "held", "put on hold"
	default:
		return "state-changed", "state changed"
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func emitWatchEvent(ctx *commandContext, event watchEvent) error {
	if ctx.json {
		return emitWatchJSON(watchOutput(ctx), event)
	}
	actor := "-"
	if event.Actor != nil && *event.Actor != "" {
		actor = *event.Actor
	}
	title := truncateHuman(safeSingleLine(event.Title), 36)
	_, err := fmt.Fprintf(watchOutput(ctx), "%s  %-10s  %s  %-36s  %s\n", event.Time.Local().Format("15:04:05"), safeSingleLine(actor), safeSingleLine(event.Ticket), title, safeSingleLine(event.Message))
	return err
}

func emitWatchJSON(w io.Writer, event watchEvent) error {
	return emitJSON(w, struct {
		Time    string  `json:"time"`
		Actor   *string `json:"actor"`
		Ticket  string  `json:"ticket"`
		Title   string  `json:"title"`
		Event   string  `json:"event"`
		Message string  `json:"message,omitempty"`
		State   string  `json:"state"`
		From    string  `json:"from,omitempty"`
		To      string  `json:"to,omitempty"`
	}{event.Time.Format(time.RFC3339), event.Actor, event.Ticket, event.Title, event.Event, event.Message, event.State, event.From, event.To})
}

func watchOutput(ctx *commandContext) io.Writer {
	if ctx.liveWriter != nil {
		return ctx.liveWriter
	}
	return ctx.stdout
}

func watchWrite(ctx *commandContext, text string) {
	_, _ = io.WriteString(watchOutput(ctx), text)
}
