package cli

import (
	"os"
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/store"
)

func cmdQuery(ctx *commandContext, args []string) error {
	producerArgs, consumerArgs, composed, err := splitQueryComposition(args)
	if err != nil {
		return err
	}
	options, err := parseQueryOptions(ctx, producerArgs)
	if err != nil {
		return err
	}
	if options.help {
		return emitHelpCommand(ctx, "query")
	}
	if !composed {
		return runRepoCommand(ctx, "query", func(st *store.Store) (any, error) {
			set, err := evaluateQueryOptions(st, options)
			if err != nil {
				return nil, err
			}
			return projectQueryOptions(ctx, st, set, options)
		})
	}
	if options.idsOnly || options.markdown || options.deps || len(options.fields) > 0 {
		return contract.NewError(contract.ErrInvalidArgument,
			"Query presentation options must follow :: on the consumer (for example, :: list --ids).", nil)
	}
	return runQueryComposition(ctx, options, consumerArgs)
}

type queryOptions struct {
	expr     domain.Expr
	scope    domain.QueryScope
	sort     domain.SortSpec
	limit    int
	offset   int
	fields   []string
	idsOnly  bool
	markdown bool
	deps     bool
	help     bool
	actorSet bool
}

func parseQueryOptions(ctx *commandContext, args []string) (*queryOptions, error) {
	p := ctx.newParser()
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var actor string
	p.str("actor", &actor)
	var all, archived, markdown, deps, idsOnly bool
	p.boolValue("all", &all)
	p.boolValue("archived", &archived)
	p.boolValue("markdown", &markdown)
	p.boolValue("deps", &deps)
	p.boolValue("ids", &idsOnly)
	p.alias("1", "ids")
	var sortName, fields string
	p.str("sort", &sortName)
	p.str("fields", &fields)
	limit, hasLimit := 0, false
	p.intValue("limit", &limit, &hasLimit)
	offset, hasOffset := 0, false
	p.intValue("offset", &offset, &hasOffset)
	if err := p.parse(args); err != nil {
		return nil, err
	}
	ctx.markdown = markdown
	if helpFlag {
		return &queryOptions{help: true}, nil
	}
	if idsOnly && ctx.json {
		return nil, contract.NewError(contract.ErrInvalidArgument, "IDs-only output cannot be combined with JSON output.", nil)
	}
	if all && archived {
		return nil, contract.NewError(contract.ErrInvalidArgument, "--all conflicts with --archived.", nil)
	}
	if hasLimit && limit <= 0 {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Query limit must be greater than zero.", nil)
	}
	expr, err := parseTQL(p.positionals)
	if err != nil {
		return nil, err
	}
	if err := ctx.check(); err != nil {
		return nil, err
	}
	if actor != "" {
		ctx.actor = actor
	}
	var scope domain.QueryScope
	if archived {
		scope = domain.ScopeArchived
	} else if all {
		scope = domain.ScopeAll
	}
	var projectionFields []string
	if fields != "" {
		projectionFields = strings.Split(fields, ",")
		for _, field := range projectionFields {
			if field == "" {
				return nil, contract.NewError(contract.ErrInvalidArgument, "Fields list contains an empty entry.", nil)
			}
		}
	}
	return &queryOptions{
		expr: expr, scope: scope, sort: domain.SortSpec(sortName), limit: limit,
		offset: offset, fields: projectionFields, idsOnly: idsOnly, markdown: markdown,
		deps: deps, actorSet: actor != "",
	}, nil
}

func evaluateQueryOptions(st *store.Store, options *queryOptions) (*domain.TicketSet, error) {
	return domain.EvaluateQuery(st, domain.QuerySpec{
		Expr: options.expr, Scope: options.scope, Sort: options.sort,
		Limit: options.limit, Offset: options.offset,
	})
}

func projectQueryOptions(ctx *commandContext, st *store.Store, set *domain.TicketSet, options *queryOptions) (*domain.ListResult, error) {
	dependencyView := ""
	if options.deps {
		dependencyView = "all"
	} else if !ctx.json && !options.idsOnly {
		// Preserve the query command's existing human dependency view. The
		// renderer is selected by ctx, so JSON callers do not receive it.
		dependencyView = "blocking"
	}
	result, err := domain.ProjectTicketSet(st, set, options.fields, dependencyView)
	if err != nil {
		return nil, err
	}
	result.IDsOnly = options.idsOnly
	return result, nil
}

