package domain

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"ticket/internal/contract"
)

func TestReadTicketAcceptsManagedFileAtLimit(t *testing.T) {
	e := newEnv(t, 191)
	id := e.create(t, "At the limit", CreateOptions{Sections: map[string]string{"objective": "Read the complete file."}})
	ticket, err := ReadTicket(e.st, id)
	if err != nil {
		t.Fatal(err)
	}
	padding := bytes.Repeat([]byte("x"), TaskMaxBytes-len(ticket.FileBytes))
	data := append(append([]byte(nil), ticket.FileBytes...), padding...)
	if len(data) != TaskMaxBytes {
		t.Fatalf("test file size=%d want %d", len(data), TaskMaxBytes)
	}
	if err := os.WriteFile(filepath.Join(e.st.Root, id, "TASK.md"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTicket(e.st, id); err != nil {
		t.Fatalf("exact-limit ticket rejected: %v", err)
	}
}

func TestReadTicketBoundsOversizedManagedFile(t *testing.T) {
	e := newEnv(t, 192)
	id := e.create(t, "Over the limit", CreateOptions{Sections: map[string]string{"objective": "Reject the oversized file."}})
	ticket, err := ReadTicket(e.st, id)
	if err != nil {
		t.Fatal(err)
	}
	data := append(append([]byte(nil), ticket.FileBytes...), bytes.Repeat([]byte("x"), TaskMaxBytes-len(ticket.FileBytes)+1)...)
	if err := os.WriteFile(filepath.Join(e.st.Root, id, "TASK.md"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = ReadTicket(e.st, id)
	ce, ok := err.(*contract.Error)
	if !ok || ce.Code != contract.ErrFileTooLarge {
		t.Fatalf("oversized read error=%v", err)
	}
	if ce.Details["path"] != id+"/TASK.md" || ce.Details["size"] != int64(len(data)) {
		t.Fatalf("oversized read details=%v", ce.Details)
	}
}
