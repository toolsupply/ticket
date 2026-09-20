package domain

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/toolsupply/ticket/internal/contract"
)

func TestReadyReviewQueueUsesReviewEligibility(t *testing.T) {
	e := newEnv(t, 9501)
	dependency := e.create(t, "open dependency", CreateOptions{Sections: map[string]string{"objective": "Wait."}})
	review := e.create(t, "review ticket", CreateOptions{
		DependsOn: []string{dependency},
		Sections:  map[string]string{"objective": "Review this."},
	})
	if _, err := Submit(e.st, review, SubmitOptions{}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// Review eligibility is governed by state and ownership, not implementation
	// readiness. Clear the objective after submission to retain a blocker that
	// the review queue must ignore.
	if _, err := Update(e.st, review, UpdateOptions{Sections: map[string]string{"objective": ""}}); err != nil {
		t.Fatalf("clear objective: %v", err)
	}

	before, err := os.ReadFile(filepath.Join(e.base, "tickets", review, "TASK.md"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := ReadyWithOptions(e.st, ReadyOptions{Queue: "review", Filters: ListOptions{Fields: []string{"id", "state", "assignee"}}})
	if err != nil {
		t.Fatalf("review ready: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != review || result.Items[0].State == nil || *result.Items[0].State != "review" || result.Items[0].Assignee != nil {
		t.Fatalf("review ready result: %+v", result)
	}
	after, err := os.ReadFile(filepath.Join(e.base, "tickets", review, "TASK.md"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("review ready changed authoritative ticket state")
	}

	if result, err := Ready(e.st, ListOptions{}); err != nil {
		t.Fatalf("open ready: %v", err)
	} else if len(result.Items) != 1 || result.Items[0].ID == review {
		t.Fatalf("open ready included review-blocked work: %+v", result)
	}
	if _, err := ReadyWithOptions(e.st, ReadyOptions{Queue: "hold"}); err == nil || contractCode(t, err) != contract.ErrInvalidArgument {
		t.Fatalf("invalid ready queue: %v", err)
	}
}

func TestReadyReviewRejectsReadinessStateFilters(t *testing.T) {
	e := newEnv(t, 9502)
	for _, filters := range []ListOptions{{State: "review"}, {Unassigned: true}, {Assignee: "reviewer"}} {
		if _, err := ReadyWithOptions(e.st, ReadyOptions{Queue: "review", Filters: filters}); err == nil || contractCode(t, err) != contract.ErrInvalidArgument {
			t.Fatalf("filters=%+v error=%v", filters, err)
		}
	}
	var ce *contract.Error
	if _, err := ReadyWithOptions(e.st, ReadyOptions{Queue: "bad"}); err == nil || !errors.As(err, &ce) {
		t.Fatalf("invalid queue error=%v", err)
	}
}
