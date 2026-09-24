package cli

import (
	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/store"
)

func cmdGraph(ctx *commandContext, args []string) error {
	p := ctx.newParser()
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var reverse, all, archived, ascii, unicode bool
	var queryTokens []string
	var querySet bool
	var depth int
	var depthSet bool
	p.boolValue("reverse", &reverse)
	p.boolValue("all", &all)
	p.boolValue("archived", &archived)
	p.boolValue("ascii", &ascii)
	p.boolValue("unicode", &unicode)
	p.intValue("depth", &depth, &depthSet)
	registerQueryTail(p, &queryTokens, &querySet)
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "graph")
	}
	if ascii && unicode {
		return contract.NewError(contract.ErrInvalidArgument, "--ascii and --unicode cannot be combined.", nil)
	}
	if err := ctx.check(); err != nil {
		return err
	}
	if all && archived {
		return contract.NewError(contract.ErrInvalidArgument, "Include archived and archived-only graph modes conflict.", nil)
	}
	if len(p.positionals) == 0 && !querySet && !all && !archived {
		// Bare graph follows the same useful active-work default as the
		// documented `ticket graph not terminal` form.
		queryTokens = []string{"not", "terminal"}
		querySet = true
	}
	prepared, err := prepareTargetSpec(p.positionals, queryTokens, querySet, targetSpecOptions{
		Scope: graphQueryScope(all, archived),
	})
	if err != nil {
		return err
	}
	if prepared.HasQuery && depthSet {
		return contract.NewError(contract.ErrInvalidArgument, "Selected-set graph does not support --depth.", nil)
	}
	if !prepared.HasQuery && len(prepared.ExplicitRefs) > 1 {
		return contract.NewError(contract.ErrInvalidArgument, "Command graph accepts at most one ID.", nil)
	}
	var ref string
	if !prepared.HasQuery && len(prepared.ExplicitRefs) == 1 {
		ref = prepared.ExplicitRefs[0]
	}
	return runRepoCommand(ctx, "graph", func(st *store.Store) (any, error) {
		if prepared.HasQuery {
			set, err := domain.EvaluateTarget(st, prepared.Target)
			if err != nil {
				return nil, err
			}
			result, err := domain.GraphForTicketSet(set, domain.GraphOptions{Reverse: reverse})
			if err != nil {
				return nil, err
			}
			setGraphRenderMode(result, ascii, unicode)
			return result, nil
		}
		root := ""
		if ref != "" {
			root, err = st.ResolveID(ref, true)
			if err != nil {
				return nil, err
			}
		}
		result, err := domain.Graph(st, domain.GraphOptions{
			RootID: root, Reverse: reverse, Depth: depth, DepthSet: depthSet,
			IncludeArchived: all, ArchivedOnly: archived,
		})
		if err != nil {
			return nil, err
		}
		setGraphRenderMode(result, ascii, unicode)
		return result, nil
	})
}

func graphQueryScope(all, archived bool) domain.QueryScope {
	if archived {
		return domain.ScopeArchived
	}
	if all {
		return domain.ScopeAll
	}
	return domain.ScopeActive
}

func setGraphRenderMode(result *domain.GraphResult, ascii, unicode bool) {
	switch {
	case ascii:
		result.RenderMode = "ascii"
	case unicode:
		result.RenderMode = "unicode"
	}
}
