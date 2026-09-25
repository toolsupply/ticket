package cli

import (
	"strings"
	"testing"

	"github.com/toolsupply/ticket/internal/domain"
)

func TestParserQueryTailConsumesRemainingTokens(t *testing.T) {
	p := &parser{}
	help, helpSeen := false, false
	p.help = &help
	p.helpSeen = &helpSeen
	var query []string
	var json bool
	p.boolValue("json", &json)
	registerQueryTail(p, &query)
	p.alias("j", "json")
	if err := p.parse([]string{"-j", "-q", "state:open", "--not-an-option", "-j"}); err != nil {
		t.Fatalf("parse query tail: %v", err)
	}
	if !json || !equalStrings(query, []string{"state:open", "--not-an-option", "-j"}) {
		t.Fatalf("json=%v query=%q", json, query)
	}
}

func TestParserQueryTailRejectsInlineValue(t *testing.T) {
	p := &parser{}
	help, helpSeen := false, false
	p.help = &help
	p.helpSeen = &helpSeen
	var query []string
	registerQueryTail(p, &query)
	if err := p.parse([]string{"--query=state:open"}); err == nil || !strings.Contains(err.Error(), "inline value") {
		t.Fatalf("inline query accepted: %v", err)
	}
}

func TestEmptyQueryTailIsRejected(t *testing.T) {
	for _, flag := range []string{"-q", "--query"} {
		t.Run(flag, func(t *testing.T) {
			p := &parser{}
			help, helpSeen := false, false
			p.help = &help
			p.helpSeen = &helpSeen
			var query []string
			registerQueryTail(p, &query)
			if err := p.parse([]string{flag}); err != nil {
				t.Fatalf("parse %s: %v", flag, err)
			}
			if _, err := prepareTargetSpec(nil, query, true, targetSpecOptions{}); err == nil || !strings.Contains(err.Error(), "requires at least one TQL token") {
				t.Fatalf("empty %s accepted: %v", flag, err)
			}
		})
	}
}

func TestPrepareTargetSpecClassifiesIDsLegacyAndTQL(t *testing.T) {
	opts := targetSpecOptions{AllowLegacyStates: true, AllowLegacyAll: true}
	tests := []struct {
		name        string
		positionals []string
		query       []string
		querySet    bool
		refs        []string
		states      []string
		hasQuery    bool
		wantErr     string
	}{
		{name: "query", positionals: []string{"state:open", "or", "ready"}, hasQuery: true},
		{name: "ID prefix query", positionals: []string{"id:202609"}, hasQuery: true},
		{name: "ID comparison query", positionals: []string{"id", "lt", "202609"}, hasQuery: true},
		{name: "ids plus query", positionals: []string{"20260923-12345", "state:open"}, refs: []string{"20260923-12345"}, hasQuery: true},
		{name: "legacy states", positionals: []string{"open", "hold"}, states: []string{"open", "hold"}},
		{name: "id plus legacy", positionals: []string{"20260923-12345", "open"}, refs: []string{"20260923-12345"}, states: []string{"open"}},
		{name: "tail query", positionals: []string{"20260923-12345"}, query: []string{"state:open"}, querySet: true, refs: []string{"20260923-12345"}, hasQuery: true},
		{name: "malformed ID", positionals: []string{"20260923-0000x"}, wantErr: "valid ticket ID"},
		{name: "mixed legacy and query", positionals: []string{"open", "state:review"}, wantErr: "Do not mix legacy"},
		{name: "all plus ID", positionals: []string{"20260923-12345", "all"}, wantErr: "cannot be combined"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := prepareTargetSpec(test.positionals, test.query, test.querySet, opts)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error=%v, want substring %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("prepare: %v", err)
			}
			if !equalStrings(got.ExplicitRefs, test.refs) || !equalStrings(got.LegacyStates, test.states) || got.HasQuery != test.hasQuery {
				t.Fatalf("prepared=%+v", got)
			}
		})
	}
}

func TestPrepareTargetSpecPreservesIDLikeQueryValues(t *testing.T) {
	prepared, err := prepareTargetSpec([]string{"tag", "eq", "2026"}, nil, false, targetSpecOptions{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(prepared.ExplicitRefs) != 0 || !prepared.HasQuery {
		t.Fatalf("prepared=%+v", prepared)
	}
	set := domain.QuerySpec{Expr: prepared.Target.Query.Expr}
	if set.Expr == nil {
		t.Fatal("query expression missing")
	}
}

func TestPrepareTargetSpecKeepsBareIDAsExplicitReference(t *testing.T) {
	for _, id := range []string{"20260923-12345", "2026"} {
		prepared, err := prepareTargetSpec([]string{id}, nil, false, targetSpecOptions{})
		if err != nil || !equalStrings(prepared.ExplicitRefs, []string{id}) || prepared.HasQuery {
			t.Errorf("bare ID %q: prepared=%+v err=%v", id, prepared, err)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
