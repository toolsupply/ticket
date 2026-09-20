package cli

import (
	"os"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/store"
)

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
	if ctx.inputProvided && inputPath != "-" {
		return unexpectedInvocationInput("update")
	}
	if helpFlag {
		return emitHelpCommand(ctx, "update")
	}
	if ctx.session != nil && ctx.input == nil && inputPath == "-" {
		return sessionStdinError("update")
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
		if err := decodeInput(ctx.input, inputPath, &in); err != nil {
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
	if ctx.inputProvided && inputPath != "-" {
		return unexpectedInvocationInput("release")
	}
	if helpFlag {
		return emitHelpCommand(ctx, "release")
	}
	if ctx.session != nil && ctx.input == nil && inputPath == "-" {
		return sessionStdinError("release")
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
		if err := decodeInput(ctx.input, inputPath, &in); err != nil {
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