func splitQueryComposition(args []string) (producer, consumer []string, composed bool, err error) {
	boundary := -1
	for i, arg := range args {
		if arg != "::" {
			continue
		}
		if boundary >= 0 {
			return nil, nil, false, contract.NewError(contract.ErrInvalidArgument,
				"Query composition accepts only one :: boundary.", nil)
		}
		boundary = i
	}
	if boundary < 0 {
		return args, nil, false, nil
	}
	if boundary == len(args)-1 {
		return nil, nil, false, contract.NewError(contract.ErrInvalidArgument,
			"Query composition requires a producer and consumer around ::.", nil)
	}
	producer = append([]string(nil), args[:boundary]...)
	consumer = append([]string(nil), args[boundary+1:]...)
	if len(consumer) == 0 || consumer[0] == "::" {
		return nil, nil, false, contract.NewError(contract.ErrInvalidArgument,
			"Query composition requires a consumer after ::.", nil)
	}
	return producer, consumer, true, nil
}

func runQueryComposition(ctx *commandContext, options *queryOptions, args []string) error {
	consumer := canonicalCommand(args[0])
	if consumer != "list" && consumer != "graph" && consumer != "close" && consumer != "archive" && consumer != "approve" {
		return contract.NewError(contract.ErrInvalidArgument,
			"Unsupported query composition consumer: "+args[0]+".", nil)
	}
	plan, err := prepareQueryConsumer(ctx, options, consumer, args[1:])
	if err != nil {
		return err
	}
	if helpFlag {
		return nil
	}
	return runRepoCommand(ctx, consumer, func(st *store.Store) (any, error) {
		set, err := evaluateQueryOptions(st, options)
		if err != nil {
			return nil, err
		}
		return plan(st, set)
	})
}

type queryConsumerPlan func(*store.Store, *domain.TicketSet) (any, error)

func prepareQueryConsumer(ctx *commandContext, options *queryOptions, consumer string, args []string) (queryConsumerPlan, error) {
	switch consumer {
	case "list":
		return prepareQueryListConsumer(ctx, args)
	case "graph":
		return prepareQueryGraphConsumer(ctx, args)
	case "close":
		return prepareQueryCloseConsumer(ctx, options, args)
	case "archive":
		return prepareQueryArchiveConsumer(ctx, args)
	case "approve":
		return prepareQueryApproveConsumer(ctx, options, args)
	default:
		return nil, contract.NewError(contract.ErrInvalidArgument,
			"Unsupported query composition consumer: "+consumer+".", nil)
	}
}

