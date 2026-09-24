package cli

// commandMetadata is the CLI's small command vocabulary. Handler dispatch
// remains explicit, while classifications shared by parsing, object-first
// normalization, and repository persistence live here.
type commandMetadata struct {
	name                   string
	aliases                []string
	objectFirst            bool
	mutation               bool
	acceptsInvocationInput bool
}

var cliCommands = []commandMetadata{
	{name: "init"},
	{name: "create", aliases: []string{"new", "add"}, mutation: true, acceptsInvocationInput: true},
	{name: "delete", objectFirst: true, mutation: true},
	{name: "archive", objectFirst: true, mutation: true},
	{name: "unarchive", objectFirst: true, mutation: true},
	{name: "bump", objectFirst: true, mutation: true},
	{name: "list", aliases: []string{"ls"}},
	{name: "query"},
	{name: "grep"},
	{name: "graph", objectFirst: true},
	{name: "ready"},
	{name: "next"},
	{name: "wait"},
	{name: "watch"},
	{name: "show", objectFirst: true},
	{name: "edit", objectFirst: true, mutation: true},
	{name: "submit", objectFirst: true, mutation: true},
	{name: "hold", objectFirst: true, mutation: true},
	{name: "review", objectFirst: true, mutation: true},
	{name: "open", objectFirst: true, mutation: true},
	{name: "state", objectFirst: true, mutation: true},
	{name: "status", objectFirst: true},
	{name: "path", objectFirst: true},
	{name: "update", objectFirst: true, mutation: true, acceptsInvocationInput: true},
	{name: "append", objectFirst: true, mutation: true, acceptsInvocationInput: true},
	{name: "claim", objectFirst: true, mutation: true},
	{name: "release", objectFirst: true, mutation: true, acceptsInvocationInput: true},
	{name: "reassign", objectFirst: true, mutation: true, acceptsInvocationInput: true},
	{name: "close", objectFirst: true, mutation: true, acceptsInvocationInput: true},
	{name: "approve", aliases: []string{"accept"}, objectFirst: true, mutation: true},
	{name: "reject", objectFirst: true, mutation: true, acceptsInvocationInput: true},
	{name: "check"},
	{name: "actor"},
	{name: "info"},
	{name: "version"},
	{name: "help"},
}

var commandIndex = buildCommandIndex()

func buildCommandIndex() map[string]commandMetadata {
	index := make(map[string]commandMetadata, len(cliCommands))
	for _, command := range cliCommands {
		index[command.name] = command
		for _, alias := range command.aliases {
			aliasCommand := command
			aliasCommand.name = command.name
			aliasCommand.aliases = nil
			index[alias] = aliasCommand
		}
	}
	return index
}

func commandInfo(name string) (commandMetadata, bool) {
	command, ok := commandIndex[name]
	return command, ok
}

func canonicalCommand(name string) string {
	if command, ok := commandInfo(name); ok {
		return command.name
	}
	return name
}

func knownCommand(name string) bool {
	_, ok := commandInfo(name)
	return ok
}
