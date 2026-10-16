//go:build ignore && goutilsmigration

package cli

// This migration-only candidate uses the finalized Ticket v0.2.4 source
// revision 3b7e40cb90a9e9e6c63b40a07693026eefc0ffb3 and the frozen Phase 0.1
// oracle. The application's existing parser remains the production router.

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	gocli "github.com/toolsupply/goutils/cli"
	"github.com/toolsupply/ticket/internal/contract"
)

type migrationArity struct {
	min int
	max int
}

// migrationCandidateArities records positional ranges declared by the
// candidate schema. The legacy side derives its independent baseline from
// commandUsage; conditional invocation-input forms remain blockers.
var migrationCandidateArities = map[string]migrationArity{
	"init":      {0, 0},
	"create":    {0, 2},
	"delete":    {1, -1},
	"archive":   {0, -1},
	"unarchive": {0, 1},
	"bump":      {0, 1},
	"list":      {0, -1},
	"query":     {0, -1},
	"grep":      {1, -1},
	"graph":     {0, -1},
	"ready":     {0, 1},
	"next":      {0, 1},
	"wait":      {0, 1},
	"watch":     {0, 0},
	"show":      {0, 1},
	"edit":      {0, 1},
	"submit":    {0, 1},
	"hold":      {0, 1},
	"review":    {0, 1},
	"open":      {0, 2},
	"state":     {2, 2},
	"status":    {0, 1},
	"path":      {0, 1},
	"update":    {0, 1},
	"append":    {1, 2},
	"claim":     {0, 1},
	"release":   {0, 1},
	"reassign":  {0, 2},
	"close":     {0, -1},
	"approve":   {0, -1},
	"reject":    {0, 2},
	"check":     {0, 0},
	"actor":     {0, 0},
	"info":      {0, 0},
	"version":   {0, 0},
}

func goutilsMigrationCandidate() *gocli.App {
	root := &gocli.Command{}
	app := &gocli.App{
		Name:    "ticket",
		Summary: "A repository-local task manager from https://github.com/toolsupply/ticket",
		Root:    root,
	}
	for _, metadata := range cliCommands {
		// Goutils owns a compatible built-in help route. It reserves "help"
		// as a root command, so Ticket's help route is represented by that
		// built-in rather than by a fake child command.
		if metadata.name == "help" {
			continue
		}
		spec, ok := migrationCommandSpec(metadata.name)
		if !ok {
			panic("missing contract command: " + metadata.name)
		}
		arity, ok := migrationCandidateArities[metadata.name]
		if !ok {
			panic("missing positional arity: " + metadata.name)
		}
		parser := parserForCommand(metadata.name)
		command := &gocli.Command{
			Name:     metadata.name,
			Aliases:  migrationAliases(metadata),
			Summary:  spec.Summary,
			Group:    migrationGroup(metadata.name),
			Args:     gocli.Args{Min: arity.min, Max: arity.max, Usage: migrationUsage(metadata.name)},
			Options:  migrationOptions(parser, spec),
			Examples: append([]string(nil), spec.Examples...),
		}
		if metadata.name == "create" || metadata.name == "edit" {
			command.HelpSections = []gocli.HelpSection{{
				Heading: "Editor",
				Body:    "TICKET_EDITOR, config.editor, VISUAL, EDITOR, then the platform default.\nDefaults to vi on Unix and notepad.exe on Windows.",
			}}
		}
		root.Commands = append(root.Commands, command)
	}
	sort.SliceStable(root.Commands, func(i, j int) bool {
		return migrationCommandOrder(root.Commands[i].Name) < migrationCommandOrder(root.Commands[j].Name)
	})
	return app
}

func migrationCommandSpec(name string) (contract.Command, bool) {
	for _, command := range contract.Commands {
		if command.Name == name {
			return command, true
		}
	}
	return contract.Command{}, false
}

