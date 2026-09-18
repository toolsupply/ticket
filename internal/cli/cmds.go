// Per-command implementations for the M1 command set.
package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"ticket/internal/contract"
	"ticket/internal/domain"
	"ticket/internal/identity"
	"ticket/internal/jsonx"
	"ticket/internal/store"
)

// helpFlag and helpSeen are per-invocation --help state (one
// command per process).
var helpFlag, helpSeen bool

// init ------------------------------------------------------------------

func cmdInit(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "init")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	var target string
	if len(p.positionals) > 0 {
		return contract.NewError(contract.ErrInvalidArgument,
			"Command init accepts no path; use TICKET_REPOSITORY or a selected scope to choose the target.", nil)
	}
	target = store.ConfiguredRoot()
	if target == "" {
		target = ctx.globalOpts.selectedRoot()
	}
	if target == "" {
		target = "./tickets"
	}
	created, err := store.InitRoot(target)
	if err != nil {
		return err
	}
	res := map[string]any{"path": target, "created": created}
	if !ctx.json {
		msg := "initialized " + target
		if !created {
			msg = "repository already initialized: " + target
		}
		fmt.Fprintln(ctx.stdout, msg)
		return nil
	}
	return emitJSON(ctx.stdout, res)
}

// create ----------------------------------------------------------------

type createInput struct {
	Title     string            `json:"title"`
	Priority  *int              `json:"priority"`
	Tags      []string          `json:"tags"`
	Parent    string            `json:"parent"`
	DependsOn []string          `json:"depends_on"`
	Sections  map[string]string `json:"sections"`
}

func cmdCreate(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var title string
	var hasTitle bool
	priority := 2
	var hasPriority bool
	var tags, dependsOn []string
	var parent string
	var inputPath string
	var edit bool
	p.flag("title", kindString, func(v string) error { title, hasTitle = v, true; return nil }, false)
	p.alias("t", "title")
	p.intValue("priority", &priority, &hasPriority)
	p.alias("p", "priority")
	p.repeat("tag", &tags)
	p.str("parent", &parent)
	p.repeat("depends-on", &dependsOn)
	p.str("input", &inputPath)
	p.boolValue("edit", &edit)
	p.alias("e", "edit")
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "create")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	if edit && ctx.json {
		return contract.NewError(contract.ErrInvalidArgument, "--edit is for interactive human use and cannot be combined with JSON mode.", nil)
	}
	stdinObjective := len(p.positionals) > 0 && p.positionals[len(p.positionals)-1] == "-"
	if stdinObjective {
		p.positionals = p.positionals[:len(p.positionals)-1]
	}
	if len(p.positionals) > 2 {
		return contract.NewError(contract.ErrInvalidArgument,
			"Command create accepts a title and optional objective.", nil)
	}
	if len(p.positionals) == 1 {
		if hasTitle {
			return contract.NewError(contract.ErrInvalidArgument,
				"Create accepts either a positional title or --title, not both.", nil)
		}
		title = p.positionals[0]
	} else if len(p.positionals) == 2 {
		if hasTitle {
			return contract.NewError(contract.ErrInvalidArgument,
				"Create accepts either positional title and objective or --title, not both.", nil)
		}
		title = p.positionals[0]
	}
	opts := domain.CreateOptions{Title: title, Priority: priority}
	var positionalObjective string
	if len(p.positionals) == 2 {
		positionalObjective = p.positionals[1]
		opts.Sections = map[string]string{"objective": positionalObjective}
	}
	if hasPriority {
		opts.Priority = priority
	}
	opts.Tags = tags
	opts.Parent = parent
	opts.DependsOn = dependsOn
	flagUsed := hasTitle || len(p.positionals) > 0 || stdinObjective || hasPriority || len(tags) > 0 || parent != "" || len(dependsOn) > 0 || edit
	if inputPath != "" && flagUsed {
		return contract.NewError(contract.ErrInvalidArgument,
			"Create flags cannot be combined with --input.", nil)
	}
	if inputPath != "" {
		in, err := readCreateInput(inputPath)
		if err != nil {
			return err
		}
		opts.Title = in.Title
		if in.Priority != nil {
			opts.Priority = *in.Priority
		}
		opts.Tags = in.Tags
		opts.Parent = in.Parent
		opts.DependsOn = in.DependsOn
		opts.Sections = in.Sections
	} else if stdinObjective {
		data, err := readStdin(maxInputBytes)
		if err != nil {
			return err
		}
		opts.Sections = map[string]string{"objective": string(data)}
	} else if edit {
		// Editor mode owns the interaction with stdin; do not interpret a
		// confirmation response or editor input as piped Markdown.
		opts.Body = ""
	} else {
		body, err := readPipedCreateBody()
		if err != nil {
			return err
		}
		if positionalObjective != "" && body != "" {
			return contract.NewError(contract.ErrInvalidArgument,
				"A positional objective cannot be combined with piped Markdown.", nil)
		}
		opts.Body = body
	}
	opts.Tags = ctx.createTags(opts.Tags)
	if opts.Title == "" && edit {
		opts.Title = "New ticket"
	}
	if opts.Title == "" {
		return contract.NewError(contract.ErrInvalidArgument,
			"A title is required for create.", nil)
	}
	if edit {
		return createWithEditor(ctx, opts)
	}
	return runRepoCommand(ctx, "create", func(st *store.Store) (any, error) {
		res, err := domain.Create(st, opts)
		if err != nil {
			return nil, err
		}
		return res, nil
	})
}

