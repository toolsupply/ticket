package cli

import (
	"errors"
	"fmt"

	"ticket/internal/contract"
	"ticket/internal/upgrade"
)

func cmdUpgrade(ctx *commandContext, args []string) error {
	p := &parser{}
	// Upgrade is independent of the ticket repository and actor ownership.
	// Keep only output/debug controls from the common process flags.
	p.boolValue("json", &ctx.json)
	p.boolValue("debug", &ctx.debug)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "upgrade")
	}
	if err := p.requireNoPositionals("upgrade"); err != nil {
		return err
	}
	result, err := upgrade.Run(upgrade.Options{CurrentVersion: Version})
	if err != nil {
		code := contract.ErrIOError
		if errors.Is(err, upgrade.ErrUnsupportedPlatform) {
			code = contract.ErrInvalidArgument
		}
		return contract.NewError(code, err.Error()+".", nil)
	}
	if ctx.json {
		return emitSuccess(ctx.stdout, result)
	}
	if !result.Changed {
		_, err := fmt.Fprintf(ctx.stdout, "ticket v%s is already up to date.\n", result.OldVersion)
		return err
	}
	_, err = fmt.Fprintf(ctx.stdout, "upgraded ticket v%s -> v%s\n\nThe ticket-tasks Skill may also have changed.\nIf you installed it separately, update it from:\nhttps://github.com/toolsupply/ticket/tree/main/skills/ticket-tasks\n", result.OldVersion, result.NewVersion)
	return err
}