func migrationAliases(command commandMetadata) []gocli.Alias {
	aliases := make([]gocli.Alias, 0, len(command.aliases))
	for _, name := range command.aliases {
		alias := gocli.Alias{Name: name}
		switch name {
		case "new":
			alias.Visible, alias.Discover, alias.Summary = true, true, "Alias for create"
		case "accept":
			alias.Visible, alias.Discover, alias.Summary = true, true, "Alias for approve"
		}
		aliases = append(aliases, alias)
	}
	return aliases
}

func migrationGroup(name string) string {
	switch name {
	case "init", "info", "create":
		return "Getting started"
	case "list", "query", "grep", "graph", "ready", "next", "wait", "watch", "show", "status":
		return "Finding work"
	case "claim", "edit", "update", "append", "bump", "release", "reassign", "actor":
		return "Working on tickets"
	case "open", "hold", "review", "close", "reject", "state":
		return "Lifecycle"
	case "submit", "approve":
		return "Review"
	case "delete", "archive", "unarchive", "check":
		return "Maintenance"
	default:
		// Ticket's curated top-level help omits path and version. Goutils has no
		// hidden-command field; these commands stay in the schema and the
		// discovery mismatch is recorded as a blocker.
		return ""
	}
}

func migrationCommandOrder(name string) int {
	order := []string{
		"init", "info", "create",
		"list", "query", "grep", "graph", "ready", "next", "wait", "watch", "show", "status",
		"claim", "edit", "update", "append", "bump", "release", "reassign", "actor",
		"open", "hold", "review", "close", "reject", "state",
		"submit", "approve",
		"delete", "archive", "unarchive", "check",
		"path", "version",
	}
	for i, command := range order {
		if command == name {
			return i
		}
	}
	return len(order)
}

func migrationUsage(name string) string {
	prefix := "ticket " + name + " [options]"
	return strings.TrimSpace(strings.TrimPrefix(commandUsage(name), prefix))
}

func migrationOptions(parser *parser, spec contract.Command) []gocli.Option {
	help := make(map[string]contract.Flag)
	ordered := make([]string, 0, len(spec.Options))
	payload, _, _ := helpPayload(spec.Name)
	if flags, ok := payload["options"].([]contract.Flag); ok {
		for _, flag := range flags {
			name := migrationLongOptionName(flag.Name)
			if name == "" {
				continue
			}
			help[name] = flag
			ordered = append(ordered, name)
		}
	}

	remaining := make([]string, 0, len(parser.defs))
	for name := range parser.defs {
		if !containsMigrationName(ordered, name) {
			remaining = append(remaining, name)
		}
	}
	sort.Strings(remaining)
	ordered = append(ordered, remaining...)

	options := make([]gocli.Option, 0, len(parser.defs))
	for _, name := range ordered {
		def, ok := parser.defs[name]
		if !ok {
			continue
		}
		option := gocli.Option{
			Name:           name,
			Type:           migrationValueType(def.kind, help[name]),
			Repeatable:     def.kind == kindRepeat,
			Tail:           def.kind == kindTail,
			AllowBoolValue: def.kind == kindBool,
			Summary:        help[name].Description,
			Aliases:        migrationOptionAliases(parser, name),
		}
		options = append(options, option)
	}
	return options
}

func migrationValueType(kind flagKind, help contract.Flag) gocli.ValueType {
	switch kind {
	case kindInt:
		return gocli.IntValue
	case kindBool:
		return gocli.BoolValue
	}
	lower := strings.ToLower(help.Kind)
	if strings.Contains(lower, "directory") {
		return gocli.DirectoryValue
	}
	if strings.Contains(lower, "file") {
		return gocli.FileValue
	}
	// Ticket validates enum-like values in application logic, after parsing;
	// treating them as EnumValue here would move policy into the parser.
	return gocli.StringValue
}

func migrationOptionAliases(parser *parser, name string) []string {
	var aliases []string
	for alias, option := range parser.aliases {
		if option == name {
			aliases = append(aliases, alias)
		}
	}
	// The ordinary Ticket parser handles -j as a built-in spelling for its
	// registered --json option instead of recording it in parser.aliases.
	if name == "json" {
		aliases = append(aliases, "j")
	}
	sort.Strings(aliases)
	return aliases
}

