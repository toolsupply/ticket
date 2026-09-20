package cli

import (
	"fmt"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/store"
)

// repositoryInfo is the stable metadata projection exposed to integrations.
// Optional values are explicit nulls so clients can distinguish an unnamed
// repository or an invocation without a selected scope.
type repositoryInfo struct {
	Path           string  `json:"path"`
	Name           *string `json:"name"`
	FormatVersion  int     `json:"format_version"`
	StorageVersion int     `json:"storage_version"`
	Scope          *string `json:"scope"`
}

func cmdInfo(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "info")
	}
	if err := p.requireNoPositionals("info"); err != nil {
		return err
	}
	if err := ctx.check(); err != nil {
		return err
	}
	return runRepoCommandMode(ctx, "info", false, func(st *store.Store) (any, error) {
		result := &repositoryInfo{
			Path:           st.Root,
			FormatVersion:  st.Cfg.FormatVersion,
			StorageVersion: contract.StorageVersion,
		}
		if st.Cfg.Name != "" {
			name := st.Cfg.Name
			result.Name = &name
		}
		if ctx.scopeName != "" {
			scope := ctx.scopeName
			result.Scope = &scope
		}
		return result, nil
	})
}

func renderRepositoryInfoHuman(ctx *commandContext, result *repositoryInfo) {
	name := "(unnamed)"
	if result.Name != nil {
		name = *result.Name
	}
	scope := "(none)"
	if result.Scope != nil {
		scope = *result.Scope
	}
	fmt.Fprintf(ctx.stdout, "path:            %s\nname:            %s\nformat_version:  %d\nstorage_version: %d\nscope:            %s\n",
		result.Path, name, result.FormatVersion, result.StorageVersion, scope)
}