func cmdNew(ctx *commandContext, args []string) error {
	if len(args) == 0 {
		return cmdCreate(ctx, []string{"--edit"})
	}
	return cmdCreate(ctx, args)
}

func cmdDelete(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "delete")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	refs, all, err := closeTargets(p.positionals, false, "delete")
	if err != nil {
		return err
	}
	if all || len(refs) == 0 {
		return contract.NewError(contract.ErrInvalidArgument,
			"Command delete requires at least one ticket ID.", nil)
	}
	return runRepoCommand(ctx, "delete", func(st *store.Store) (any, error) {
		if len(refs) == 1 {
			return domain.Delete(st, refs[0])
		}
		return domain.DeleteMany(st, refs)
	})
}

func cmdBump(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "bump")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	ref, err := optionalTicketRef(p, "bump")
	if err != nil {
		return err
	}
	actor := ctx.actor
	if actor == "" {
		actor = os.Getenv("TICKET_ACTOR")
	}
	return runRepoCommand(ctx, "bump", func(st *store.Store) (any, error) {
		ref, err = resolveTicketRef(st, ctx, ref, "bump")
		if err != nil {
			return nil, err
		}
		ticket, err := domain.ReadTicket(st, ref)
		if err != nil {
			return nil, err
		}
		priority := ticket.Priority
		if priority > 0 {
			priority--
		}
		return domain.Update(st, ref, domain.UpdateOptions{
			Set: map[string]any{"priority": priority}, Actor: actor,
		})
	})
}

func readPipedCreateBody() (string, error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", contract.NewError(contract.ErrIOError,
			"Could not inspect standard input: "+err.Error(), nil)
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", nil
	}
	data, err := readStdin(2 << 20)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func readCreateInput(path string) (*createInput, error) {
	if path != "-" {
		return nil, contract.NewError(contract.ErrInvalidArgument,
			"Structured input must be provided with --input -.", nil)
	}
	data, err := readStdin(2 << 20)
	if err != nil {
		return nil, contract.NewError(contract.ErrFileTooLarge,
			"Input could not be read: "+err.Error(), nil)
	}
	if len(data) > 2<<20 {
		return nil, contract.NewError(contract.ErrFileTooLarge,
			"Input exceeds the 2 MiB limit.", nil)
	}
	var in createInput
	if err := jsonx.Decode(data, &in); err != nil {
		if errors.Is(err, jsonx.ErrUnknownField) {
			return nil, contract.NewError(contract.ErrInvalidArgument,
				"Unknown key in --input JSON: "+err.Error(), nil)
		}
		return nil, contract.NewError(contract.ErrInvalidJSON,
			"Invalid --input JSON: "+err.Error(), nil)
	}
	return &in, nil
}

// list ------------------------------------------------------------------

