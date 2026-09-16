package cli

import (
	"time"

	"ticket/internal/contract"
	"ticket/internal/domain"
	"ticket/internal/store"
)

const (
	waitPollInterval    = 250 * time.Millisecond
	waitRefreshInterval = 30 * time.Second
)

func cmdWait(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var claim bool
	var tags []string
	var priority int
	var hasPriority bool
	p.boolValue("claim", &claim)
	p.repeat("tag", &tags)
	p.intValue("priority", &priority, &hasPriority)
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "wait")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	queue, err := workQueue(p.positionals, "wait")
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
	return waitForNext(ctx, opts)
}

func waitForNext(ctx *commandContext, opts domain.NextOptions) error {
	marker, err := currentChangeState(ctx)
	if err != nil {
		return err
	}
	lastRefresh := time.Now()
	for {
		result, root, err := attemptWait(ctx, opts)
		if err != nil {
			return err
		}
		if result.Item != nil {
			if !ctx.json {
				return renderHumanTo(ctx.stdout, "wait", result, false)
			}
			return emitSuccess(ctx.stdout, result)
		}

		forceRefresh := time.Since(lastRefresh) >= waitRefreshInterval
		if forceRefresh {
			lastRefresh = time.Now()
			marker, err = store.ChangeState(root)
			if err != nil {
				return contract.NewError(contract.ErrIOError, "Cannot inspect ticket change marker: "+err.Error(), nil)
			}
			continue
		}
		for {
			time.Sleep(waitPollInterval)
			current, readErr := store.ChangeState(root)
			if readErr != nil {
				return contract.NewError(contract.ErrIOError, "Cannot inspect ticket change marker: "+readErr.Error(), nil)
			}
			if current != marker || time.Since(lastRefresh) >= waitRefreshInterval {
				marker = current
				lastRefresh = time.Now()
				break
			}
		}
	}
}

func attemptWait(ctx *commandContext, opts domain.NextOptions) (*domain.NextResult, string, error) {
	st, backend, err := openSynchronizedStore(&ctx.globalOpts, ctx.cwd)
	if err != nil {
		return nil, "", err
	}
	root := st.Root
	defer st.Close()
	result, err := domain.NextWithOptions(st, opts)
	if err != nil {
		return nil, root, err
	}
	if result.Item == nil {
		return result, root, nil
	}
	if opts.Claim {
		if err := persistMutation(backend, st, "wait", result); err != nil {
			return nil, root, err
		}
		if result.Changed {
			if err := st.SignalChange(); err != nil {
				return nil, root, contract.NewError(contract.ErrIOError, "Cannot signal ticket change: "+err.Error(), nil)
			}
		}
	}
	rememberCurrentTicket(st, result)
	return result, root, nil
}

func currentChangeState(ctx *commandContext) (string, error) {
	root, err := store.Discover(ctx.cwd)
	if err != nil {
		return "", err
	}
	marker, err := store.ChangeState(root)
	if err != nil {
		return "", contract.NewError(contract.ErrIOError, "Cannot inspect ticket change marker: "+err.Error(), nil)
	}
	return marker, nil
}
