package cli

import "testing"

func TestCloseAndApproveAcceptMixedStateSelectors(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	create := func(title string) string {
		return exactlyOneJSONObject(t, mustCLI(t, "create", title, "Do the work."))["id"].(string)
	}

	firstReview := create("first review")
	secondReview := create("second review")
	for _, id := range []string{firstReview, secondReview} {
		mustCLI(t, "claim", id, "--actor", "worker")
		mustCLI(t, "submit", id, "--actor", "worker")
	}
	approved := exactlyOneJSONObject(t, mustCLI(t, "approve", "-s", "review", "--actor", "reviewer"))
	approvedItems := approved["items"].([]any)
	if len(approvedItems) != 2 {
		t.Fatalf("approve state selector: %v", approved)
	}
	for _, value := range approvedItems {
		if value.(map[string]any)["state"] != "signoff" {
			t.Fatalf("approve state result: %v", value)
		}
	}

	open := create("open close")
	hold := create("hold close")
	mustCLI(t, "hold", hold, "--actor", "worker")
	closed := exactlyOneJSONObject(t, mustCLI(t, "close", open, "--state", "hold"))
	closedItems := closed["items"].([]any)
	if len(closedItems) != 2 {
		t.Fatalf("mixed close selectors: %v", closed)
	}
	for _, value := range closedItems {
		row := value.(map[string]any)
		if row["state"] != "closed" || row["changed"] != true {
			t.Fatalf("close state result: %v", row)
		}
	}
}

func TestCloseAndApproveAcceptTQLTargetsAndEmptyBatches(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	open := exactlyOneJSONObject(t, mustCLI(t, "create", "open target", "Do the work."))["id"].(string)
	hold := exactlyOneJSONObject(t, mustCLI(t, "create", "hold target", "Pause the work."))["id"].(string)
	mustCLI(t, "hold", hold, "--actor", "worker")

	closed := exactlyOneJSONObject(t, mustCLI(t, "close", "state:open", "or", "state:hold"))
	closedItems := closed["items"].([]any)
	if len(closedItems) != 2 || closedItems[0].(map[string]any)["state"] != "closed" || closedItems[1].(map[string]any)["state"] != "closed" {
		t.Fatalf("TQL close: %v", closed)
	}

	empty := exactlyOneJSONObject(t, mustCLI(t, "close", "state:review"))
	if items := empty["items"].([]any); len(items) != 0 {
		t.Fatalf("empty TQL close: %v", empty)
	}
	if open == "" {
		t.Fatal("missing open fixture")
	}

	rejected := exactlyOneJSONObject(t, mustCLI(t, "create", "rejected target", "Reject the work."))["id"].(string)
	mustCLI(t, "reject", rejected, "no longer needed")
	if out, code := runCLI(t, "close", "state:rejected"); code == 0 || errCode(t, out) != "invalid_transition" {
		t.Fatalf("TQL close accepted rejected target: %q", out)
	}

	firstReview := exactlyOneJSONObject(t, mustCLI(t, "create", "first TQL review", "Review this."))["id"].(string)
	secondReview := exactlyOneJSONObject(t, mustCLI(t, "create", "second TQL review", "Review this too."))["id"].(string)
	for _, id := range []string{firstReview, secondReview} {
		mustCLI(t, "claim", id, "--actor", "worker")
		mustCLI(t, "submit", id, "--actor", "worker")
	}
	approved := exactlyOneJSONObject(t, mustCLI(t, "approve", "state:review", "unclaimed", "--actor", "reviewer"))
	if items := approved["items"].([]any); len(items) != 2 {
		t.Fatalf("TQL approve: %v", approved)
	}

	thirdReview := exactlyOneJSONObject(t, mustCLI(t, "create", "tail review", "Review this from a query tail."))["id"].(string)
	mustCLI(t, "claim", thirdReview, "--actor", "worker")
	mustCLI(t, "submit", thirdReview, "--actor", "worker")
	tail := exactlyOneJSONObject(t, mustCLIJSONBeforeTail(t, "approve", "--actor", "reviewer", "-q", "state:review", "unclaimed"))
	if items := tail["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != thirdReview {
		t.Fatalf("query-tail approve: %v", tail)
	}
}
