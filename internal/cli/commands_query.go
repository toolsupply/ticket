package cli

import (
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/store"
)

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
	queue, err := workQueue(p.positionals, "ready")
	if err != nil {
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
		return domain.ReadyWithOptions(st, domain.ReadyOptions{Queue: queue, Filters: opts})
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
