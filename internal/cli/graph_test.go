package cli

import (
	"strings"
	"testing"
)

func TestGraphCommandRendersTreeAndStructuredEdges(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	depJSON, code := runCLI(t, "create", "Dependency", "Finish dependency")
	if code != 0 {
		t.Fatalf("dependency: exit=%d out=%q", code, depJSON)
	}
	dep := stringValue(t, exactlyOneJSONObject(t, depJSON), "id")
	rootJSON, code := runCLI(t, "create", "Root", "Finish root", "--depends-on", dep)
	if code != 0 {
		t.Fatalf("root: exit=%d out=%q", code, rootJSON)
	}
	root := stringValue(t, exactlyOneJSONObject(t, rootJSON), "id")

	ascii, code := runCLIHuman(t, "graph", root, "--ascii")
	if code != 0 || !strings.Contains(ascii, root) || !strings.Contains(ascii, dep) || !strings.Contains(ascii, "`-") || strings.Contains(ascii, "└") {
		t.Fatalf("ascii graph: exit=%d out=%q", code, ascii)
	}
	unicode, code := runCLIHuman(t, "graph", root, "--unicode")
	if code != 0 || !strings.Contains(unicode, "└─") {
		t.Fatalf("unicode graph: exit=%d out=%q", code, unicode)
	}
	if out, code := runCLI(t, "graph", root, "--ascii", "--unicode"); code == 0 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("conflicting render flags: exit=%d out=%q", code, out)
	}
	structured, code := runCLI(t, "graph", root)
	if code != 0 {
		t.Fatalf("json graph: exit=%d out=%q", code, structured)
	}
	graph := exactlyOneJSONObject(t, structured)
	if graph["root"] != root || graph["direction"] != "dependencies" {
		t.Fatalf("graph envelope: %v", graph)
	}
	edges, ok := graph["edges"].([]any)
	if !ok || len(edges) != 1 {
		t.Fatalf("graph edges: %v", graph)
	}
}

func TestGraphDefaultsToNonterminalTickets(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	openID := exactlyOneJSONObject(t, mustCLI(t, "create", "Graph open", "Open work."))["id"].(string)
	closedID := exactlyOneJSONObject(t, mustCLI(t, "create", "Graph closed", "Closed work."))["id"].(string)
	if out, code := runCLI(t, "close", closedID); code != 0 {
		t.Fatalf("close: exit=%d out=%q", code, out)
	}
	result := exactlyOneJSONObject(t, mustCLI(t, "graph"))
	for _, node := range result["nodes"].([]any) {
		id := node.(map[string]any)["id"]
		if id == closedID {
			t.Fatalf("bare graph included terminal ticket: %v", result)
		}
	}
	if result["nodes"] == nil || len(result["nodes"].([]any)) == 0 || result["nodes"].([]any)[0].(map[string]any)["id"] != openID {
		t.Fatalf("bare graph omitted open ticket: %v", result)
	}
}

func TestGraphSelectedSetIsInducedAndSupportsTQLTargets(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	inside := stringValue(t, exactlyOneJSONObject(t, mustCLI(t, "create", "Inside dependency", "Keep this dependency in the selected set.", "--tag", "inside")), "id")
	outside := stringValue(t, exactlyOneJSONObject(t, mustCLI(t, "create", "Outside dependency", "Leave this dependency outside the selected set.", "--tag", "outside")), "id")
	root := stringValue(t, exactlyOneJSONObject(t, mustCLI(t, "create", "Selected root", "Select this root.", "--tag", "root", "--depends-on", inside, "--depends-on", outside)), "id")

	selected := exactlyOneJSONObject(t, mustCLI(t, "graph", "tag:inside", "or", "tag:root"))
	if _, hasRoot := selected["root"]; hasRoot || selected["direction"] != "dependencies" {
		t.Fatalf("selected graph metadata: %v", selected)
	}
	nodes := selected["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("selected graph nodes: %v", selected)
	}
	edges := selected["edges"].([]any)
	if len(edges) != 1 || edges[0].(map[string]any)["from"] != root || edges[0].(map[string]any)["to"] != inside {
		t.Fatalf("induced dependency edge: %v", selected)
	}
	for _, node := range nodes {
		if node.(map[string]any)["id"] == outside {
			t.Fatalf("outside dependency leaked into selected graph: %v", selected)
		}
	}

	reverse := exactlyOneJSONObject(t, mustCLI(t, "graph", "--reverse", "tag:inside", "or", "tag:root"))
	if reverse["direction"] != "dependents" {
		t.Fatalf("reverse graph metadata: %v", reverse)
	}
	reverseEdges := reverse["edges"].([]any)
	if len(reverseEdges) != 1 || reverseEdges[0].(map[string]any)["from"] != inside || reverseEdges[0].(map[string]any)["to"] != root {
		t.Fatalf("induced reverse edge: %v", reverse)
	}

	tail := exactlyOneJSONObject(t, mustCLIJSONBeforeTail(t, "graph", "-q", "tag:inside", "or", "tag:root"))
	if len(tail["nodes"].([]any)) != 2 || len(tail["edges"].([]any)) != 1 {
		t.Fatalf("query-tail graph: %v", tail)
	}
	if out, code := runCLI(t, "graph", "--depth", "1", "tag:inside"); code == 0 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("selected graph accepted depth: %q", out)
	}
}

