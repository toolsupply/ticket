package cli

import (
	"testing"
)

func TestStatusIndicatesArchivedTicket(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	created, code := runCLI(t, "create", "Archived status", "Keep archived status visible")
	if code != 0 {
		t.Fatalf("create: exit=%d out=%q", code, created)
	}
	createdObject := exactlyOneJSONObject(t, created)
	id, ok := createdObject["id"].(string)
	if !ok || id == "" {
		t.Fatalf("create missing id: %v", createdObject)
	}
	if out, code := runCLI(t, "close", id); code != 0 {
		t.Fatalf("close: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "archive", id); code != 0 {
		t.Fatalf("archive: exit=%d out=%q", code, out)
	}
	human, code := runCLIHuman(t, "status", id)
	if code != 0 || !containsLine(human, "archived: true") {
		t.Fatalf("human archived status: exit=%d out=%q", code, human)
	}
	jsonStatus, code := runCLI(t, "status", id)
	if code != 0 {
		t.Fatalf("json status: exit=%d out=%q", code, jsonStatus)
	}
	status := exactlyOneJSONObject(t, jsonStatus)
	if archived, ok := status["archived"].(bool); !ok || !archived {
		t.Fatalf("json archived status: %v", status)
	}
}

func containsLine(text, want string) bool {
	for _, line := range splitLines(text) {
		if line == want {
			return true
		}
	}
	return false
}

func splitLines(text string) []string {
	var lines []string
	start := 0
	for index := 0; index < len(text); index++ {
		if text[index] == '\n' {
			lines = append(lines, text[start:index])
			start = index + 1
		}
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}