func cmdList(ctx *commandContext, args []string, alias bool) error {
	args = normalizeListAliasArgs(args)
	bareHumanList := !ctx.json && len(args) == 0
	p := &parser{}
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var assignee, parent, fields string
	var states []string
	var ids []string
	var withoutTags []string
	var tags []string
	var all, long, byID, byModified bool
	var markdown bool
	p.boolValue("all", &all)
	p.boolValue("long", &long)
	p.boolValue("by-id", &byID)
	p.boolValue("by-modified", &byModified)
	p.boolValue("markdown", &markdown)
	p.alias("a", "all")
	p.alias("l", "long")
	p.alias("t", "by-id")
	p.alias("u", "by-modified")
	p.repeat("state", &states)
	p.alias("s", "state")
	p.repeat("tag", &tags)
	p.repeat("without-tag", &withoutTags)
	p.str("assignee", &assignee)
	var unassigned bool
	p.boolValue("unassigned", &unassigned)
	p.str("parent", &parent)
	limitVal := 0
	hasLimitVal := false
	p.intValue("limit", &limitVal, &hasLimitVal)
	offsetVal := 0
	hasOffsetVal := false
	p.intValue("offset", &offsetVal, &hasOffsetVal)
	priorityVal := 0
	hasPriority := false
	p.intValue("priority", &priorityVal, &hasPriority)
	p.str("fields", &fields)
	if err := p.parse(args); err != nil {
		return err
	}
	ctx.markdown = markdown
	if helpFlag {
		return emitHelpCommand(ctx, "list")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	if len(p.positionals) > 0 {
		for _, positional := range p.positionals {
			if isTicketState(positional) {
				states = append(states, positional)
				continue
			}
			if !looksLikeTicketRef(positional) {
				return contract.NewError(contract.ErrInvalidArgument,
					"State must be open, hold, review, signoff, completed, rejected, all, or a ticket ID.", nil)
			}
			ids = append(ids, positional)
		}
	}
	if all {
		if len(ids) > 0 || (len(states) > 0 && !(len(states) == 1 && states[0] == "all")) {
			return contract.NewError(contract.ErrInvalidArgument, "--all conflicts with ticket IDs or --state.", nil)
		}
		states = []string{"all"}
	}
	opts := domain.ListOptions{
		IDs: ids, States: states, Tags: tags, WithoutTags: withoutTags,
		Assignee: assignee, Unassigned: unassigned, Parent: parent,
	}
	if bareHumanList {
		opts.State = "all"
		opts.NonterminalOnly = true
		opts.Unlimited = true
	}
	if hasLimitVal {
		opts.Limit = limitVal
		opts.LimitSet = true
	}
	if hasOffsetVal {
		opts.Offset = offsetVal
	}
	if hasPriority {
		opts.Priority = &priorityVal
	}
	if fields != "" {
		parts := strings.Split(fields, ",")
		for _, part := range parts {
			if part == "" {
				return contract.NewError(contract.ErrInvalidArgument,
					"Fields list contains an empty entry.", nil)
			}
		}
		opts.Fields = parts
	}
	// Human list output is a work log view: the tickets touched most
	// recently are the most useful entries to put in front of a person.
	// JSON keeps the domain's deterministic priority/ID order for agents and
	// scripts unless an explicit sort is requested.
	if !ctx.json && !byID && !byModified {
		byModified = true
	}
	if !ctx.json && len(states) == 1 && states[0] == "all" && !hasLimitVal {
		opts.Unlimited = true
	}
	switch {
	case byModified:
		opts.Sort = "modified_desc"
	case byID:
		opts.Sort = "id_desc"
	}
	return runRepoCommand(ctx, "list", func(st *store.Store) (any, error) {
		return domain.List(st, opts)
	})
}

func normalizeListAliasArgs(args []string) []string {
	var normalized []string
	for _, arg := range args {
		switch arg {
		case "-ltua":
			normalized = append(normalized, "-l", "-t", "-u", "-a")
		case "-lt":
			normalized = append(normalized, "-l", "-t")
		case "-ltu":
			normalized = append(normalized, "-l", "-t", "-u")
		case "-la", "-al":
			normalized = append(normalized, "-l", "-a")
		default:
			normalized = append(normalized, arg)
		}
	}
	return normalized
}

// grep -----------------------------------------------------------------

func cmdGrep(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "grep")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	if len(p.positionals) == 0 {
		return contract.NewError(contract.ErrInvalidArgument,
			"Command grep requires at least one expression.", nil)
	}
	return runRepoCommand(ctx, "grep", func(st *store.Store) (any, error) {
		return domain.Grep(st, p.positionals...)
	})
}

// status ----------------------------------------------------------------

