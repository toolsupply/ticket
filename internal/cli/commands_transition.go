package cli

import (
	"os"
	"strings"
	"unicode"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/store"
)

type transitionInput struct {
	Outcome *string `json:"outcome"`
	Message *string `json:"message"`
}

func cmdApprove(ctx *commandContext, args []string) error {
	p := ctx.newParser()
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var message string
	var messageSet bool
	var states []string
	var queryTokens []string
	var querySet bool
	p.flag("message", kindString, func(value string) error { message, messageSet = value, true; return nil }, false)
	p.alias("m", "message")
	p.repeat("state", &states)
	p.alias("s", "state")
	registerQueryTail(p, &queryTokens, &querySet)
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "approve")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	selectors := append(append([]string{}, targetTokens(p.positionals)...), states...)
	prepared, err := prepareTargetSpec(selectors, queryTokens, querySet, targetSpecOptions{AllowLegacyStates: true, AllowLegacyAll: true})
	if err != nil {
		return err
	}
	if prepared.HasQuery && len(states) > 0 {
		return ambiguousTargetError()
	}
	ref := ""
	if !prepared.HasQuery && len(selectors) == 1 && len(states) == 0 && selectors[0] != "all" {
		ref = selectors[0]
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
		if prepared.HasQuery {
			set, err := domain.EvaluateTarget(st, prepared.Target)
			if err != nil {
				return nil, err
			}
			return domain.ApproveMany(st, set.IDs(), domain.ReviewOptions{Actor: actor, Message: messagePtr})
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
	p := ctx.newParser()
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var outcome, inputPath string
	var outcomeSet bool
	var message string
	var messageSet bool
	var states []string
	var all bool
	var queryTokens []string
	var querySet bool
	p.flag("outcome", kindString, func(v string) error { outcome = v; outcomeSet = true; return nil }, false)
	p.flag("message", kindString, func(v string) error { message = v; messageSet = true; return nil }, false)
	p.alias("m", "message")
	p.str("input", &inputPath)
	if command == "close" {
		p.repeat("state", &states)
		p.alias("s", "state")
		p.boolValue("all", &all)
		p.alias("a", "all")
		registerQueryTail(p, &queryTokens, &querySet)
	}
	if err := p.parse(args); err != nil {
		return err
	}
	if ctx.inputProvided && inputPath != "-" {
		return unexpectedInvocationInput(command)
	}
	if helpFlag {
		return emitHelpCommand(ctx, command)
	}
	if command == "reject" && len(p.positionals) == 1 && p.positionals[0] == "help" &&
		!outcomeSet && inputPath == "" && !messageSet {
		return emitHelpCommand(ctx, command)
	}
	if ctx.session != nil && ctx.input == nil && inputPath == "-" {
		return sessionStdinError(command)
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
	selectors := append(append([]string{}, targetTokens(positionals)...), states...)
	var refs []string
	prepared, err := prepareTargetSpec(selectors, queryTokens, querySet, targetSpecOptions{AllowLegacyStates: command == "close", AllowLegacyAll: command == "close"})
	if err != nil {
		return err
	}
	if prepared.HasQuery {
		if command != "close" {
			return contract.NewError(contract.ErrInvalidArgument, "Command "+command+" does not accept TQL targets.", nil)
		}
		if all || len(states) > 0 {
			return ambiguousTargetError()
		}
	} else {
		var targetErr error
		refs, all, targetErr = closeTargets(selectors, all, command)
		if targetErr != nil {
			return targetErr
		}
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
		if err := decodeInput(ctx.input, inputPath, &in); err != nil {
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
			if prepared.HasQuery {
				set, err := domain.EvaluateTarget(st, prepared.Target)
				if err != nil {
					return nil, err
				}
				return domain.CloseMany(st, set.IDs(), opts)
			}
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
		if command == "close" && (part == "closed" || part == "completed" || part == "rejected") {
			return nil, false, false, contract.NewError(contract.ErrInvalidArgument,
				"Close accepts only nonterminal states as state selectors.", nil)
		}
		set, err := domain.EvaluateQuery(st, domain.QuerySpec{
			Expr:  domain.Field("state", part),
			Scope: domain.ScopeActive,
		})
		if err != nil {
			return nil, false, false, err
		}
		targets = append(targets, set.IDs()...)
		stateBatch = true
	}
	return targets, stateBatch || len(targets) > 1, selectAll, nil
}

func isTicketState(value string) bool {
	switch value {
	case "open", "hold", "review", "signoff", "closed", "completed", "rejected", "all":
		return true
	default:
		return false
	}
}