func prepareQueryListConsumer(ctx *commandContext, args []string) (queryConsumerPlan, error) {
	p := ctx.newParser()
	ctx.registerJSON(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var deps, markdown, idsOnly bool
	p.boolValue("deps", &deps)
	p.boolValue("markdown", &markdown)
	p.boolValue("ids", &idsOnly)
	p.alias("1", "ids")
	var fields string
	p.str("fields", &fields)
	if err := p.parse(args); err != nil {
		return nil, err
	}
	if helpFlag {
		return nil, emitHelpCommand(ctx, "list")
	}
	if len(p.positionals) > 0 {
		return nil, compositionTargetError("list")
	}
	if idsOnly && ctx.json {
		return nil, contract.NewError(contract.ErrInvalidArgument, "IDs-only output cannot be combined with JSON output.", nil)
	}
	projectionFields, err := compositionFields(fields)
	if err != nil {
		return nil, err
	}
	ctx.markdown = markdown
	dependencyView := ""
	if deps {
		dependencyView = "all"
	} else if !ctx.json && !idsOnly {
		dependencyView = "blocking"
	}
	return func(st *store.Store, set *domain.TicketSet) (any, error) {
		result, err := domain.ProjectTicketSet(st, set, projectionFields, dependencyView)
		if err != nil {
			return nil, err
		}
		result.IDsOnly = idsOnly
		return result, nil
	}, nil
}

func prepareQueryGraphConsumer(ctx *commandContext, args []string) (queryConsumerPlan, error) {
	p := ctx.newParser()
	ctx.registerJSON(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var reverse, ascii, unicode bool
	var depth int
	var depthSet bool
	p.boolValue("reverse", &reverse)
	p.boolValue("ascii", &ascii)
	p.boolValue("unicode", &unicode)
	p.intValue("depth", &depth, &depthSet)
	if err := p.parse(args); err != nil {
		return nil, err
	}
	if helpFlag {
		return nil, emitHelpCommand(ctx, "graph")
	}
	if len(p.positionals) > 0 {
		return nil, compositionTargetError("graph")
	}
	if ascii && unicode {
		return nil, contract.NewError(contract.ErrInvalidArgument, "--ascii and --unicode cannot be combined.", nil)
	}
	if depthSet {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Selected-set graph does not support --depth.", nil)
	}
	return func(_ *store.Store, set *domain.TicketSet) (any, error) {
		result, err := domain.GraphForTicketSet(set, domain.GraphOptions{Reverse: reverse})
		if err != nil {
			return nil, err
		}
		setGraphRenderMode(result, ascii, unicode)
		return result, nil
	}, nil
}

func prepareQueryCloseConsumer(ctx *commandContext, options *queryOptions, args []string) (queryConsumerPlan, error) {
	p := ctx.newParser()
	ctx.registerJSON(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var outcome, message string
	var messageSet bool
	var consumerActor string
	p.str("actor", &consumerActor)
	p.str("outcome", &outcome)
	p.flag("message", kindString, func(value string) error {
		message, messageSet = value, true
		return nil
	}, false)
	p.alias("m", "message")
	if err := p.parse(args); err != nil {
		return nil, err
	}
	if helpFlag {
		return nil, emitHelpCommand(ctx, "close")
	}
	if len(p.positionals) > 0 {
		return nil, compositionTargetError("close")
	}
	if consumerActor != "" {
		if options.actorSet {
			return nil, contract.NewError(contract.ErrInvalidArgument, "Actor must be supplied on only one side of ::.", nil)
		}
		ctx.actor = consumerActor
	}
	actor := ctx.actor
	if actor == "" {
		actor = os.Getenv("TICKET_ACTOR")
	}
	var messagePtr *string
	if messageSet {
		messagePtr = &message
	}
	opts := domain.CloseOptions{Outcome: outcome, Actor: actor, Message: messagePtr}
	return func(st *store.Store, set *domain.TicketSet) (any, error) {
		return domain.CloseMany(st, set.IDs(), opts)
	}, nil
}

func prepareQueryArchiveConsumer(ctx *commandContext, args []string) (queryConsumerPlan, error) {
	p := ctx.newParser()
	ctx.registerJSON(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	if err := p.parse(args); err != nil {
		return nil, err
	}
	if helpFlag {
		return nil, emitHelpCommand(ctx, "archive")
	}
	if len(p.positionals) > 0 {
		return nil, compositionTargetError("archive")
	}
	return func(st *store.Store, set *domain.TicketSet) (any, error) {
		return domain.ArchiveMany(st, set.IDs())
	}, nil
}

func prepareQueryApproveConsumer(ctx *commandContext, options *queryOptions, args []string) (queryConsumerPlan, error) {
	p := ctx.newParser()
	ctx.registerJSON(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var message string
	var messageSet bool
	var consumerActor string
	p.str("actor", &consumerActor)
	p.flag("message", kindString, func(value string) error {
		message, messageSet = value, true
		return nil
	}, false)
	p.alias("m", "message")
	if err := p.parse(args); err != nil {
		return nil, err
	}
	if helpFlag {
		return nil, emitHelpCommand(ctx, "approve")
	}
	if len(p.positionals) > 0 {
		return nil, compositionTargetError("approve")
	}
	if consumerActor != "" {
		if options.actorSet {
			return nil, contract.NewError(contract.ErrInvalidArgument, "Actor must be supplied on only one side of ::.", nil)
		}
		ctx.actor = consumerActor
	}
	actor := ctx.actor
	if actor == "" {
		actor = os.Getenv("TICKET_ACTOR")
	}
	var messagePtr *string
	if messageSet {
		messagePtr = &message
	}
	opts := domain.ReviewOptions{Actor: actor, Message: messagePtr}
	return func(st *store.Store, set *domain.TicketSet) (any, error) {
		return domain.ApproveMany(st, set.IDs(), opts)
	}, nil
}

func compositionFields(fields string) ([]string, error) {
	if fields == "" {
		return nil, nil
	}
	parts := strings.Split(fields, ",")
	for _, part := range parts {
		if part == "" {
			return nil, contract.NewError(contract.ErrInvalidArgument, "Fields list contains an empty entry.", nil)
		}
	}
	return parts, nil
}

func compositionTargetError(consumer string) error {
	return contract.NewError(contract.ErrInvalidArgument,
		"Command "+consumer+" after :: accepts no ticket selectors; the query side already supplies the target.", nil)
}
