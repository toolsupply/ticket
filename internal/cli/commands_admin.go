package cli

import (
	"fmt"
	"os"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/store"
)

// helpFlag and helpSeen are per-invocation --help state (one
// command per process).
var helpFlag, helpSeen bool

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
	if ctx.session != nil {
		target = ctx.globalOpts.selectedRoot()
	} else {
		target = store.ConfiguredRoot()
		if target == "" {
			target = ctx.globalOpts.selectedRoot()
		}
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