func cmdStatus(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var markdown bool
	p.boolValue("markdown", &markdown)
	if err := p.parse(args); err != nil {
		return err
	}
	ctx.markdown = markdown
	if helpFlag {
		return emitHelpCommand(ctx, "status")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	ref, err := optionalTicketRef(p, "status")
	if err != nil {
		return err
	}
	return runRepoCommand(ctx, "status", func(st *store.Store) (any, error) {
		ref, err = resolveTicketRef(st, ctx, ref, "status")
		if err != nil {
			return nil, err
		}
		return domain.Status(st, ref)
	})
}

func cmdReady(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var tags, withoutTags []string
	var parent, fields string
	var limit, priority int
	var hasLimit, hasPriority bool
	var markdown bool
	p.boolValue("markdown", &markdown)
	p.repeat("tag", &tags)
	p.repeat("without-tag", &withoutTags)
	p.str("parent", &parent)
	p.intValue("limit", &limit, &hasLimit)
	p.intValue("priority", &priority, &hasPriority)
	p.str("fields", &fields)
	if err := p.parse(args); err != nil {
		return err
	}
	ctx.markdown = markdown
	if helpFlag {
		return emitHelpCommand(ctx, "ready")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	if err := p.requireNoPositionals("ready"); err != nil {
		return err
	}
	tags = ctx.workTags(tags)
	opts := domain.ListOptions{Tags: tags, WithoutTags: withoutTags, Parent: parent}
	if hasLimit {
		opts.Limit = limit
		opts.LimitSet = true
	}
	if hasPriority {
		opts.Priority = &priority
	}
	if fields != "" {
		opts.Fields = strings.Split(fields, ",")
	}
	return runRepoCommand(ctx, "ready", func(st *store.Store) (any, error) {
		return domain.Ready(st, opts)
	})
}

func cmdNext(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var claim bool
	var markdown bool
	var tags []string
	var priority int
	var hasPriority bool
	p.boolValue("claim", &claim)
	p.boolValue("markdown", &markdown)
	p.repeat("tag", &tags)
	p.intValue("priority", &priority, &hasPriority)
	if err := p.parse(args); err != nil {
		return err
	}
	ctx.markdown = markdown
	if helpFlag {
		return emitHelpCommand(ctx, "next")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	tags = ctx.workTags(tags)
	queue, err := workQueue(p.positionals, "next")
	if err != nil {
		return err
	}
	actor := ""
	if claim {
		actor, err = ctx.actorToken()
		if err != nil {
			return err
		}
	}
	opts := domain.NextOptions{Queue: queue, Tags: tags, Claim: claim, Actor: actor}
	if hasPriority {
		opts.Priority = &priority
	}
	return runRepoCommandMode(ctx, "next", claim, func(st *store.Store) (any, error) {
		return domain.NextWithOptions(st, opts)
	})
}

func workQueue(positionals []string, command string) (string, error) {
	if len(positionals) == 0 {
		return "open", nil
	}
	if len(positionals) > 1 || (positionals[0] != "open" && positionals[0] != "review") {
		return "", contract.NewError(contract.ErrInvalidArgument,
			"Command "+command+" accepts only the open or review queue.", nil)
	}
	return positionals[0], nil
}

// show ------------------------------------------------------------------

func cmdShow(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var sections []string
	var full bool
	var readiness bool
	var markdown bool
	maxBytes := 0
	var hasMaxBytes bool
	p.repeat("section", &sections)
	p.boolValue("full", &full)
	p.boolValue("readiness", &readiness)
	p.boolValue("markdown", &markdown)
	p.intValue("max-bytes", &maxBytes, &hasMaxBytes)
	if err := p.parse(args); err != nil {
		return err
	}
	ctx.markdown = markdown
	if helpFlag {
		return emitHelpCommand(ctx, "show")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	if full && len(sections) > 0 {
		return contract.NewError(contract.ErrInvalidArgument,
			"--full and --section conflict.", nil)
	}
	if hasMaxBytes && (maxBytes < 1 || maxBytes > 1<<20) {
		return contract.NewError(contract.ErrInvalidArgument,
			"Max bytes must be between 1 and 1048576.", nil)
	}
	ref, err := optionalTicketRef(p, "show")
	if err != nil {
		return err
	}
	opts := domain.ShowOptions{Sections: sections, Full: full, MaxBytes: maxBytes, Readiness: readiness}
	return runRepoCommand(ctx, "show", func(st *store.Store) (any, error) {
		ref, err = resolveTicketRef(st, ctx, ref, "show")
		if err != nil {
			return nil, err
		}
		return domain.Show(st, ref, opts)
	})
}

// edit ------------------------------------------------------------------

type EditResult struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
}

func cmdEdit(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "edit")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	if ctx.json {
		return contract.NewError(contract.ErrInvalidArgument,
			"edit is interactive and cannot be combined with JSON mode.", nil)
	}
	ref, err := optionalTicketRef(p, "edit")
	if err != nil {
		return err
	}
	return editWithEditor(ctx, ref)
}

func cmdSubmit(ctx *commandContext, args []string) error {
	return cmdWorkflowMove(ctx, args, "submit", func(st *store.Store, id, actor string, handoff, message *string) (any, error) {
		return domain.Submit(st, id, domain.SubmitOptions{Actor: actor, Handoff: handoff, Message: message})
	})
}

func cmdHold(ctx *commandContext, args []string) error {
	return cmdWorkflowMove(ctx, args, "hold", func(st *store.Store, id, actor string, handoff, message *string) (any, error) {
		return domain.Hold(st, id, domain.HoldOptions{Actor: actor, Handoff: handoff, Message: message})
	})
}

func cmdOpen(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var handoff string
	var handoffSet bool
	var message string
	var messageSet bool
	var claim bool
	p.flag("handoff", kindString, func(value string) error { handoff, handoffSet = value, true; return nil }, false)
	p.flag("message", kindString, func(value string) error { message, messageSet = value, true; return nil }, false)
	p.alias("m", "message")
	p.boolValue("claim", &claim)
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "open")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	actor := ctx.actor
	if actor == "" {
		actor = os.Getenv("TICKET_ACTOR")
	}
	if claim {
		var err error
		actor, err = ctx.actorToken()
		if err != nil {
			return err
		}
	}
	if len(p.positionals) > 2 {
		return contract.NewError(contract.ErrInvalidArgument,
			"Command open accepts an optional ID and handoff text.", nil)
	}
	positionals := append([]string(nil), p.positionals...)
	return runRepoCommand(ctx, "open", func(st *store.Store) (any, error) {
		ref := ""
		if len(positionals) == 2 {
			ref = positionals[0]
			if handoffSet {
				return nil, contract.NewError(contract.ErrInvalidArgument,
					"Handoff cannot be supplied both positionally and with --handoff.", nil)
			}
			handoff, handoffSet = positionals[1], true
		} else if len(positionals) == 1 {
			if looksLikeTicketRef(positionals[0]) {
				ref = positionals[0]
			} else {
				if handoffSet {
					return nil, contract.NewError(contract.ErrInvalidArgument,
						"Handoff cannot be supplied both positionally and with --handoff.", nil)
				}
				handoff, handoffSet = positionals[0], true
			}
		}
		resolved, err := resolveTicketRef(st, ctx, ref, "open")
		if err != nil {
			return nil, err
		}
		ref = resolved
		var handoffPtr *string
		var messagePtr *string
		if handoffSet {
			handoffPtr = &handoff
		}
		if messageSet {
			messagePtr = &message
		}
		return domain.Open(st, ref, domain.OpenOptions{Handoff: handoffPtr, Actor: actor, Message: messagePtr, Claim: claim})
	})
}

func looksLikeTicketRef(value string) bool {
	if _, _, ok := store.ParseID(value); ok {
		return true
	}
	return identity.ValidShorthand(value)
}

func cmdWorkflowMove(ctx *commandContext, args []string, command string, run func(*store.Store, string, string, *string, *string) (any, error)) error {
	p := &parser{}
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var handoff string
	var handoffSet bool
	var message string
	var messageSet bool
	p.flag("handoff", kindString, func(value string) error { handoff, handoffSet = value, true; return nil }, false)
	p.flag("message", kindString, func(value string) error { message, messageSet = value, true; return nil }, false)
	p.alias("m", "message")
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, command)
	}
	if err := ctx.check(); err != nil {
		return err
	}
	ref, err := optionalTicketRef(p, command)
	if err != nil {
		return err
	}
	actor := ctx.actor
	if actor == "" {
		actor = os.Getenv("TICKET_ACTOR")
	}
	return runRepoCommand(ctx, command, func(st *store.Store) (any, error) {
		ref, err = resolveTicketRef(st, ctx, ref, command)
		if err != nil {
			return nil, err
		}
		var handoffPtr *string
		var messagePtr *string
		if handoffSet {
			handoffPtr = &handoff
		}
		if messageSet {
			messagePtr = &message
		}
		return run(st, ref, actor, handoffPtr, messagePtr)
	})
}

