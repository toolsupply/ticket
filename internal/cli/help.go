// Human and machine readable help for the supported commands.
package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
)

const topLevelHelp = `
A repository-local task manager from https://github.com/toolsupply/ticket

Usage:
  ticket <command> [options]

Getting started:
  init       Initialize ./tickets, TICKET_REPOSITORY, or the selected scope repository
  create     Create a ticket
  new        Alias for create

Finding work:
  list       List tickets
  grep       Find tickets by text or regular expression
  next       Select the next eligible ticket
  wait       Wait for eligible work
  show       Show a ticket
  status     Show concise ticket status

Working on tickets:
  claim      Claim available work
  edit       Edit a ticket in your editor
  update     Update a ticket
  bump       Raise a ticket's priority
  release    Release ownership
  actor      Show the effective actor identity

Lifecycle:
  open       Make a ticket active
  hold       Pause active work
  review     Send a ticket to review
  close      Mark a ticket completed
  reject     Abandon or decline a ticket
  state      Force a lifecycle state

Review:
  submit     Send implementation for review
  approve    Send reviewed work for human signoff
  accept     Alias for approve

Maintenance:
  delete     Permanently delete tickets
  check      Validate the repository

Run 'ticket help <command>' or 'ticket <command> -h' for command-specific help.
Run 'ticket help options' for global and common options.

`

func globalHelpOptions() []contract.Flag {
	return []contract.Flag{
		{Name: "--actor", Kind: "token", Description: "Actor identity override; takes precedence over TICKET_ACTOR."},
		{Name: "-c, --config", Kind: "file", Description: "Use an alternate user config file."},
		{Name: "--scope", Kind: "name", Description: "Use a named configuration scope."},
		{Name: "-j, --json", Kind: "bool", Description: "Use compact JSON output for success and errors."},
		{Name: "-h, --help", Kind: "bool", Description: "Show help for this command."},
	}
}

var optionsHelpCommand = contract.Command{
	Name:    "options",
	Summary: "Show global and common CLI options.",
	Options: []contract.Flag{
		{Name: "--scope", Kind: "name", Description: "Use a named configuration scope."},
		{Name: "-c, --config", Kind: "file", Description: "Use an alternate user config file."},
		{Name: "-i, --interactive", Kind: "bool", Description: "Start the line-oriented shell."},
		{Name: "-j, --json", Kind: "bool", Description: "Use compact JSON output for success and errors."},
		{Name: "-h, --help", Kind: "bool", Description: "Show help for this command."},
		{Name: "--debug", Kind: "bool", Description: "Print a stack trace if an unexpected internal error occurs; no effect on successful commands."},
	},
	Examples: []string{
		"ticket --config ~/.config/ticket/config.json list",
		"ticket --scope work next -j",
		"ticket help create -j",
	},
}

func helpPayload(name string) (map[string]any, *contract.Command, bool) {
	if name == "options" {
		return map[string]any{"command": optionsHelpCommand.Name, "summary": optionsHelpCommand.Summary, "options": optionsHelpCommand.Options, "examples": optionsHelpCommand.Examples}, &optionsHelpCommand, true
	}
	for i := range contract.Commands {
		command := &contract.Commands[i]
		if command.Name != name {
			continue
		}
		options := make([]contract.Flag, 0, len(command.Options)+len(globalHelpOptions()))
		var messageOptions []contract.Flag
		for _, option := range command.Options {
			if option.Name == "-m, --message" {
				messageOptions = append(messageOptions, option)
				continue
			}
			options = append(options, option)
		}
		for _, option := range globalHelpOptions() {
			if commandHasNoActor(command.Name) && option.Name == "--actor" {
				continue
			}
			if option.Name == "-j, --json" {
				options = append(options, messageOptions...)
			}
			options = append(options, option)
		}
		return map[string]any{"command": command.Name, "summary": command.Summary, "options": options, "examples": command.Examples}, command, true
	}
	return nil, nil, false
}

func commandHasNoActor(name string) bool {
	switch name {
	case "init", "create", "delete", "list", "grep", "ready", "show", "edit", "status", "path", "state", "check", "version", "actor", "help":
		return true
	default:
		return false
	}
}

