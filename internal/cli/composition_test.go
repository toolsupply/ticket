package cli

import (
	"bytes"
	"strings"
	"testing"
)

func runInvocation(args ...string) (string, int) {
	var output bytes.Buffer
	code := Run(args, &output)
	return output.String(), code
}

func TestQueryCompositionRoutesTicketSetInProcess(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	inside := stringValue(t, exactlyOneJSONObject(t, mustCLI(t, "create", "inside", "Keep this dependency.", "--tag", "inside")), "id")
	outside := stringValue(t, exactlyOneJSONObject(t, mustCLI(t, "create", "outside", "Exclude this dependency.", "--tag", "outside")), "id")
	root := stringValue(t, exactlyOneJSONObject(t, mustCLI(t, "create", "root", "Select the root.", "--tag", "root", "--depends-on", inside, "--depends-on", outside)), "id")

	direct := queryJSONItems(t, mustCLI(t, "query", "tag:inside", "or", "tag:root"))
	composed := queryJSONItems(t, mustCLI(t, "query", "tag:inside", "or", "tag:root", "::", "list"))
	if len(composed) != len(direct) || composed[0].(map[string]any)["id"] != direct[0].(map[string]any)["id"] || composed[1].(map[string]any)["id"] != direct[1].(map[string]any)["id"] {
		t.Fatalf("composed list differs from direct query: direct=%v composed=%v", direct, composed)
	}

	graph := exactlyOneJSONObject(t, mustCLI(t, "query", "tag:inside", "or", "tag:root", "::", "graph"))
	if len(graph["nodes"].([]any)) != 2 || len(graph["edges"].([]any)) != 1 {
		t.Fatalf("composed graph was not induced: %v", graph)
	}
	edge := graph["edges"].([]any)[0].(map[string]any)
	if edge["from"] != root || edge["to"] != inside {
		t.Fatalf("composed graph edge: %v", edge)
	}
	if out, code := runCLI(t, "query", "tag:root", "::", "graph", root); code == 0 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("consumer target accepted: %q", out)
	}
	if out, code := runCLI(t, "query", "tag:root", "::", "graph", "--depth", "1"); code == 0 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("composition graph depth accepted: %q", out)
	}
	if out, code := runCLI(t, "query", "tag:root", "::", "graph", "--scope", "other"); code == 0 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("consumer scope accepted: %q", out)
	}
}

func TestQueryCompositionRunsBatchMutations(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	open := stringValue(t, exactlyOneJSONObject(t, mustCLI(t, "create", "open", "Close this.")), "id")
	closed := exactlyOneJSONObject(t, mustCLI(t, "query", "state:open", "::", "close", "-m", "Accepted"))
	if items := closed["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != open {
		t.Fatalf("composed close: %v", closed)
	}

	archived := exactlyOneJSONObject(t, mustCLI(t, "query", "state:closed", "::", "archive"))
	if items := archived["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != open {
		t.Fatalf("composed archive: %v", archived)
	}

	review := stringValue(t, exactlyOneJSONObject(t, mustCLI(t, "create", "review", "Approve this.")), "id")
	mustCLI(t, "claim", review, "--actor", "worker")
	mustCLI(t, "submit", review, "--actor", "worker")
	approved := exactlyOneJSONObject(t, mustCLI(t, "query", "--actor", "reviewer", "state:review", "unclaimed", "::", "approve"))
	if items := approved["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != review {
		t.Fatalf("composed approve: %v", approved)
	}
}

func TestQueryCompositionListPresentationAndHumanOutput(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := stringValue(t, exactlyOneJSONObject(t, mustCLI(t, "create", "present", "Presentation.")), "id")
	ids, code := runCLIHuman(t, "query", "state:open", "::", "list", "--ids")
	if code != 0 {
		t.Fatalf("composed IDs output: exit=%d out=%q", code, ids)
	}
	if !strings.Contains(ids, id+"\n") {
		t.Fatalf("composed IDs output: %q", ids)
	}
	human, code := runCLIHuman(t, "query", "state:open", "::", "list", "--fields", "id,title")
	if code != 0 || !strings.Contains(human, id) {
		t.Fatalf("composed list fields: exit=%d out=%q", code, human)
	}
}

func TestQueryCompositionConsumerHelpDoesNotExecute(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	out, code := runCLIHuman(t, "query", "state:open", "::", "list", "--help")
	if code != 0 || !strings.Contains(out, "list - List tickets") {
		t.Fatalf("composed consumer help: exit=%d out=%q", code, out)
	}
}

func TestQueryCompositionJSONTransportIsInvocationGlobal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := stringValue(t, exactlyOneJSONObject(t, mustCLI(t, "create", "json", "JSON transport.")), "id")

	for _, args := range [][]string{
		{"query", "-j", "state:open", "::", "list", "--json=false"},
		{"query", "state:open", "::", "list", "-j"},
	} {
		out, code := runInvocation(args...)
		if code != 0 {
			t.Fatalf("JSON composition failed: args=%v exit=%d out=%q", args, code, out)
		}
		result := exactlyOneJSONObject(t, out)
		items, ok := result["items"].([]any)
		if !ok || len(items) != 1 || items[0].(map[string]any)["id"] != id {
			t.Fatalf("JSON composition result: args=%v result=%v", args, result)
		}
	}

	for _, args := range [][]string{
		{"query", "-j", "state:open", "::", "list", "bad"},
		{"query", "state:open", "::", "list", "bad", "-j"},
		{"query", "-j", "state:open", "::", "list", "--json=false", "bad"},
	} {
		out, code := runInvocation(args...)
		if code == 0 {
			t.Fatalf("invalid JSON composition succeeded: args=%v out=%q", args, out)
		}
		result := exactlyOneJSONObject(t, out)
		if result["error"] == nil {
			t.Fatalf("invalid JSON composition was not an error object: args=%v result=%v", args, result)
		}
	}

	closedOut, code := runInvocation("query", "-j", "state:open", "::", "close")
	if code != 0 || len(exactlyOneJSONObject(t, closedOut)["items"].([]any)) != 1 {
		t.Fatalf("JSON mutating composition close: exit=%d out=%q", code, closedOut)
	}
	archivedOut, code := runInvocation("query", "state:closed", "::", "archive", "-j")
	if code != 0 || len(exactlyOneJSONObject(t, archivedOut)["items"].([]any)) != 1 {
		t.Fatalf("JSON mutating composition archive: exit=%d out=%q", code, archivedOut)
	}
}
