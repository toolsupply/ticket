package cli

import "fmt"

type actorResult struct {
	Actor  *string `json:"actor"`
	Source *string `json:"source"`
}

func cmdActor(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "actor")
	}
	if err := p.requireNoPositionals("actor"); err != nil {
		return err
	}
	actor, source := ctx.effectiveActor()
	if !ctx.json {
		if actor == "" {
			fmt.Fprintln(ctx.stdout, "no actor configured")
		} else {
			fmt.Fprintln(ctx.stdout, actor)
		}
		return nil
	}
	result := actorResult{}
	if actor != "" {
		result.Actor = &actor
		result.Source = &source
	}
	return emitJSON(ctx.stdout, result)
}
