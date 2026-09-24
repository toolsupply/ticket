package domain

import "testing"

func TestArchivedListDependenciesResolveActiveTickets(t *testing.T) {
	e := newEnv(t, 9910)
	activeDependency := e.create(t, "Active dependency", CreateOptions{Sections: map[string]string{"objective": "Finish the active prerequisite."}})
	archivedTicket := e.create(t, "Archived dependent", CreateOptions{
		DependsOn: []string{activeDependency},
		Sections:  map[string]string{"objective": "Finish the archived work."},
	})
	if _, err := Close(e.st, archivedTicket, CloseOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(e.st, archivedTicket); err != nil {
		t.Fatal(err)
	}

	result, err := List(e.st, ListOptions{
		ArchivedOnly: true, States: []string{"all"}, DependencyView: "all",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || len(result.Items[0].Dependencies) != 1 {
		t.Fatalf("archived dependency projection: %+v", result)
	}
	dependency := result.Items[0].Dependencies[0]
	if dependency.ID != activeDependency || dependency.Missing || dependency.Archived || dependency.Satisfied {
		t.Fatalf("active dependency was resolved incorrectly: %+v", dependency)
	}
}