func migrationLongOptionName(name string) string {
	for _, part := range strings.Split(name, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "--") {
			return strings.TrimPrefix(part, "--")
		}
	}
	return ""
}

func containsMigrationName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func TestGoutilsMigrationCandidatePassesSchemaValidation(t *testing.T) {
	app := goutilsMigrationCandidate()
	if _, err := gocli.Parse(app, []string{"help"}); err != nil {
		t.Fatalf("candidate schema is invalid: %v", err)
	}
}

func TestGoutilsMigrationCandidateCoversOrdinaryCommandMetadata(t *testing.T) {
	app := goutilsMigrationCandidate()
	got := make(map[string]*gocli.Command, len(app.Root.Commands))
	for _, command := range app.Root.Commands {
		got[command.Name] = command
		if command.Run != nil {
			t.Errorf("candidate command %q unexpectedly dispatches production behavior", command.Name)
		}
	}
	if len(got)+1 != len(cliCommands) {
		t.Fatalf("candidate has %d commands for %d canonical commands; help is represented by the built-in route", len(got), len(cliCommands))
	}
	if app.Version != nil {
		t.Fatal("Ticket's version command must not become Goutils' --version application action")
	}

	for _, metadata := range cliCommands {
		if metadata.name == "help" {
			continue
		}
		command := got[metadata.name]
		if command == nil {
			t.Errorf("candidate is missing canonical command %q", metadata.name)
			continue
		}
		if command.Summary == "" {
			t.Errorf("candidate command %q is missing its summary", metadata.name)
		}
		if command.Args.Usage != migrationUsage(metadata.name) {
			t.Errorf("%s usage = %q, want %q", metadata.name, command.Args.Usage, migrationUsage(metadata.name))
		}
		parser := parserForCommand(metadata.name)
		if len(command.Options) != len(parser.defs) {
			t.Errorf("%s has %d candidate options for %d parser options", metadata.name, len(command.Options), len(parser.defs))
		}
		options := make(map[string]gocli.Option, len(command.Options))
		for _, option := range command.Options {
			options[option.Name] = option
		}
		for name, def := range parser.defs {
			option, ok := options[name]
			if !ok {
				t.Errorf("%s is missing parser option --%s", metadata.name, name)
				continue
			}
			if option.Repeatable != (def.kind == kindRepeat) || option.Tail != (def.kind == kindTail) {
				t.Errorf("%s --%s repeatable=%t tail=%t does not match parser kind %v", metadata.name, name, option.Repeatable, option.Tail, def.kind)
			}
			if want := migrationOptionAliases(parser, name); !reflect.DeepEqual(option.Aliases, want) {
				t.Errorf("%s --%s aliases = %v, want %v", metadata.name, name, option.Aliases, want)
			}
			if def.kind == kindBool && (!option.AllowBoolValue || option.Type != gocli.BoolValue) {
				t.Errorf("%s --%s must allow explicit boolean values", metadata.name, name)
			}
		}
		for _, alias := range metadata.aliases {
			if !hasMigrationAlias(command.Aliases, alias) {
				t.Errorf("%s is missing command alias %q", metadata.name, alias)
			}
		}
	}
	inv, err := gocli.Parse(app, []string{"help", "options"})
	if err != nil || !inv.HelpRequested() || !inv.OptionsHelpRequested() {
		t.Fatalf("built-in options help route = %#v, %v", inv, err)
	}
}

func hasMigrationAlias(aliases []gocli.Alias, name string) bool {
	for _, alias := range aliases {
		if alias.Name == name {
			return true
		}
	}
	return false
}

func TestGoutilsMigrationCandidatePreservesCommandLocalOptionScope(t *testing.T) {
	app := goutilsMigrationCandidate()
	if len(app.Root.Options) != 0 {
		t.Fatalf("candidate root options = %#v, want none", app.Root.Options)
	}
	for _, name := range []string{"list", "init"} {
		command := findMigrationCommand(app, name)
		if command == nil {
			t.Fatalf("candidate %s command is missing", name)
		}
		for _, option := range command.Options {
			if option.Name == "actor" {
				t.Fatalf("Ticket's %s command must not accept --actor", name)
			}
		}
	}
	claim := findMigrationCommand(app, "claim")
	if claim == nil {
		t.Fatal("candidate claim command is missing")
	}
	for _, option := range claim.Options {
		if option.Name == "actor" {
			return
		}
	}
	t.Fatal("Ticket's claim command lost its command-local --actor option")
}