func emitHelpCommand(ctx *commandContext, name string) error {
	payload, command, ok := helpPayload(name)
	if !ok {
		return contract.NewError(contract.ErrInvalidArgument, "Unknown command: "+name+".", nil)
	}
	if !ctx.json {
		fmt.Fprintln(ctx.stdout)
		fmt.Fprintln(ctx.stdout, command.Name+" - "+command.Summary)
		fmt.Fprintln(ctx.stdout)
		fmt.Fprintln(ctx.stdout, "Usage:")
		fmt.Fprintf(ctx.stdout, "  %s\n\n", commandUsage(command.Name))
		fmt.Fprintln(ctx.stdout, "Options:")
		renderHelpOptions(ctx.stdout, payload["options"].([]contract.Flag))
		if len(command.Examples) > 0 {
			fmt.Fprintln(ctx.stdout)
			fmt.Fprintln(ctx.stdout, "Examples:")
			for _, example := range command.Examples {
				fmt.Fprintf(ctx.stdout, "  $ %s\n", example)
			}
		}
		if command.Name == "create" || command.Name == "edit" {
			fmt.Fprintln(ctx.stdout)
			fmt.Fprintln(ctx.stdout, "Editor:")
			fmt.Fprintln(ctx.stdout, "  TICKET_EDITOR, config.editor, VISUAL, EDITOR, then the platform default.")
			fmt.Fprintln(ctx.stdout, "  Defaults to vi on Unix and notepad.exe on Windows.")
		}
		fmt.Fprintln(ctx.stdout)
		return nil
	}
	return emitJSON(ctx.stdout, payload)
}

func commandUsage(name string) string {
	operands := map[string]string{
		"create":  "TITLE [OBJECTIVE]",
		"delete":  "ID...",
		"bump":    "[ID]",
		"list":    "[STATE...|ID...]",
		"grep":    "EXPRESSION...",
		"next":    "[open|review]",
		"wait":    "[open|review]",
		"show":    "[ID]",
		"actor":   "",
		"edit":    "[ID]",
		"submit":  "[ID]",
		"hold":    "[ID]",
		"review":  "[ID]",
		"open":    "[ID] [HANDOFF]",
		"state":   "ID STATE",
		"status":  "[ID]",
		"path":    "[ID]",
		"update":  "[ID]",
		"claim":   "[ID]",
		"release": "[ID]",
		"close":   "[ID...|STATE...|all]",
		"approve": "[ID...|review|all]",
		"reject":  "[ID] [OUTCOME]",
		"help":    "[COMMAND]",
	}
	if name == "options" {
		return "ticket help options"
	}
	usage := "ticket " + name + " [options]"
	if operand := operands[name]; operand != "" {
		usage += " " + operand
	}
	return usage
}

func renderHelpOptions(w io.Writer, options []contract.Flag) {
	visible := make([]contract.Flag, 0, len(options))
	nameWidth := 0
	for _, option := range options {
		if isPositionalHelpOption(option) {
			continue
		}
		visible = append(visible, option)
		if len(option.Name) > nameWidth {
			nameWidth = len(option.Name)
		}
	}
	const indent = 2
	const gap = 2
	for _, option := range visible {
		fmt.Fprintf(w, "%s%-*s%s%s\n", strings.Repeat(" ", indent), nameWidth, option.Name, strings.Repeat(" ", gap), option.Description)
	}
}

func isPositionalHelpOption(option contract.Flag) bool {
	return strings.Contains(option.Name, "(positional)")
}

func emitTopLevelHelp(ctx *commandContext) error {
	if ctx.json {
		commands := make([]map[string]string, 0, len(contract.Commands))
		for _, command := range contract.Commands {
			if command.Name == "ready" {
				continue
			}
			commands = append(commands, map[string]string{"name": command.Name, "summary": command.Summary})
		}
		return emitJSON(ctx.stdout, map[string]any{"commands": commands})
	}
	_, err := fmt.Fprint(ctx.stdout, topLevelHelp)
	return err
}

func emitTopLevelVersion(ctx *commandContext) error {
	if ctx.json {
		return emitJSON(ctx.stdout, versionOutput{
			Version:        Version,
			APIVersion:     APIVersion,
			StorageVersion: StorageVersion,
			Commit:         Commit,
		})
	}
	_, err := fmt.Fprintf(ctx.stdout, "ticket %s (api %d, storage %d)\n", Version, APIVersion, StorageVersion)
	return err
}

func cmdHelp(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "help")
	}
	if len(p.positionals) == 0 {
		return emitTopLevelHelp(ctx)
	}
	if len(p.positionals) > 1 {
		return contract.NewError(contract.ErrInvalidArgument, "Command help accepts at most one command name.", nil)
	}
	name := p.positionals[0]
	if name == "new" || name == "add" {
		name = "create"
	} else if name == "ls" {
		name = "list"
	} else if name == "accept" {
		name = "approve"
	}
	_, command, ok := helpPayload(name)
	if !ok {
		return contract.NewError(contract.ErrInvalidArgument, "Unknown command: "+p.positionals[0]+".", nil)
	}
	return emitHelpCommand(ctx, command.Name)
}
