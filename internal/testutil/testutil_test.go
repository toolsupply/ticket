package testutil

import (
	"errors"
	"testing"

	"github.com/toolsupply/ticket/internal/identity"
)

func TestTicketIDLength(t *testing.T) {
	src := NewDeterministicSource(1)
	id, err := src.ID("")
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != identity.TimestampIDLen {
		t.Fatalf("ticket ID length = %d, want %d", len(id), identity.TimestampIDLen)
	}
}

func TestDeterministicSourceUnique(t *testing.T) {
	src := NewDeterministicSource(42)
	seen := make(map[string]bool, 10000)
	for i := 0; i < 10000; i++ {
		id, err := src.ID("")
		if err != nil {
			t.Fatal(err)
		}
		if !identity.ValidID(id) {
			t.Fatalf("ID %d not canonical: %s", i, id)
		}
		if seen[id] {
			t.Fatalf("duplicate fixture ID: %s", id)
		}
		seen[id] = true
	}
}

func TestFailingSource(t *testing.T) {
	_, err := FailingSource{}.ID("")
	if !errors.Is(err, identity.ErrRandomnessUnavailable) {
		t.Fatalf("err = %v, want ErrRandomnessUnavailable", err)
	}
}