func selectedEditor(configs ...*globalOpts) string {
	if value := strings.TrimSpace(os.Getenv("TICKET_EDITOR")); value != "" {
		return value
	}
	if len(configs) > 0 && configs[0] != nil {
		if value := strings.TrimSpace(configs[0].config.Editor); value != "" {
			return value
		}
	}
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	if runtime.GOOS == "windows" {
		return "notepad.exe"
	}
	return "vi"
}

func editorInvocation(editor, path string, line int) (string, []string, error) {
	parts, err := splitEditorCommand(editor)
	if err != nil {
		return "", nil, err
	}
	if len(parts) == 0 {
		return "", nil, contract.NewError(contract.ErrInvalidArgument, "The configured editor is empty.", nil)
	}
	command := parts[0]
	args := append([]string(nil), parts[1:]...)
	base := strings.ToLower(filepath.Base(command))
	base = strings.TrimSuffix(base, ".exe")
	if base == "vi" || base == "vim" || base == "nvim" {
		args = append(args, fmt.Sprintf("+%d", line), path)
	} else {
		args = append(args, path)
	}
	return command, args, nil
}

// splitEditorCommand tokenizes the small command form accepted for editor
// configuration. Quotes group an argument and backslash escapes quotes or
// backslashes in double-quoted text, or whitespace/quotes outside quotes.
// It deliberately never invokes a shell.
func splitEditorCommand(input string) ([]string, error) {
	var parts []string
	var current strings.Builder
	var quote rune
	started := false
	for i := 0; i < len(input); i++ {
		c := rune(input[i])
		if quote != 0 {
			if c == quote {
				quote = 0
				started = true
				continue
			}
			if c == '\\' && quote == '"' && i+1 < len(input) &&
				(input[i+1] == '\\' || input[i+1] == '"') {
				i++
				current.WriteByte(input[i])
				started = true
				continue
			}
			current.WriteRune(c)
			started = true
			continue
		}
		switch {
		case c == '\'' || c == '"':
			quote = c
			started = true
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			if started {
				parts = append(parts, current.String())
				current.Reset()
				started = false
			}
		case c == '\\' && i+1 < len(input) && isEditorEscape(input[i+1]):
			i++
			current.WriteByte(input[i])
			started = true
		default:
			current.WriteRune(c)
			started = true
		}
	}
	if quote != 0 {
		return nil, contract.NewError(contract.ErrInvalidArgument, "The configured editor has an unterminated quote.", nil)
	}
	if started {
		parts = append(parts, current.String())
	}
	return parts, nil
}

