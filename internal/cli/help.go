// Human and machine readable help for the supported commands.
package cli

import (
	"fmt"
	"io"
	"strings"

	"ticket/internal/contract"
)

const topLevelHelp = `
A repository-local task manager from https://github.com/toolsupply/ticket

Usage:
  ticket <command> [options]

Getting started:
  init       Initialize ./tickets (or TICKET_ROOT)
  create     Create a ticket

Finding work:
  list       List tickets
  grep       Find tickets by text or regular expression
  ready      Show ready work
  next       Select the next ready ticket
  wait       Wait for ready work
  show       Show a ticket
  status     Show concise ticket status

Working on tickets:
  claim      Claim available work
  edit       Edit a ticket in your editor
  update     Update a ticket
  bump       Raise a ticket's priority
  release    Release ownership

Lifecycle:
  open       Make a ticket active
  hold       Pause active work
  close      Mark a ticket completed
  reject     Abandon or decline a ticket

Review:
  submit     Send implementation for review
  approve    Send reviewed work for human signoff
  accept     Alias for approve

Maintenance:
  delete     Permanently delete tickets
  upgrade    Update the Linux ticket executable
  check      Validate the repository

Run 'ticket help <command>' or 'ticket <command> -h' for command-specific help.

`

func globalHelpOptions() []contract.Flag {
	return []contract.Flag{
		{Name: "--actor", Kind: "token", Description: "Actor for mutations; overrides TICKET_ACTOR."},
		{Name: "-j, --json", Kind: "bool", Description: "Use compact JSON output for success and errors."},
		{Name: "-h, --help", Kind: "bool", Description: "Show help for this command."},
	}
}

func helpPayload(name string) (map[string]any, *contract.Command, bool) {
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
	case "init", "create", "delete", "list", "grep", "ready", "show", "edit", "status", "path", "check", "version", "upgrade", "help":
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
			fmt.Fprintln(ctx.stdout, "  TICKET_EDITOR, then VISUAL, then EDITOR.")
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
		"edit":    "[ID]",
		"submit":  "[ID]",
		"hold":    "[ID]",
		"open":    "[ID] [HANDOFF]",
		"status":  "[ID]",
		"path":    "[ID]",
		"update":  "[ID]",
		"claim":   "[ID]",
		"release": "[ID]",
		"close":   "[ID...|STATE...|all]",
		"approve": "[ID...|review|all]",
		"reject":  "[ID] [OUTCOME]",
		"help":    "[COMMAND]",
		"upgrade": "",
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
	if name == "new" {
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
