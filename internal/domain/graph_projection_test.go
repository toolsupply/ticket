package domain

import "testing"

func TestGraphProjectionForwardReverseDepthAndArchive(t *testing.T) {
	e := newEnv(t, 9900)
	shared := e.create(t, "Shared", CreateOptions{Sections: map[string]string{"objective": "Complete shared work."}})
	left := e.create(t, "Left", CreateOptions{DependsOn: []string{shared}, Sections: map[string]string{"objective": "Complete left work."}})
	right := e.create(t, "Right", CreateOptions{DependsOn: []string{shared}, Sections: map[string]string{"objective": "Complete right work."}})
	root := e.create(t, "Root", CreateOptions{DependsOn: []string{left, right}, Sections: map[string]string{"objective": "Complete root work."}})

	forward, err := Graph(e.st, GraphOptions{RootID: root})
	if err != nil {
		t.Fatal(err)
	}
	if forward.Direction != "dependencies" || len(forward.Nodes) != 4 || len(forward.Edges) != 4 {
		t.Fatalf("forward graph: %+v", forward)
	}
	if countGraphNode(forward, shared) != 1 || !hasGraphEdge(forward, root, left) || !hasGraphEdge(forward, root, right) || !hasGraphEdge(forward, left, shared) {
		t.Fatalf("forward topology: %+v", forward)
	}

	reverse, err := Graph(e.st, GraphOptions{RootID: shared, Reverse: true})
	if err != nil {
		t.Fatal(err)
	}
	if reverse.Direction != "dependents" || len(reverse.Nodes) != 4 || !hasGraphEdge(reverse, shared, left) || !hasGraphEdge(reverse, shared, right) {
		t.Fatalf("reverse topology: %+v", reverse)
	}

	limited, err := Graph(e.st, GraphOptions{RootID: root, Depth: 1, DepthSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if !limited.Truncated || len(limited.Nodes) != 3 {
		t.Fatalf("depth-limited graph: %+v", limited)
	}

	if _, err := Close(e.st, shared, CloseOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(e.st, shared); err != nil {
		t.Fatal(err)
	}
	unrelated := e.create(t, "Unrelated archived", CreateOptions{Sections: map[string]string{"objective": "Complete unrelated work."}})
	if _, err := Close(e.st, unrelated, CloseOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(e.st, unrelated); err != nil {
		t.Fatal(err)
	}
	if ticket, err := ReadTicket(e.st, shared); err != nil {
		t.Fatalf("read archived dependency: %v", err)
	} else if !ticket.Archived {
		t.Fatalf("archived dependency not marked archived: %+v", ticket)
	}
	archived, err := Graph(e.st, GraphOptions{RootID: root})
	if err != nil {
		t.Fatal(err)
	}
	if !graphNodeArchived(archived, shared) {
		t.Fatalf("archived dependency missing from graph: %+v", archived)
	}
	if _, err := Graph(e.st, GraphOptions{RootID: shared}); err != nil {
		t.Fatalf("explicit archived root: %v", err)
	}
	active, err := Graph(e.st, GraphOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if countGraphNode(active, unrelated) != 0 || !graphNodeArchived(active, shared) {
		t.Fatalf("active graph archive selection: %+v", active)
	}
}

func countGraphNode(result *GraphResult, id string) int {
	count := 0
	for _, node := range result.Nodes {
		if node.ID == id {
			count++
		}
	}
	return count
}

func hasGraphEdge(result *GraphResult, from, to string) bool {
	for _, edge := range result.Edges {
		if edge.From == from && edge.To == to {
			return true
		}
	}
	return false
}

func graphNodeArchived(result *GraphResult, id string) bool {
	for _, node := range result.Nodes {
		if node.ID == id {
			return node.Archived
		}
	}
	return false
}
