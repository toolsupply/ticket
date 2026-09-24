package cli

import "testing"

func TestJSONRequestedSkipsValuesAndQueryTails(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "title value", args: []string{"create", "--title", "--json=true", "objective"}},
		{name: "import value", args: []string{"create", "--import", "--json=true"}},
		{name: "short title value", args: []string{"create", "-t", "--json=true", "Title"}},
		{name: "actor value", args: []string{"claim", "20260923-12345", "--actor", "--json=true"}},
		{name: "assignee value", args: []string{"list", "--assignee", "--json"}},
		{name: "short message value", args: []string{"close", "20260923-12345", "-m", "--json=true"}},
		{name: "unknown update message then JSON", args: []string{"update", "20260923-12345", "-m", "--json=true"}, want: true},
		{name: "unknown update long message then JSON", args: []string{"update", "20260923-12345", "--message", "--json=true"}, want: true},
		{name: "list query data", args: []string{"list", "-q", "state:open", "--json=true"}},
		{name: "config value", args: []string{"create", "-c", "--json=true"}},
		{name: "leading config value", args: []string{"-c", "--json=true", "create"}},
		{name: "query value followed by JSON", args: []string{"create", "--title", "--query", "--json=true", "Title"}, want: true},
		{name: "short query value followed by JSON", args: []string{"list", "--assignee", "-q", "--json=true"}, want: true},
		{name: "unknown create actor then JSON", args: []string{"create", "--actor", "--json"}, want: true},
		{name: "unknown create state then JSON", args: []string{"create", "--state", "--json"}, want: true},
		{name: "consumer JSON", args: []string{"query", "state:open", "::", "list", "--json=true"}, want: true},
		{name: "producer JSON", args: []string{"query", "--json", "state:open", "::", "list"}, want: true},
		{name: "direct JSON", args: []string{"create", "--json=true", "Title"}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := jsonRequested(test.args); got != test.want {
				t.Fatalf("jsonRequested(%v)=%v, want %v", test.args, got, test.want)
			}
		})
	}
}

func TestJSONDetectionUsesCommandParserMetadata(t *testing.T) {
	tests := []struct {
		name    string
		command string
		arg     string
		kind    flagKind
	}{
		{name: "create import value", command: "create", arg: "--import", kind: kindString},
		{name: "create json boolean", command: "create", arg: "--json", kind: kindBool},
		{name: "list query tail", command: "list", arg: "-q", kind: kindTail},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			def := parserOption(test.command, test.arg)
			if def == nil || def.kind != test.kind {
				t.Fatalf("parserOption(%q, %q)=%v, want kind %v", test.command, test.arg, def, test.kind)
			}
		})
	}
	if def := parserOption("create", "--not-a-real-option"); def != nil {
		t.Fatalf("unknown option metadata=%v, want nil", def)
	}
}
