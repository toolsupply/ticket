package cli

import (
	"strconv"
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/store"
)

func cmdList(ctx *commandContext, args []string) error {
	p := ctx.newParser()
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var assignee, parent, fields string
	var states []string
	var ids []string
	var withoutTags []string
	var tags []string
	var all, archived, long, byID, byModified, idsOnly, deps, graph bool
	p.boolValue("archived", &archived)
	var markdown bool
	p.boolValue("all", &all)
	p.boolValue("long", &long)
	p.boolValue("by-id", &byID)
	p.boolValue("by-modified", &byModified)
	p.boolValue("markdown", &markdown)
	p.boolValue("ids", &idsOnly)
	p.boolValue("deps", &deps)
	p.boolValue("graph", &graph)
	p.alias("a", "all")
	p.alias("l", "long")
	p.alias("t", "by-id")
	p.alias("u", "by-modified")
	p.alias("1", "ids")
	var queryTokens []string
	var querySet bool
	registerQueryTail(p, &queryTokens, &querySet)
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
	// Selection modifiers determine the TicketSet. Output flags below are
	// presentation/mode choices and must not alter the bare human defaults.
	selectionSpecified := len(p.positionals) > 0 || querySet || len(states) > 0 ||
		len(tags) > 0 || len(withoutTags) > 0 || assignee != "" || unassigned ||
		parent != "" || hasPriority || all || archived || hasLimitVal ||
		hasOffsetVal || byID || byModified
	bareHumanList := !ctx.json && !selectionSpecified
	ctx.markdown = markdown
	if helpFlag {
		return emitHelpCommand(ctx, "list")
	}
	if graph {
		switch {
		case idsOnly:
			return contract.NewError(contract.ErrInvalidArgument, "--graph cannot be combined with --ids.", nil)
		case fields != "":
			return contract.NewError(contract.ErrInvalidArgument, "--graph cannot be combined with --fields.", nil)
		case deps:
			return contract.NewError(contract.ErrInvalidArgument, "--graph cannot be combined with --deps.", nil)
		case markdown:
			return contract.NewError(contract.ErrInvalidArgument, "--graph cannot be combined with --markdown.", nil)
		case long:
			return contract.NewError(contract.ErrInvalidArgument, "--graph cannot be combined with --long.", nil)
		}
	}
	if idsOnly && ctx.json {
		return contract.NewError(contract.ErrInvalidArgument, "IDs-only output cannot be combined with JSON output.", nil)
	}
	if idsOnly && (deps || markdown) {
		return contract.NewError(contract.ErrInvalidArgument, "IDs-only output cannot be combined with dependency or Markdown output.", nil)
	}
	if err := ctx.check(); err != nil {
		return err
	}
	prepared, targetErr := prepareTargetSpec(p.positionals, queryTokens, querySet, targetSpecOptions{
		AllowLegacyStates: true,
		AllowLegacyAll:    true,
		Scope:             listQueryScope(all, archived),
	})
	if targetErr != nil {
		return targetErr
	}
	if !prepared.HasQuery {
		ids = append(ids, prepared.ExplicitRefs...)
		states = append(states, prepared.LegacyStates...)
		if prepared.LegacyAll {
			states = append(states, "all")
		}
	}
	if !prepared.HasQuery && all {
		if len(ids) > 0 || (len(states) > 0 && !(len(states) == 1 && states[0] == "all")) {
			return contract.NewError(contract.ErrInvalidArgument, "--all conflicts with ticket IDs or --state.", nil)
		}
		states = []string{"all"}
	}
	if all && archived {
		return contract.NewError(contract.ErrInvalidArgument, "--all conflicts with --archived.", nil)
	}
	if archived && len(ids) > 0 {
		return contract.NewError(contract.ErrInvalidArgument, "--archived conflicts with ticket IDs.", nil)
	}
	opts := domain.ListOptions{
		IDs: ids, States: states, Tags: tags, WithoutTags: withoutTags,
		Assignee: assignee, Unassigned: unassigned, Parent: parent,
		ArchivedOnly: archived, IncludeArchived: all,
	}
	if deps {
		opts.DependencyView = "all"
	} else if !ctx.json && !idsOnly {
		opts.DependencyView = "blocking"
	}
	if bareHumanList {
		opts.State = "all"
		opts.NonterminalOnly = true
		opts.Unlimited = true
	}
	if !prepared.HasQuery && archived && len(states) == 0 {
		states = []string{"all"}
		opts.States = states
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
	if !prepared.HasQuery && !ctx.json && !idsOnly && !byID && !byModified {
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

	if prepared.HasQuery {
		if len(states) > 0 {
			return ambiguousTargetError()
		}
		if hasLimitVal && limitVal <= 0 {
			return contract.NewError(contract.ErrInvalidArgument, "Limit must be greater than zero.", nil)
		}
		if unassigned && assignee != "" {
			return contract.NewError(contract.ErrInvalidArgument, "Assignee and unassigned conflict.", nil)
		}
		if prepared.Target.Query == nil {
			return contract.NewError(contract.ErrInvalidArgument, "List query is missing an expression.", nil)
		}
		prepared.Target.Query.Expr = listQueryWithFilters(prepared.Target.Query.Expr, tags, withoutTags, assignee, unassigned, parent, priorityVal, hasPriority)
		if hasLimitVal {
			prepared.Target.Query.Limit = limitVal
		}
		prepared.Target.Query.Offset = offsetVal
		if byModified {
			prepared.Target.Query.Sort = domain.SortModifiedDesc
		} else if byID {
			prepared.Target.Query.Sort = domain.SortIDDesc
		} else {
			prepared.Target.Query.Sort = domain.SortPriorityID
		}
		return runRepoCommand(ctx, "list", func(st *store.Store) (any, error) {
			set, err := domain.EvaluateTarget(st, prepared.Target)
			if err != nil {
				return nil, err
			}
			if graph {
				return domain.GraphForTicketSet(set, domain.GraphOptions{})
			}
			result, err := domain.ProjectTicketSet(st, set, opts.Fields, opts.DependencyView)
			if err != nil {
				return nil, err
			}
			result.IDsOnly = idsOnly
			return result, nil
		})
	}
	return runRepoCommand(ctx, "list", func(st *store.Store) (any, error) {
		if graph {
			set, err := domain.SelectList(st, opts)
			if err != nil {
				return nil, err
			}
			return domain.GraphForTicketSet(set, domain.GraphOptions{})
		}
		result, err := domain.List(st, opts)
		if err != nil {
			return nil, err
		}
		result.IDsOnly = idsOnly
		return result, nil
	})
}

func listQueryScope(all, archived bool) domain.QueryScope {
	if archived {
		return domain.ScopeArchived
	}
	if all {
		return domain.ScopeAll
	}
	return domain.ScopeActive
}

func listQueryWithFilters(expr domain.Expr, tags, withoutTags []string, assignee string, unassigned bool, parent string, priority int, hasPriority bool) domain.Expr {
	terms := []domain.Expr{expr}
	for _, tag := range tags {
		terms = append(terms, domain.Field("tag", tag))
	}
	for _, tag := range withoutTags {
		terms = append(terms, domain.Not(domain.Field("tag", tag)))
	}
	if assignee != "" {
		terms = append(terms, domain.Field("assignee", assignee))
	}
	if unassigned {
		terms = append(terms, domain.Bare("unclaimed"))
	}
	if parent != "" {
		terms = append(terms, domain.Field("parent", parent))
	}
	if hasPriority {
		terms = append(terms, domain.Field("priority", "P"+strconv.Itoa(priority)))
	}
	if len(terms) == 1 {
		return expr
	}
	return domain.And(terms...)
}

// grep -----------------------------------------------------------------

func cmdGrep(ctx *commandContext, args []string) error {
	p := ctx.newParser()
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
	p := ctx.newParser()
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
	p := ctx.newParser()
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
	p := ctx.newParser()
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var claim bool
	var markdown bool
	var tags []string
	var withoutTags []string
	var priority int
	var hasPriority bool
	p.boolValue("claim", &claim)
	p.boolValue("markdown", &markdown)
	p.repeat("tag", &tags)
	p.repeat("without-tag", &withoutTags)
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
	opts := domain.NextOptions{Queue: queue, Tags: tags, WithoutTags: withoutTags, Claim: claim, Actor: actor}
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
	p := ctx.newParser()
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
