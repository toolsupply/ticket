package domain

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/toolsupply/ticket/internal/store"
)

func TestCreatePublishesCompleteHeldTicketAsFirstVisibleVersion(t *testing.T) {
	e := newEnv(t, 802)
	published := make(chan string, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	originalPublish := publishCreatedTicket
	publishCreatedTicket = func(st *store.Store, id string, data []byte, maxBytes int) error {
		if err := originalPublish(st, id, data, maxBytes); err != nil {
			return err
		}
		published <- id
		<-release
		return nil
	}
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		publishCreatedTicket = originalPublish
	})

	type createOutcome struct {
		result *CreateResult
		err    error
	}
	done := make(chan createOutcome, 1)
	go func() {
		result, err := Create(e.st, CreateOptions{
			Title: "Complete held publication", Priority: 2, State: "hold",
			Sections: map[string]string{
				"objective":  "Complete the held work.",
				"acceptance": "The first visible document is complete and held.",
			},
		})
		done <- createOutcome{result: result, err: err}
	}()

	var id string
	select {
	case id = <-published:
	case <-time.After(5 * time.Second):
		t.Fatal("creation did not reach its publication milestone")
	}

	data, err := os.ReadFile(filepath.Join(e.base, "tickets", id, "TASK.md"))
	if err != nil {
		t.Fatalf("read first published TASK.md: %v", err)
	}
	first, err := ParseTicketFile(id, data)
	if err != nil {
		t.Fatalf("parse first published TASK.md: %v", err)
	}
	if first.State != "hold" || strings.TrimSpace(first.SectionText("objective")) != "Complete the held work." || strings.TrimSpace(first.SectionText("acceptance")) != "The first visible document is complete and held." {
		t.Fatalf("first published ticket is incomplete or actionable: state=%q objective=%q acceptance=%q", first.State, first.SectionText("objective"), first.SectionText("acceptance"))
	}

	for _, queue := range []string{"open", "review"} {
		ready, err := ReadyWithOptions(e.st, ReadyOptions{Queue: queue})
		if err != nil {
			t.Fatalf("select %s queue at publication milestone: %v", queue, err)
		}
		if len(ready.Items) != 0 {
			t.Fatalf("held first version appeared in %s queue: %+v", queue, ready.Items)
		}
	}

	releaseOnce.Do(func() { close(release) })
	select {
	case outcome := <-done:
		if outcome.err != nil || outcome.result == nil || outcome.result.ID != id {
			t.Fatalf("create after publication: result=%+v error=%v", outcome.result, outcome.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("creation did not finish after publication was released")
	}
}
