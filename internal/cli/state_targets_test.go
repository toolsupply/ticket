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
		if row["state"] != "completed" || row["changed"] != true {
			t.Fatalf("close state result: %v", row)
		}
	}
}
