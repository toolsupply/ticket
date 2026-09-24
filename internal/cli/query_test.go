package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func queryJSONItems(t *testing.T, output string) []any {
	t.Helper()
	result := exactlyOneJSONObject(t, output)
	items, ok := result["items"].([]any)
	if !ok {
		t.Fatalf("query result has no items: %v", result)
	}
	return items
}

func mustCLIJSONBeforeTail(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	argv := append([]string{"-j"}, args...)
	if code := Run(argv, &out); code != 0 {
		t.Fatalf("cli %v: exit=%d out=%q", args, code, out.String())
	}
	return out.String()
}

func TestQueryCommandScopesGrammarAndProjection(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	open := exactlyOneJSONObject(t, mustCLI(t, "create", "Open", "Open work.", "--priority", "0", "--tag", "release"))["id"].(string)
	hold := exactlyOneJSONObject(t, mustCLI(t, "create", "Hold", "Hold work.", "--priority", "2", "--tag", "other"))["id"].(string)
	mustCLI(t, "state", hold, "hold")
	closed := exactlyOneJSONObject(t, mustCLI(t, "create", "Closed", "Closed work.", "--priority", "3"))["id"].(string)
	mustCLI(t, "close", closed)
	archived := exactlyOneJSONObject(t, mustCLI(t, "create", "Archived", "Archived work."))["id"].(string)
	mustCLI(t, "close", archived)
	mustCLI(t, "archive", archived)

	items := queryJSONItems(t, mustCLI(t, "query", "state:open"))
	if len(items) != 1 || items[0].(map[string]any)["id"] != open {
		t.Fatalf("state query: %v", items)
	}
	if items[0].(map[string]any)["state"] != "open" {
		t.Fatalf("state projection: %v", items[0])
	}

	allActive := queryJSONItems(t, mustCLI(t, "query"))
	if len(allActive) != 3 {
		t.Fatalf("empty query should select all active tickets: %v", allActive)
	}
	legacyClosed := queryJSONItems(t, mustCLI(t, "query", "state:completed"))
	if len(legacyClosed) != 1 || legacyClosed[0].(map[string]any)["id"] != closed || legacyClosed[0].(map[string]any)["state"] != "closed" {
		t.Fatalf("legacy state query: %v", legacyClosed)
	}

	archivedItems := queryJSONItems(t, mustCLI(t, "query", "--archived", "archived"))
	if len(archivedItems) != 1 || archivedItems[0].(map[string]any)["id"] != archived || archivedItems[0].(map[string]any)["archived"] != true {
		t.Fatalf("archived query: %v", archivedItems)
	}
	allClosed := queryJSONItems(t, mustCLI(t, "query", "--all", "archived", "state:closed"))
	if len(allClosed) != 1 || allClosed[0].(map[string]any)["id"] != archived {
		t.Fatalf("all archived closed query: %v", allClosed)
	}

	if ids, code := runCLIHuman(t, "query", "--ids", "state:open"); code != 0 || ids != open+"\n" {
		t.Fatalf("query IDs output: exit=%d output=%q", code, ids)
	}
	if human, code := runCLIHuman(t, "query", "state:open"); code != 0 || !strings.Contains(human, "STATE") || !strings.Contains(human, open) {
		t.Fatalf("query human output: exit=%d output=%q", code, human)
	}
	if out, code := runCLI(t, "query", "--all", "--archived"); code == 0 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("conflicting query scopes accepted: %q", out)
	}
	if out, code := runCLI(t, "query", "unknown:value"); code == 0 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("unknown query field accepted: %q", out)
	}
	if out, code := runCLI(t, "query", "priority", "le", "P1"); code != 0 || len(queryJSONItems(t, out)) != 1 || queryJSONItems(t, out)[0].(map[string]any)["id"] != open {
		t.Fatalf("priority comparison: exit=%d output=%q", code, out)
	}
}