func findMigrationCommand(app *gocli.App, name string) *gocli.Command {
	for _, command := range app.Root.Commands {
		if command.Name == name {
			return command
		}
	}
	return nil
}

func TestGoutilsMigrationCandidateMatchesTicketAliasDiscovery(t *testing.T) {
	app := goutilsMigrationCandidate()
	wantVisible := map[string]map[string]string{
		"create":  {"new": "Alias for create"},
		"approve": {"accept": "Alias for approve"},
	}
	for _, command := range app.Root.Commands {
		want := wantVisible[command.Name]
		for _, alias := range command.Aliases {
			wantSummary, visible := want[alias.Name]
			if alias.Visible != visible || alias.Discover != visible {
				t.Errorf("%s alias %q visible=%t discover=%t, want %t", command.Name, alias.Name, alias.Visible, alias.Discover, visible)
			}
			if visible && alias.Summary != wantSummary {
				t.Errorf("%s alias %q summary = %q, want %q", command.Name, alias.Name, alias.Summary, wantSummary)
			}
		}
	}
}

// migrationGrammar is the shared, parse-level view used to compare Ticket's
// registered parser grammar with the candidate schema. Help prose and display
// annotations (such as file/directory labels) are intentionally outside this
// representation because they do not change which argv values the parsers
// accept.
type migrationGrammar struct {
	commands map[string]migrationCommandGrammar
	aliases  map[string]string
}

type migrationCommandGrammar struct {
	path         string
	aliases      []string
	minArgs      int
	maxArgs      int
	compareArity bool
	options      map[string]migrationOptionGrammar
}

type migrationOptionGrammar struct {
	aliases        []string
	valueKind      string
	repeatable     bool
	allowBoolValue bool
	tail           bool
}

func TestGoutilsMigrationCandidateMatchesTicketParserGrammar(t *testing.T) {
	legacy := ticketParserGrammar()
	candidate := goutilsCandidateGrammar(goutilsMigrationCandidate())
	for _, mismatch := range migrationGrammarMismatches(legacy, candidate) {
		t.Error(mismatch)
	}
}

func TestGoutilsMigrationCandidateArityMismatchIsDetected(t *testing.T) {
	legacy := ticketParserGrammar()
	candidate := goutilsCandidateGrammar(goutilsMigrationCandidate())
	command := candidate.commands["show"]
	command.minArgs++
	candidate.commands["show"] = command

	for _, mismatch := range migrationGrammarMismatches(legacy, candidate) {
		if strings.Contains(mismatch, "command show positional arity") {
			return
		}
	}
	t.Fatal("candidate arity change was not detected for command show")
}

func TestGoutilsMigrationCandidateAcceptsPositionalTQLForGraphAndArchive(t *testing.T) {
	app := goutilsMigrationCandidate()
	positionals := []string{"state:review", "owner:coder"}
	for _, name := range []string{"graph", "archive"} {
		args := append([]string{name}, positionals...)
		invocation, err := gocli.Parse(app, args)
		if err != nil {
			t.Errorf("%s rejects multiple positional TQL tokens: %v", name, err)
			continue
		}
		if invocation.Command == nil || invocation.Command.Name != name {
			t.Errorf("multiple positional TQL selected command %#v, want %q", invocation.Command, name)
		}
		if !reflect.DeepEqual(invocation.Args(), positionals) {
			t.Errorf("%s positional TQL = %v, want %v", name, invocation.Args(), positionals)
		}
	}
}

