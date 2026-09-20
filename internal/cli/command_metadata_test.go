package cli

import (
	"testing"

	"github.com/toolsupply/ticket/internal/contract"
)

func TestCommandMetadataMatchesContractAndAliases(t *testing.T) {
	canonical := make(map[string]bool, len(contract.Commands))
	for _, command := range contract.Commands {
		metadata, ok := commandInfo(command.Name)
		if !ok {
			t.Errorf("contract command %q is missing CLI metadata", command.Name)
			continue
		}
		if metadata.name != command.Name {
			t.Errorf("contract command %q resolves to %q", command.Name, metadata.name)
		}
		canonical[command.Name] = true
	}
	for _, metadata := range cliCommands {
		if !canonical[metadata.name] {
			t.Errorf("CLI metadata command %q is missing from contract.Commands", metadata.name)
		}
		for _, alias := range metadata.aliases {
			resolved, ok := commandInfo(alias)
			if !ok || resolved.name != metadata.name {
				t.Errorf("alias %q does not resolve to %q", alias, metadata.name)
			}
		}
	}
}

func TestCommandMetadataClassifications(t *testing.T) {
	tests := []struct {
		name        string
		canonical   string
		objectFirst bool
		mutation    bool
		input       bool
	}{
		{name: "create", canonical: "create", mutation: true, input: true},
		{name: "new", canonical: "create", mutation: true, input: true},
		{name: "add", canonical: "create", mutation: true, input: true},
		{name: "delete", canonical: "delete", objectFirst: true, mutation: true},
		{name: "bump", canonical: "bump", objectFirst: true, mutation: true},
		{name: "list", canonical: "list"},
		{name: "ls", canonical: "list"},
		{name: "show", canonical: "show", objectFirst: true},
		{name: "edit", canonical: "edit", objectFirst: true, mutation: true},
		{name: "submit", canonical: "submit", objectFirst: true, mutation: true},
		{name: "hold", canonical: "hold", objectFirst: true, mutation: true},
		{name: "open", canonical: "open", objectFirst: true, mutation: true},
		{name: "review", canonical: "review", objectFirst: true, mutation: true},
		{name: "state", canonical: "state", objectFirst: true, mutation: true},
		{name: "status", canonical: "status", objectFirst: true},
		{name: "path", canonical: "path", objectFirst: true},
		{name: "update", canonical: "update", objectFirst: true, mutation: true, input: true},
		{name: "claim", canonical: "claim", objectFirst: true, mutation: true},
		{name: "release", canonical: "release", objectFirst: true, mutation: true, input: true},
		{name: "close", canonical: "close", objectFirst: true, mutation: true, input: true},
		{name: "approve", canonical: "approve", objectFirst: true, mutation: true},
		{name: "accept", canonical: "approve", objectFirst: true, mutation: true},
		{name: "reject", canonical: "reject", objectFirst: true, mutation: true, input: true},
		{name: "init", canonical: "init"},
		{name: "grep", canonical: "grep"},
		{name: "ready", canonical: "ready"},
		{name: "next", canonical: "next"},
		{name: "wait", canonical: "wait"},
		{name: "check", canonical: "check"},
		{name: "actor", canonical: "actor"},
		{name: "version", canonical: "version"},
		{name: "help", canonical: "help"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata, ok := commandInfo(test.name)
			if !ok {
				t.Fatalf("command has no metadata")
			}
			if metadata.name != test.canonical || metadata.objectFirst != test.objectFirst || metadata.mutation != test.mutation || metadata.acceptsInvocationInput != test.input {
				t.Fatalf("metadata=%+v, want canonical=%q objectFirst=%t mutation=%t input=%t", metadata, test.canonical, test.objectFirst, test.mutation, test.input)
			}
		})
	}
}
