package cli

import (
	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/store"
)

func cmdArchive(ctx *commandContext, args []string) error {
	return cmdArchiveMove(ctx, args, "archive", domain.Archive)
}

func cmdUnarchive(ctx *commandContext, args []string) error {
	return cmdArchiveMove(ctx, args, "unarchive", domain.Unarchive)
}

func cmdArchiveMove(ctx *commandContext, args []string, command string, move func(*store.Store, string) (*domain.ArchiveResult, error)) error {
	p := ctx.newParser()
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var queryTokens []string
	var querySet bool
	if command == "archive" {
		registerQueryTail(p, &queryTokens, &querySet)
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
	if command == "archive" {
		prepared, err := prepareTargetSpec(p.positionals, queryTokens, querySet, targetSpecOptions{Scope: domain.ScopeActive})
		if err != nil {
			return err
		}
		if prepared.HasQuery {
			return runRepoCommand(ctx, command, func(st *store.Store) (any, error) {
				set, err := domain.EvaluateTarget(st, prepared.Target)
				if err != nil {
					return nil, err
				}
				return domain.ArchiveMany(st, set.IDs())
			})
		}
		if len(prepared.ExplicitRefs) > 1 {
			return contract.NewError(contract.ErrInvalidArgument, "Command archive accepts at most one ID.", nil)
		}
		ref := ""
		if len(prepared.ExplicitRefs) == 1 {
			ref = prepared.ExplicitRefs[0]
		}
		return runRepoCommand(ctx, command, func(st *store.Store) (any, error) {
			ref, err = resolveTicketRef(st, ctx, ref, command)
			if err != nil {
				return nil, err
			}
			return move(st, ref)
		})
	}
	ref, err := optionalTicketRef(p, command)
	if err != nil {
		return err
	}
	return runRepoCommand(ctx, command, func(st *store.Store) (any, error) {
		ref, err := resolveTicketRef(st, ctx, ref, command)
		if err != nil {
			return nil, err
		}
		return move(st, ref)
	})
}