func ticketParserGrammar() migrationGrammar {
	grammar := migrationGrammar{
		commands: make(map[string]migrationCommandGrammar, len(cliCommands)),
		aliases:  make(map[string]string),
	}
	for _, metadata := range cliCommands {
		arity := ticketUsageArity(metadata.name)
		command := migrationCommandGrammar{
			path:         "/" + metadata.name,
			aliases:      sortedStrings(metadata.aliases),
			minArgs:      arity.min,
			maxArgs:      arity.max,
			compareArity: migrationLegacyArityComparable(metadata),
			options:      make(map[string]migrationOptionGrammar),
		}
		if metadata.name != "help" {
			parser := parserForCommand(metadata.name)
			for name, def := range parser.defs {
				command.options[name] = migrationOptionGrammar{
					aliases:        sortedStrings(migrationOptionAliases(parser, name)),
					valueKind:      migrationParserValueKind(def.kind),
					repeatable:     def.kind == kindRepeat,
					allowBoolValue: def.kind == kindBool,
					tail:           def.kind == kindTail,
				}
			}
		}
		grammar.commands[metadata.name] = command
		for _, alias := range metadata.aliases {
			grammar.aliases[alias] = metadata.name
		}
	}
	return grammar
}

func migrationLegacyArityComparable(metadata commandMetadata) bool {
	if metadata.acceptsInvocationInput {
		return false
	}
	switch metadata.name {
	case "archive", "graph":
		// Ticket accepts positional TQL here, while commandUsage only shows an
		// optional ID. Its parser does not expose an independent arity oracle.
		return false
	default:
		return true
	}
}

func goutilsCandidateGrammar(app *gocli.App) migrationGrammar {
	grammar := migrationGrammar{
		commands: make(map[string]migrationCommandGrammar, len(app.Root.Commands)+1),
		aliases:  make(map[string]string),
	}
	for _, command := range app.Root.Commands {
		canonicalAliases := make([]string, 0, len(command.Aliases))
		for _, alias := range command.Aliases {
			canonicalAliases = append(canonicalAliases, alias.Name)
			grammar.aliases[alias.Name] = command.Name
		}
		entry := migrationCommandGrammar{
			path:         "/" + command.Name,
			aliases:      sortedStrings(canonicalAliases),
			minArgs:      command.Args.Min,
			maxArgs:      command.Args.Max,
			compareArity: true,
			options:      make(map[string]migrationOptionGrammar, len(command.Options)),
		}
		for _, option := range command.Options {
			entry.options[option.Name] = migrationOptionGrammar{
				aliases:        sortedStrings(option.Aliases),
				valueKind:      migrationSchemaValueKind(option.Type),
				repeatable:     option.Repeatable,
				allowBoolValue: option.AllowBoolValue,
				tail:           option.Tail,
			}
		}
		grammar.commands[command.Name] = entry
	}
	// The library reserves help and exposes it as a built-in root route instead
	// of a child *Command. Normalize that route to Ticket's explicit command.
	grammar.commands["help"] = migrationCommandGrammar{
		path:         "/help",
		minArgs:      0,
		maxArgs:      1,
		compareArity: true,
		options:      map[string]migrationOptionGrammar{},
	}
	return grammar
}

// ticketUsageArity derives the documented legacy positional range from
// Ticket's command usage strings, independently of the candidate arity map.
// Brackets mark optional operands and ellipses mark an unbounded maximum.
func ticketUsageArity(name string) migrationArity {
	usage := strings.TrimSpace(strings.TrimPrefix(commandUsage(name), "ticket "+name+" [options]"))
	if usage == "" {
		return migrationArity{}
	}

	var arity migrationArity
	for i := 0; i < len(usage); {
		if usage[i] == ' ' {
			i++
			continue
		}
		optional := usage[i] == '['
		start := i
		if optional {
			depth := 0
			for i < len(usage) {
				switch usage[i] {
				case '[':
					depth++
				case ']':
					depth--
				}
				i++
				if depth == 0 {
					break
				}
			}
		} else {
			for i < len(usage) && usage[i] != ' ' {
				i++
			}
		}
		operand := usage[start:i]
		if strings.Contains(operand, "...") {
			if !optional {
				arity.min++
			}
			arity.max = -1
		} else if optional {
			if arity.max >= 0 {
				arity.max++
			}
		} else {
			arity.min++
			if arity.max >= 0 {
				arity.max++
			}
		}
	}
	return arity
}