func isEditorEscape(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\\' || c == '\'' || c == '"'
}

func objectiveEditLine(t *domain.Ticket) int {
	if section, ok := t.Sections["objective"]; ok {
		// The canonical section has an empty separator line after its
		// heading. Start editing on the content line so typing does not
		// consume that separator.
		return section.HeadingLine + 2
	}
	return 1
}

// path ------------------------------------------------------------------

func cmdPath(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var absolute bool
	p.boolValue("absolute", &absolute)
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "path")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	ref, err := optionalTicketRef(p, "path")
	if err != nil {
		return err
	}
	return runRepoCommand(ctx, "path", func(st *store.Store) (any, error) {
		ref, err = resolveTicketRef(st, ctx, ref, "path")
		if err != nil {
			return nil, err
		}
		return domain.Path(st, ref, absolute)
	})
}

// update --------------------------------------------------------------

type updateInput struct {
	Set      map[string]any    `json:"set"`
	Sections map[string]string `json:"sections"`
}

func cmdUpdate(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var inputPath string
	var tags []string
	p.str("input", &inputPath)
	p.repeat("tag", &tags)
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "update")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	ref, err := optionalTicketRef(p, "update")
	if err != nil {
		return err
	}
	if inputPath == "" && len(tags) == 0 {
		return contract.NewError(contract.ErrInvalidArgument,
			"Update requires --input.", nil)
	}
	if inputPath != "" && len(tags) > 0 {
		return contract.NewError(contract.ErrInvalidArgument,
			"--tag cannot be combined with --input; set tags in the input JSON.", nil)
	}
	var in updateInput
	if inputPath != "" {
		if err := decodeInput(inputPath, &in); err != nil {
			return err
		}
	} else {
		in.Set = map[string]any{"tags": tags}
	}
	actor := ctx.actor
	if actor == "" {
		actor = os.Getenv("TICKET_ACTOR")
	}
	opts := domain.UpdateOptions{Set: in.Set, Sections: in.Sections, Actor: actor}
	return runRepoCommand(ctx, "update", func(st *store.Store) (any, error) {
		ref, err = resolveTicketRef(st, ctx, ref, "update")
		if err != nil {
			return nil, err
		}
		return domain.Update(st, ref, opts)
	})
}

func cmdClaim(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "claim")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	ref, err := optionalTicketRef(p, "claim")
	if err != nil {
		return err
	}
	actor, err := ctx.actorToken()
	if err != nil {
		return err
	}
	return runRepoCommand(ctx, "claim", func(st *store.Store) (any, error) {
		ref, err = resolveTicketRef(st, ctx, ref, "claim")
		if err != nil {
			return nil, err
		}
		return domain.Claim(st, ref, domain.ClaimOptions{Actor: actor})
	})
}

func cmdRelease(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var handoff, message, inputPath string
	var handoffSet bool
	var messageSet bool
	p.flag("handoff", kindString, func(v string) error { handoff = v; handoffSet = true; return nil }, false)
	p.flag("message", kindString, func(v string) error { message = v; messageSet = true; return nil }, false)
	p.alias("m", "message")
	p.str("input", &inputPath)
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "release")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	ref, err := optionalTicketRef(p, "release")
	if err != nil {
		return err
	}
	var in releaseInput
	if inputPath != "" {
		if handoffSet || messageSet {
			return contract.NewError(contract.ErrInvalidArgument, "Release flags cannot be combined with --input.", nil)
		}
		if err := decodeInput(inputPath, &in); err != nil {
			return err
		}
		if in.Handoff != nil {
			handoff = *in.Handoff
			handoffSet = true
		}
		if in.Message != nil {
			message = *in.Message
			messageSet = true
		}
	}
	actor, err := ctx.actorToken()
	if err != nil {
		return err
	}
	return runRepoCommand(ctx, "release", func(st *store.Store) (any, error) {
		ref, err = resolveTicketRef(st, ctx, ref, "release")
		if err != nil {
			return nil, err
		}
		var handoffPtr *string
		var messagePtr *string
		if handoffSet {
			handoffPtr = &handoff
		}
		if messageSet {
			messagePtr = &message
		}
		return domain.Release(st, ref, domain.ReleaseOptions{Actor: actor, Handoff: handoffPtr, Message: messagePtr})
	})
}