func TestQueryBooleanPrecedenceAndReadOnlyStorage(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	open := exactlyOneJSONObject(t, mustCLI(t, "create", "Open", "Open work.", "--tag", "match"))["id"].(string)
	hold := exactlyOneJSONObject(t, mustCLI(t, "create", "Hold", "Hold work.", "--tag", "other"))["id"].(string)
	mustCLI(t, "state", hold, "hold")
	path := filepath.Join(dir, "tickets", open, "TASK.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	items := queryJSONItems(t, mustCLI(t, "query", "state:open", "or", "state:hold", "tag:match"))
	if len(items) != 1 || items[0].(map[string]any)["id"] != open {
		t.Fatalf("boolean precedence query: %v", items)
	}
	notOpen := queryJSONItems(t, mustCLI(t, "query", "not", "state:open"))
	if len(notOpen) != 1 || notOpen[0].(map[string]any)["id"] != hold {
		t.Fatalf("not query: %v", notOpen)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("query changed TASK.md: err=%v", err)
	}
}

func TestQueryPaginationPreservesMoreMetadata(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	for _, title := range []string{"One", "Two", "Three"} {
		mustCLI(t, "create", title, title+" work.")
	}
	result := exactlyOneJSONObject(t, mustCLI(t, "query", "--limit", "1"))
	if result["more"] != true || len(result["items"].([]any)) != 1 {
		t.Fatalf("limited query metadata: %v", result)
	}
	offset := exactlyOneJSONObject(t, mustCLI(t, "query", "--offset", "1", "--limit", "1"))
	if offset["more"] != true || len(offset["items"].([]any)) != 1 {
		t.Fatalf("offset query metadata: %v", offset)
	}
	last := exactlyOneJSONObject(t, mustCLI(t, "query", "--offset", "2", "--limit", "1"))
	if last["more"] != false || len(last["items"].([]any)) != 1 {
		t.Fatalf("final query metadata: %v", last)
	}
}

func TestListUsesTQLAdaptersWithoutChangingLegacyDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	open := exactlyOneJSONObject(t, mustCLI(t, "create", "Open", "Open work.", "--tag", "release"))["id"].(string)
	hold := exactlyOneJSONObject(t, mustCLI(t, "create", "Hold", "Hold work."))["id"].(string)
	mustCLI(t, "state", hold, "hold")
	closed := exactlyOneJSONObject(t, mustCLI(t, "create", "Closed", "Closed work."))["id"].(string)
	mustCLI(t, "close", closed)
	archived := exactlyOneJSONObject(t, mustCLI(t, "create", "Archived", "Archived work."))["id"].(string)
	mustCLI(t, "close", archived)
	mustCLI(t, "archive", archived)

	bare := exactlyOneJSONObject(t, mustCLI(t, "list"))
	if items := bare["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != open {
		t.Fatalf("bare JSON list: %v", bare)
	}

	state := queryJSONItems(t, mustCLI(t, "list", "state:open"))
	if len(state) != 1 || state[0].(map[string]any)["id"] != open {
		t.Fatalf("TQL list: %v", state)
	}
	allClosed := queryJSONItems(t, mustCLI(t, "list", "--all", "state:closed"))
	if len(allClosed) != 2 {
		t.Fatalf("all-scope TQL list: %v", allClosed)
	}
	archivedClosed := queryJSONItems(t, mustCLI(t, "list", "--archived", "state:closed"))
	if len(archivedClosed) != 1 || archivedClosed[0].(map[string]any)["id"] != archived {
		t.Fatalf("archived-scope TQL list: %v", archivedClosed)
	}
	notTerminal := queryJSONItems(t, mustCLI(t, "list", "not", "terminal"))
	if len(notTerminal) != 2 {
		t.Fatalf("semantic TQL list: %v", notTerminal)
	}
	legacy := queryJSONItems(t, mustCLI(t, "list", "open", "hold"))
	if len(legacy) != 2 {
		t.Fatalf("legacy state list: %v", legacy)
	}
	legacyIntersection := queryJSONItems(t, mustCLI(t, "list", closed, "open"))
	if len(legacyIntersection) != 0 {
		t.Fatalf("legacy ID/state interaction changed: %v", legacyIntersection)
	}
	union := queryJSONItems(t, mustCLI(t, "list", closed, "state:open"))
	if len(union) != 2 || union[0].(map[string]any)["id"] != closed || union[1].(map[string]any)["id"] != open {
		t.Fatalf("ID/TQL union: %v", union)
	}
	filtered := queryJSONItems(t, mustCLI(t, "list", "--tag", "release", "state:open"))
	if len(filtered) != 1 || filtered[0].(map[string]any)["id"] != open {
		t.Fatalf("TQL adapter filter: %v", filtered)
	}
	tail := queryJSONItems(t, mustCLIJSONBeforeTail(t, "list", "-q", "state:open"))
	if len(tail) != 1 || tail[0].(map[string]any)["id"] != open {
		t.Fatalf("query tail list: %v", tail)
	}
	var tailError bytes.Buffer
	if code := Run([]string{"-j", "list", "-q", "state:open", "-l"}, &tailError); code == 0 || errCode(t, tailError.String()) != "invalid_argument" {
		t.Fatalf("option after query tail accepted: %q", tailError.String())
	}
}

func TestParseTQLRejectsAmbiguousAndMalformedExpressions(t *testing.T) {
	if expr, err := parseTQL(nil); err != nil || expr != nil {
		t.Fatalf("empty expression: expr=%v err=%v", expr, err)
	}
	for _, tokens := range [][]string{
		{"and", "state:open"}, {"state:open", "or"}, {"state:open", "and"},
		{"priority", "le"}, {"state:"}, {"unknown:value"}, {"::"},
	} {
		if _, err := parseTQL(tokens); err == nil {
			t.Fatalf("malformed expression accepted: %v", tokens)
		}
	}
}
