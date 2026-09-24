package cli

import (
	"os"
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/markdown"
	"github.com/toolsupply/ticket/internal/store"
)

func cmdPath(ctx *commandContext, args []string) error {
	p := ctx.newParser()
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

// cmdAppend adds forgiving human Markdown to a ticket's Objective section.
// The domain update path performs ownership, validation, and atomic replace.
func cmdAppend(ctx *commandContext, args []string) error {
	p := ctx.newParser()
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var objectivePath string
	p.str("objective", &objectivePath)
	p.alias("o", "objective")
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "append")
	}
	if ctx.session != nil && ctx.input == nil && (objectivePath == "-" || (len(p.positionals) > 1 && p.positionals[len(p.positionals)-1] == "-")) {
		return sessionStdinError("append")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	if len(p.positionals) == 0 || len(p.positionals) > 2 {
		return contract.NewError(contract.ErrInvalidArgument,
			"Command append requires a ticket ID and Objective source.", nil)
	}
	ref := p.positionals[0]
	if len(p.positionals) == 2 {
		if objectivePath != "" {
			return contract.NewError(contract.ErrInvalidArgument,
				"An Objective source may be supplied only once.", nil)
		}
		objectivePath = p.positionals[1]
	}
	var data []byte
	var err error
	if objectivePath == "-" {
		data, err = readSource(ctx.input, objectivePath, maxInvocationInputBytes)
	} else if len(p.positionals) == 1 && objectivePath == "" {
		var body string
		body, err = readPipedCreateBody(ctx.input)
		data = []byte(body)
	} else {
		data, err = readSource(ctx.input, objectivePath, maxInvocationInputBytes)
	}
	if err != nil {
		return err
	}
	_, objective := markdown.PrepareObjective(string(data), false)
	if strings.TrimSpace(objective) == "" {
		return contract.NewError(contract.ErrInvalidArgument, "Objective content must not be empty.", nil)
	}
	actor, _ := ctx.effectiveActor()
	return runRepoCommand(ctx, "append", func(st *store.Store) (any, error) {
		ref, err = resolveTicketRef(st, ctx, ref, "append")
		if err != nil {
			return nil, err
		}
		return domain.Update(st, ref, domain.UpdateOptions{
			AppendSections: map[string]string{"objective": objective},
			Actor:          actor,
		})
	})
}

// update --------------------------------------------------------------

type updateInput struct {
	Set            map[string]any    `json:"set"`
	Sections       map[string]string `json:"sections"`
	AppendSections map[string]string `json:"append_sections"`
}

func cmdUpdate(ctx *commandContext, args []string) error {
	p := ctx.newParser()
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var inputPath string
	var tags []string
	var priority int
	var hasPriority bool
	p.str("input", &inputPath)
	p.repeat("tag", &tags)
	p.intValue("priority", &priority, &hasPriority)
	p.alias("p", "priority")
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
	if inputPath == "" && len(tags) == 0 && !hasPriority {
		return contract.NewError(contract.ErrInvalidArgument,
			"Update requires --input.", nil)
	}
	if inputPath != "" && (len(tags) > 0 || hasPriority) {
		return contract.NewError(contract.ErrInvalidArgument,
			"Direct update flags cannot be combined with --input; set fields in the input JSON.", nil)
	}
	var in updateInput
	if inputPath != "" {
		if err := decodeInput(ctx.input, inputPath, &in); err != nil {
			return err
		}
	} else {
		in.Set = map[string]any{}
		if len(tags) > 0 {
			in.Set["tags"] = tags
		}
		if hasPriority {
			in.Set["priority"] = priority
		}
	}
	actor := ctx.actor
	if actor == "" {
		actor = os.Getenv("TICKET_ACTOR")
	}
	opts := domain.UpdateOptions{Set: in.Set, Sections: in.Sections, AppendSections: in.AppendSections, Actor: actor}
	return runRepoCommand(ctx, "update", func(st *store.Store) (any, error) {
		ref, err = resolveTicketRef(st, ctx, ref, "update")
		if err != nil {
			return nil, err
		}
		return domain.Update(st, ref, opts)
	})
}

func cmdClaim(ctx *commandContext, args []string) error {
	p := ctx.newParser()
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
	p := ctx.newParser()
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

func cmdReassign(ctx *commandContext, args []string) error {
	p := ctx.newParser()
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var handoff, message, inputPath string
	var handoffSet, messageSet bool
	p.flag("handoff", kindString, func(v string) error { handoff = v; handoffSet = true; return nil }, false)
	p.flag("message", kindString, func(v string) error { message = v; messageSet = true; return nil }, false)
	p.alias("m", "message")
	p.str("input", &inputPath)
	if err := p.parse(args); err != nil {
		return err
	}
	if ctx.inputProvided && inputPath != "-" {
		return unexpectedInvocationInput("reassign")
	}
	if helpFlag {
		return emitHelpCommand(ctx, "reassign")
	}
	if ctx.session != nil && ctx.input == nil && inputPath == "-" {
		return sessionStdinError("reassign")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	if inputPath == "" && len(p.positionals) != 2 {
		return contract.NewError(contract.ErrInvalidArgument,
			"Command reassign requires a ticket ID and target assignee.", nil)
	}
	if inputPath != "" && len(p.positionals) > 1 {
		return contract.NewError(contract.ErrInvalidArgument,
			"Command reassign accepts at most one ticket ID with --input.", nil)
	}
	ref := ""
	target := ""
	if len(p.positionals) > 0 {
		ref = p.positionals[0]
	}
	if inputPath == "" {
		target = p.positionals[1]
	}
	var in reassignInput
	if inputPath != "" {
		if handoffSet || messageSet {
			return contract.NewError(contract.ErrInvalidArgument, "Reassign flags cannot be combined with --input.", nil)
		}
		if err := decodeInput(ctx.input, inputPath, &in); err != nil {
			return err
		}
		target = in.Assignee
		handoff = valueOrEmpty(in.Handoff)
		message = valueOrEmpty(in.Message)
	}
	actor, err := ctx.actorToken()
	if err != nil {
		return err
	}
	return runRepoCommand(ctx, "reassign", func(st *store.Store) (any, error) {
		ref, err = resolveTicketRef(st, ctx, ref, "reassign")
		if err != nil {
			return nil, err
		}
		var handoffPtr, messagePtr *string
		if inputPath != "" {
			handoffPtr, messagePtr = in.Handoff, in.Message
		} else {
			if handoffSet {
				handoffPtr = &handoff
			}
			if messageSet {
				messagePtr = &message
			}
		}
		return domain.Reassign(st, ref, domain.ReassignOptions{
			Actor: actor, Assignee: target, Handoff: handoffPtr, Message: messagePtr,
		})
	})
}

type releaseInput struct {
	Handoff *string `json:"handoff"`
	Message *string `json:"message"`
}

type reassignInput struct {
	Assignee string  `json:"assignee"`
	Handoff  *string `json:"handoff"`
	Message  *string `json:"message"`
}