type releaseInput struct {
	Handoff *string `json:"handoff"`
	Message *string `json:"message"`
}

type transitionInput struct {
	Outcome *string `json:"outcome"`
	Message *string `json:"message"`
}

func cmdApprove(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var message string
	var messageSet bool
	var states []string
	p.flag("message", kindString, func(value string) error { message, messageSet = value, true; return nil }, false)
	p.alias("m", "message")
	p.repeat("state", &states)
	p.alias("s", "state")
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "approve")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	selectors := append(append([]string{}, p.positionals...), states...)
	ref := ""
	if len(selectors) == 1 && len(states) == 0 && p.positionals[0] != "all" {
		ref = p.positionals[0]
	}
	actor := ctx.actor
	if actor == "" {
		actor = os.Getenv("TICKET_ACTOR")
	}
	return runRepoCommand(ctx, "approve", func(st *store.Store) (any, error) {
		var messagePtr *string
		if messageSet {
			messagePtr = &message
		}
		targets, stateBatch, selectAll, err := transitionTargets(st, selectors, "approve")
		if err != nil {
			return nil, err
		}
		if selectAll {
			return domain.ApproveAll(st, domain.ReviewOptions{Actor: actor, Message: messagePtr})
		}
		if stateBatch || len(targets) > 1 {
			return domain.ApproveMany(st, targets, domain.ReviewOptions{Actor: actor, Message: messagePtr})
		}
		if len(targets) == 1 {
			ref = targets[0]
		}
		ref, err = resolveTicketRef(st, ctx, ref, "approve")
		if err != nil {
			return nil, err
		}
		return domain.Approve(st, ref, domain.ReviewOptions{Actor: actor, Message: messagePtr})
	})
}

func cmdReject(ctx *commandContext, args []string) error {
	return cmdClose(ctx, args, "reject", func(st *store.Store, id string, in transitionInput, actor string) (any, error) {
		return domain.Reject(st, id, domain.RejectOptions{Actor: actor, Outcome: valueOrEmpty(in.Outcome), Message: in.Message})
	})
}

func cmdClose(ctx *commandContext, args []string, command string, run func(*store.Store, string, transitionInput, string) (any, error)) error {
	p := &parser{}
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var outcome, inputPath string
	var outcomeSet bool
	var message string
	var messageSet bool
	var states []string
	var all bool
	p.flag("outcome", kindString, func(v string) error { outcome = v; outcomeSet = true; return nil }, false)
	p.flag("message", kindString, func(v string) error { message = v; messageSet = true; return nil }, false)
	p.alias("m", "message")
	p.str("input", &inputPath)
	if command == "close" {
		p.repeat("state", &states)
		p.alias("s", "state")
		p.boolValue("all", &all)
		p.alias("a", "all")
	}
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, command)
	}
	if err := ctx.check(); err != nil {
		return err
	}
	positionals := p.positionals
	if command == "reject" {
		if len(positionals) > 2 {
			return contract.NewError(contract.ErrInvalidArgument,
				"Command reject accepts one ID and an optional outcome.", nil)
		}
		if len(positionals) == 1 && !looksLikeTicketRef(positionals[0]) {
			if outcomeSet {
				return contract.NewError(contract.ErrInvalidArgument,
					"Reject outcome cannot be supplied both positionally and with --outcome.", nil)
			}
			if inputPath != "" {
				return contract.NewError(contract.ErrInvalidArgument,
					"A positional reject outcome cannot be combined with --input.", nil)
			}
			outcome = positionals[0]
			outcomeSet = true
			positionals = nil
		} else if len(positionals) == 2 {
			if outcomeSet {
				return contract.NewError(contract.ErrInvalidArgument,
					"Reject outcome cannot be supplied both positionally and with --outcome.", nil)
			}
			if inputPath != "" {
				return contract.NewError(contract.ErrInvalidArgument,
					"A positional reject outcome cannot be combined with --input.", nil)
			}
			outcome = positionals[1]
			outcomeSet = true
			positionals = positionals[:1]
		}
	}
	selectors := append(append([]string{}, positionals...), states...)
	refs, all, err := closeTargets(selectors, all, command)
	if err != nil {
		return err
	}
	if command == "reject" && len(refs) > 1 {
		return contract.NewError(contract.ErrInvalidArgument,
			"Command reject accepts one ID and an optional outcome.", nil)
	}
	in := transitionInput{}
	if inputPath != "" {
		if outcomeSet || messageSet {
			return contract.NewError(contract.ErrInvalidArgument, command+" flags cannot be combined with --input.", nil)
		}
		if err := decodeInput(inputPath, &in); err != nil {
			return err
		}
	} else {
		in.Outcome = &outcome
		if messageSet {
			in.Message = &message
		}
	}
	actor := ctx.actor
	if actor == "" {
		actor = os.Getenv("TICKET_ACTOR")
	}
	return runRepoCommand(ctx, command, func(st *store.Store) (any, error) {
		if command == "close" {
			opts := domain.CloseOptions{Outcome: valueOrEmpty(in.Outcome), Actor: actor, Message: in.Message}
			targets, stateBatch, selectAll, targetErr := transitionTargets(st, selectors, command)
			if targetErr != nil {
				return nil, targetErr
			}
			if all || selectAll {
				return domain.CloseAll(st, opts)
			}
			if stateBatch || len(targets) > 1 {
				return domain.CloseMany(st, targets, opts)
			}
			if len(targets) == 1 {
				refs = targets
			}
		}
		ref := ""
		if len(refs) == 1 {
			ref = refs[0]
		}
		ref, err = resolveTicketRef(st, ctx, ref, command)
		if err != nil {
			return nil, err
		}
		return run(st, ref, in, actor)
	})
}

