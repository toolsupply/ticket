package domain

import "testing"

func TestEmptyObjectiveDoesNotMaskLaterFilledObjective(t *testing.T) {
	data := []byte("# Pasted ticket\n\n- State: open\n- Priority: P2\n\n## Objective\n\n## Objective\n\nDo the pasted work.\n")
	ticket, err := ParseTicketFile("20260913-21924", data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := ticket.SectionText("objective"); got != "Do the pasted work." {
		t.Fatalf("objective=%q", got)
	}
}