func migrationParserValueKind(kind flagKind) string {
	switch kind {
	case kindInt:
		return "int"
	case kindBool:
		return "bool"
	default:
		return "string"
	}
}

func migrationSchemaValueKind(kind gocli.ValueType) string {
	switch kind {
	case gocli.IntValue:
		return "int"
	case gocli.BoolValue:
		return "bool"
	case gocli.StringValue, gocli.FileValue, gocli.DirectoryValue:
		// Goutils file/directory kinds affect help and completion metadata, not
		// argv validation; Ticket registers these values as ordinary strings.
		return "string"
	case gocli.EnumValue:
		return "enum"
	default:
		return "unknown"
	}
}

func migrationGrammarMismatches(want, got migrationGrammar) []string {
	var mismatches []string
	add := func(format string, args ...any) {
		mismatches = append(mismatches, fmt.Sprintf(format, args...))
	}
	for _, name := range sortedMapKeys(want.commands) {
		wantCommand := want.commands[name]
		gotCommand, ok := got.commands[name]
		if !ok {
			add("command %s is missing from candidate grammar", name)
			continue
		}
		if wantCommand.path != gotCommand.path {
			add("command %s path = %q, want %q", name, gotCommand.path, wantCommand.path)
		}
		if !reflect.DeepEqual(wantCommand.aliases, gotCommand.aliases) {
			add("command %s aliases = %v, want %v", name, gotCommand.aliases, wantCommand.aliases)
		}
		if wantCommand.compareArity && (wantCommand.minArgs != gotCommand.minArgs || wantCommand.maxArgs != gotCommand.maxArgs) {
			add("command %s positional arity = [%d,%d], want [%d,%d]", name, gotCommand.minArgs, gotCommand.maxArgs, wantCommand.minArgs, wantCommand.maxArgs)
		}
		for _, optionName := range sortedMapKeys(wantCommand.options) {
			wantOption := wantCommand.options[optionName]
			gotOption, ok := gotCommand.options[optionName]
			if !ok {
				add("command %s option --%s is missing from candidate grammar", name, optionName)
				continue
			}
			if !reflect.DeepEqual(wantOption.aliases, gotOption.aliases) {
				add("command %s option --%s aliases = %v, want %v", name, optionName, gotOption.aliases, wantOption.aliases)
			}
			if wantOption.valueKind != gotOption.valueKind {
				add("command %s option --%s value kind = %s, want %s", name, optionName, gotOption.valueKind, wantOption.valueKind)
			}
			if wantOption.repeatable != gotOption.repeatable {
				add("command %s option --%s repeatable = %t, want %t", name, optionName, gotOption.repeatable, wantOption.repeatable)
			}
			if wantOption.allowBoolValue != gotOption.allowBoolValue {
				add("command %s option --%s explicit bool value = %t, want %t", name, optionName, gotOption.allowBoolValue, wantOption.allowBoolValue)
			}
			if wantOption.tail != gotOption.tail {
				add("command %s option --%s consumes rest = %t, want %t", name, optionName, gotOption.tail, wantOption.tail)
			}
		}
		for _, optionName := range sortedMapKeys(gotCommand.options) {
			if _, ok := wantCommand.options[optionName]; !ok {
				add("command %s has unexpected candidate option --%s", name, optionName)
			}
		}
	}
	for _, name := range sortedMapKeys(got.commands) {
		if _, ok := want.commands[name]; !ok {
			add("candidate has unexpected command %s", name)
		}
	}
	for _, alias := range sortedMapKeys(want.aliases) {
		if got.aliases[alias] != want.aliases[alias] {
			add("command alias %q belongs to %q, want %q", alias, got.aliases[alias], want.aliases[alias])
		}
	}
	for _, alias := range sortedMapKeys(got.aliases) {
		if want.aliases[alias] != got.aliases[alias] {
			add("candidate has unexpected command alias %q for %q", alias, got.aliases[alias])
		}
	}
	return mismatches
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