func closeTargets(positionals []string, all bool, command string) ([]string, bool, error) {
	var targets []string
	parts := targetTokens(positionals)
	if len(positionals) > 0 && len(parts) == 0 {
		return nil, false, contract.NewError(contract.ErrInvalidArgument,
			"Command "+command+" contains no targets.", nil)
	}
	if all && len(parts) > 0 {
		return nil, false, contract.NewError(contract.ErrInvalidArgument,
			"The all-target selector cannot be combined with ticket IDs.", nil)
	}
	for _, target := range parts {
		if target == "all" || target == "*" {
			if command != "close" || len(parts) != 1 {
				return nil, false, contract.NewError(contract.ErrInvalidArgument,
					"The all-target selector is supported only as the sole close target.", nil)
			}
			all = true
			continue
		}
		targets = append(targets, target)
	}
	return targets, all, nil
}

func targetTokens(positionals []string) []string {
	raw := strings.Join(positionals, " ")
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
}

func transitionTargets(st *store.Store, positionals []string, command string) ([]string, bool, bool, error) {
	parts := targetTokens(positionals)
	if len(parts) == 0 {
		return nil, false, false, nil
	}
	var targets []string
	stateBatch := false
	selectAll := false
	for _, part := range parts {
		if part == "all" || (command == "close" && part == "*") {
			if len(parts) != 1 {
				return nil, false, false, contract.NewError(contract.ErrInvalidArgument,
					"The all-target selector cannot be combined with ticket IDs or states.", nil)
			}
			selectAll = true
			continue
		}
		if !isTicketState(part) {
			targets = append(targets, part)
			continue
		}
		if command == "approve" && part != "review" {
			return nil, false, false, contract.NewError(contract.ErrInvalidArgument,
				"Approve accepts only the review state as a state selector.", nil)
		}
		if command == "close" && (part == "completed" || part == "rejected") {
			return nil, false, false, contract.NewError(contract.ErrInvalidArgument,
				"Close accepts only nonterminal states as state selectors.", nil)
		}
		listed, err := domain.List(st, domain.ListOptions{
			States: []string{part}, Unlimited: true, Fields: []string{"id"},
		})
		if err != nil {
			return nil, false, false, err
		}
		for _, item := range listed.Items {
			targets = append(targets, item.ID)
		}
		stateBatch = true
	}
	return targets, stateBatch || len(targets) > 1, selectAll, nil
}

func isTicketState(value string) bool {
	switch value {
	case "open", "hold", "review", "signoff", "completed", "rejected", "all":
		return true
	default:
		return false
	}
}

func cmdCheck(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "check")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	if err := p.requireNoPositionals("check"); err != nil {
		return err
	}
	return runRepoCommand(ctx, "check", func(st *store.Store) (any, error) {
		if err := domain.ValidateGraphs(st); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	})
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func decodeInput(path string, out any) error {
	data, err := readInputData(path)
	if err != nil {
		return err
	}
	if err := jsonx.Decode(data, out); err != nil {
		if errors.Is(err, jsonx.ErrUnknownField) {
			return contract.NewError(contract.ErrInvalidArgument,
				"Unknown key in --input JSON: "+err.Error(), nil)
		}
		return contract.NewError(contract.ErrInvalidJSON,
			"Invalid --input JSON: "+err.Error(), nil)
	}
	return nil
}

func readInputData(path string) ([]byte, error) {
	if path != "-" {
		return nil, contract.NewError(contract.ErrInvalidArgument,
			"Structured input must be provided with --input -.", nil)
	}
	data, err := readStdin(2 << 20)
	if err != nil {
		return nil, contract.NewError(contract.ErrFileTooLarge,
			"Input could not be read: "+err.Error(), nil)
	}
	if len(data) > 2<<20 {
		return nil, contract.NewError(contract.ErrFileTooLarge,
			"Input exceeds the 2 MiB limit.", nil)
	}
	return data, nil
}