func TestListGraphUsesTheSelectedTicketSet(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	inside := stringValue(t, exactlyOneJSONObject(t, mustCLI(t, "create", "List graph inside", "Selected dependency.", "--tag", "inside")), "id")
	outside := stringValue(t, exactlyOneJSONObject(t, mustCLI(t, "create", "List graph outside", "Unselected dependency.", "--tag", "outside")), "id")
	root := stringValue(t, exactlyOneJSONObject(t, mustCLI(t, "create", "List graph root", "Selected root.", "--tag", "root", "--depends-on", inside, "--depends-on", outside)), "id")

	selected := exactlyOneJSONObject(t, mustCLI(t, "list", "--graph", "tag:inside", "or", "tag:root"))
	if selected["direction"] != "dependencies" || selected["root"] != nil {
		t.Fatalf("list graph metadata: %v", selected)
	}
	nodes := selected["nodes"].([]any)
	if len(nodes) != 2 || !containsGraphNode(nodes, inside) || !containsGraphNode(nodes, root) || containsGraphNode(nodes, outside) {
		t.Fatalf("list graph induced nodes: %v", selected)
	}
	edges := selected["edges"].([]any)
	if len(edges) != 1 || edges[0].(map[string]any)["from"] != root || edges[0].(map[string]any)["to"] != inside {
		t.Fatalf("list graph induced edge: %v", selected)
	}
	for _, args := range [][]string{
		{"--ids"}, {"--fields", "id,title"}, {"--deps"}, {"--markdown"}, {"--long"},
	} {
		commandArgs := append([]string{"list", "--graph"}, args...)
		if out, code := runCLI(t, commandArgs...); code == 0 || errCode(t, out) != "invalid_argument" {
			t.Fatalf("list graph conflict accepted: args=%v exit=%d out=%q", args, code, out)
		}
	}

	legacy := graphNodeIDs(t, mustCLI(t, "list", "--graph", "open"))
	tql := graphNodeIDs(t, mustCLI(t, "list", "--graph", "state:open"))
	if strings.Join(legacy, "\n") != strings.Join(tql, "\n") {
		t.Fatalf("legacy and TQL list graph selections differ: legacy=%v tql=%v", legacy, tql)
	}
	dedup := graphNodeIDs(t, mustCLI(t, "list", "--graph", root, root))
	if len(dedup) != 1 || dedup[0] != root {
		t.Fatalf("explicit list graph IDs were not deduplicated: %v", dedup)
	}
	limited := graphNodeIDs(t, mustCLI(t, "list", "--graph", "--limit", "1", "--offset", "1", "state:open"))
	if len(limited) != 1 {
		t.Fatalf("list graph pagination: %v", limited)
	}
	limitedBare := graphNodeIDs(t, mustCLI(t, "list", "--graph", "--limit", "1"))
	if len(limitedBare) != 1 {
		t.Fatalf("bare list graph ignored explicit limit: %v", limitedBare)
	}

	closed := stringValue(t, exactlyOneJSONObject(t, mustCLI(t, "create", "List graph archived", "Archived ticket.")), "id")
	mustCLI(t, "close", closed)
	mustCLI(t, "archive", closed)
	archived := graphNodeIDs(t, mustCLI(t, "list", "--graph", "--archived"))
	if len(archived) != 1 || archived[0] != closed {
		t.Fatalf("archived list graph: %v", archived)
	}
	all := graphNodeIDs(t, mustCLI(t, "list", "--graph", "--all"))
	if !containsGraphID(all, closed) {
		t.Fatalf("all list graph omitted archived ticket: %v", all)
	}

	bare, code := runCLIHuman(t, "list")
	if code != 0 {
		t.Fatalf("bare human list: exit=%d out=%q", code, bare)
	}
	human, code := runCLIHuman(t, "list", "--graph")
	if code != 0 {
		t.Fatalf("human list graph: exit=%d out=%q", code, human)
	}
	for _, id := range []string{inside, outside, root} {
		if !strings.Contains(bare, id) || !strings.Contains(human, id) {
			t.Fatalf("bare list and list graph differ for %s: bare=%q graph=%q", id, bare, human)
		}
	}
	if strings.Contains(bare, closed) || strings.Contains(human, closed) {
		t.Fatalf("bare list or list graph included archived ticket: bare=%q graph=%q", bare, human)
	}
}

func graphNodeIDs(t *testing.T, output string) []string {
	t.Helper()
	result := exactlyOneJSONObject(t, output)
	nodes, ok := result["nodes"].([]any)
	if !ok {
		t.Fatalf("graph result has no nodes: %v", result)
	}
	ids := make([]string, 0, len(nodes))
	for _, raw := range nodes {
		ids = append(ids, raw.(map[string]any)["id"].(string))
	}
	return ids
}

func containsGraphNode(nodes []any, id string) bool {
	for _, raw := range nodes {
		if raw.(map[string]any)["id"] == id {
			return true
		}
	}
	return false
}

func containsGraphID(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func stringValue(t *testing.T, object map[string]any, key string) string {
	t.Helper()
	value, ok := object[key].(string)
	if !ok || value == "" {
		t.Fatalf("missing string %q in %v", key, object)
	}
	return value
}
