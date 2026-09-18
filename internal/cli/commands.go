// Command dispatch: every v1 command maps to one success object or one
// error object.
package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"ticket/internal/contract"
	"ticket/internal/domain"
	"ticket/internal/scm"
	"ticket/internal/store"
)

// globalOpts are the flags accepted on every command.
type globalOpts struct {
	json           bool
	actor          string
	debug          bool
	scopeName      string
	configPath     string
	configExplicit bool
	scopeExplicit  bool
	configLoaded   bool
	config         userConfig
	scope          *scopeConfig
}

func (g *globalOpts) register(p *parser) {
	p.boolValue("json", &g.json)
	p.str("actor", &g.actor)
	p.boolValue("debug", &g.debug)
	g.registerConfig(p)
}

func (g *globalOpts) registerWithoutActor(p *parser) {
	p.boolValue("json", &g.json)
	p.boolValue("debug", &g.debug)
	g.registerConfig(p)
}

func (g *globalOpts) registerConfig(p *parser) {
	p.flag("config", kindString, func(value string) error {
		if g.configExplicit {
			return duplicateFlag("config")
		}
		g.configPath, g.configExplicit = value, true
		return nil
	}, false)
	p.alias("c", "config")
	p.flag("scope", kindString, func(value string) error {
		if g.scopeExplicit {
			return duplicateFlag("scope")
		}
		g.scopeName = value
		g.scopeExplicit = true
		return nil
	}, false)
}

func (g *globalOpts) check(cwd string) error {
	return g.loadConfig(cwd)
}

func (g *globalOpts) openStore(cwd string) (*store.Store, error) {
	if err := g.loadConfig(cwd); err != nil {
		return nil, err
	}
	st, err := store.Open(cwd, store.OpenOptions{Root: g.selectedRoot()})
	if err != nil {
		return nil, err
	}
	return st, nil
}

func (g *globalOpts) actorToken() (string, error) {
	a, _ := g.effectiveActor()
	if a == "" {
		return "", contract.NewError(contract.ErrMissingActor,
			"An actor is required; set --actor or TICKET_ACTOR.", nil)
	}
	return a, nil
}

func (g *globalOpts) effectiveActor() (actor, source string) {
	if g.actor != "" {
		return g.actor, "--actor"
	}
	if actor := os.Getenv("TICKET_ACTOR"); actor != "" {
		return actor, "TICKET_ACTOR"
	}
	return "", ""
}

// commandContext carries the invocation through one command.
type commandContext struct {
	stdout   *bytes.Buffer
	cwd      string
	markdown bool
	globalOpts
}

func (ctx *commandContext) check() error {
	return ctx.globalOpts.check(ctx.cwd)
}

func (ctx *commandContext) createTags(explicit []string) []string {
	return ctx.globalOpts.createTags(explicit)
}

func (ctx *commandContext) workTags(explicit []string) []string {
	return ctx.globalOpts.workTags(explicit)
}

func (ctx *commandContext) decorator() string {
	return ctx.globalOpts.decorator()
}

func (ctx *commandContext) discoverRoot() (string, error) {
	if err := ctx.globalOpts.loadConfig(ctx.cwd); err != nil {
		return "", err
	}
	return store.DiscoverWithRoot(ctx.cwd, ctx.globalOpts.selectedRoot())
}

// emitSuccess writes the success object (JSON mode).
func emitSuccess(stdout *bytes.Buffer, v any) error {
	return emitJSON(stdout, v)
}

func runRepoCommand(ctx *commandContext, cmd string, fn func(st *store.Store) (any, error)) error {
	return runRepoCommandMode(ctx, cmd, mutationCommand(cmd), fn)
}

func runRepoCommandMode(ctx *commandContext, cmd string, mutation bool, fn func(st *store.Store) (any, error)) error {
	stdout := ctx.stdout
	st, backend, err := openSynchronizedStore(&ctx.globalOpts, ctx.cwd)
	if err != nil {
		return err
	}
	defer st.Close()
	res, err := fn(st)
	if err != nil {
		return err
	}
	if mutation {
		if err := persistMutation(backend, st, cmd, res); err != nil {
			return err
		}
		_ = st.SignalChange()
	}
	normalizeShowPaths(ctx, res)
	rememberCurrentTicket(st, res)
	if !ctx.json {
		return renderHumanTo(stdout, cmd, res, ctx.markdown, ctx.decorator())
	}
	return emitSuccess(stdout, res)
}

// normalizeShowPaths makes filesystem paths in show output usable by the
// harness from its current working directory. The containment check is
// lexical; symlink handling remains the responsibility of the harness.
func normalizeShowPaths(ctx *commandContext, res any) {
	view, ok := res.(*domain.ShowView)
	if !ok || view.AttachmentPath == "" {
		return
	}
	cwd, err := filepath.Abs(ctx.cwd)
	if err != nil {
		view.AttachmentPath = ""
		return
	}
	root, err := filepath.Abs(view.TicketRoot)
	if err != nil {
		view.AttachmentPath = ""
		return
	}
	rootRel, err := filepath.Rel(cwd, root)
	if err != nil || !relativePathWithinRoot(rootRel) {
		view.AttachmentPath = ""
		return
	}
	attachment, err := filepath.Abs(view.AttachmentPath)
	if err != nil {
		view.AttachmentPath = ""
		return
	}
	attachmentRel, err := filepath.Rel(cwd, attachment)
	if err != nil || !relativePathWithinRoot(attachmentRel) {
		view.AttachmentPath = ""
		return
	}
	view.AttachmentPath = filepath.ToSlash(attachmentRel)
}

func relativePathWithinRoot(rel string) bool {
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func openSynchronizedStore(g *globalOpts, cwd string) (*store.Store, scm.Backend, error) {
	st, err := g.openStore(cwd)
	if err != nil {
		return nil, nil, err
	}
	kind, mode := g.effectiveSCM()
	backend, err := scm.ConfigureValues(kind, mode)
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	if backend != nil {
		if err := backend.Update(st.Root); err != nil {
			st.Close()
			return nil, nil, err
		}
		cfg, err := store.LoadConfig(st.Root)
		if err != nil {
			st.Close()
			return nil, nil, err
		}
		st.Cfg = cfg
	}
	return st, backend, nil
}

func persistMutation(backend scm.Backend, st *store.Store, cmd string, result any) error {
	if backend == nil {
		return nil
	}
	paths := []string{"."}
	paths = append(paths, deletedResultIDs(result)...)
	if err := backend.Commit(st.Root, commitMessage(cmd, result), paths); err != nil {
		return scmMutationError(err, result)
	}
	if err := backend.Publish(st.Root); err != nil {
		return scmMutationError(err, result)
	}
	return nil
}

func deletedResultIDs(result any) []string {
	switch value := result.(type) {
	case *domain.DeleteResult:
		if value.Changed {
			return []string{value.ID}
		}
	case *domain.BatchDeleteResult:
		ids := make([]string, 0, len(value.Items))
		for _, item := range value.Items {
			if item.Changed {
				ids = append(ids, item.ID)
			}
		}
		return ids
	}
	return nil
}

func scmMutationError(err error, result any) error {
	details := map[string]any{}
	applied := resultChanged(result)
	if applied {
		details["mutation_applied"] = true
		if id := changedResultID(result); id != "" {
			details["id"] = id
		}
	}
	message := "SCM persistence failed: " + scmErrorMessage(err)
	if applied {
		message = "Local ticket mutation was applied, but " + message
	}
	return contract.NewError(contract.ErrIOError, message, details)
}

func scmErrorMessage(err error) string {
	var ce *contract.Error
	if errors.As(err, &ce) {
		return ce.Message
	}
	return err.Error()
}

func resultChanged(result any) bool {
	switch value := result.(type) {
	case *domain.CreateResult:
		return value.Changed
	case *domain.DeleteResult:
		return value.Changed
	case *domain.BatchDeleteResult:
		for _, item := range value.Items {
			if item.Changed {
				return true
			}
		}
	case *EditResult:
		return value.Changed
	case *domain.UpdateResult:
		return value.Changed
	case *domain.ClaimResult:
		return value.Changed
	case *domain.ReleaseResult:
		return value.Changed
	case *domain.TransitionResult:
		return value.Changed
	case *domain.BatchTransitionResult:
		for _, item := range value.Items {
			if item.Changed {
				return true
			}
		}
	case *domain.NextResult:
		return value.Changed
	}
	return false
}

func changedResultID(result any) string {
	switch value := result.(type) {
	case *domain.BatchDeleteResult:
		for _, item := range value.Items {
			if item.Changed {
				return item.ID
			}
		}
	case *domain.BatchTransitionResult:
		for _, item := range value.Items {
			if item.Changed {
				return item.ID
			}
		}
	default:
		return resultID(result)
	}
	return ""
}

func mutationCommand(command string) bool {
	switch command {
	case "create", "new", "delete", "bump", "edit", "update", "claim", "release", "submit", "hold", "open", "review", "state", "approve", "reject", "close":
		return true
	default:
		return false
	}
}

func commitMessage(command string, result any) string {
	message := "ticket: " + command
	if id := resultID(result); id != "" {
		message += " " + id
	}
	return message
}

func resultID(result any) string {
	switch value := result.(type) {
	case *domain.CreateResult:
		return value.ID
	case *domain.DeleteResult:
		return value.ID
	case *domain.BatchDeleteResult:
		if len(value.Items) > 0 {
			return value.Items[0].ID
		}
	case *EditResult:
		return value.ID
	case *domain.UpdateResult:
		return value.ID
	case *domain.ClaimResult:
		return value.ID
	case *domain.ReleaseResult:
		return value.ID
	case *domain.TransitionResult:
		return value.ID
	case *domain.NextResult:
		if value.Item != nil {
			return value.Item.ID
		}
	case *domain.BatchTransitionResult:
		if len(value.Items) > 0 {
			return value.Items[0].ID
		}
	}
	return ""
}

// optionalTicketRef permits the human convenience form with no positional
// ID. JSON invocations remain explicit so agent and script behavior cannot
// depend on ticket-root-local state.
func optionalTicketRef(p *parser, command string) (string, error) {
	if len(p.positionals) > 1 {
		return "", contract.NewError(contract.ErrInvalidArgument,
			"Command "+command+" accepts at most one ID.", nil)
	}
	if len(p.positionals) == 0 {
		return "", nil
	}
	return p.positionals[0], nil
}

func resolveTicketRef(st *store.Store, ctx *commandContext, ref, command string) (string, error) {
	if ref != "" {
		return ref, nil
	}
	if ctx.json {
		return "", contract.NewError(contract.ErrInvalidArgument,
			"Command "+command+" requires an ID in JSON mode.", nil)
	}
	if current := strings.TrimSpace(os.Getenv("TICKET_CURRENT")); current != "" {
		return current, nil
	}
	data, err := os.ReadFile(filepath.Join(st.Root, ".local", "current"))
	if err == nil && strings.TrimSpace(string(data)) != "" {
		return strings.TrimSpace(string(data)), nil
	}
	return "", contract.NewError(contract.ErrInvalidArgument,
		"Command "+command+" requires an ID; no current ticket is set.", nil)
}

func emitCurrentSummaryOrHelp(ctx *commandContext) error {
	st, _, err := openSynchronizedStore(&ctx.globalOpts, ctx.cwd)
	if err != nil {
		var ce *contract.Error
		if errors.As(err, &ce) && ce.Code == contract.ErrRepoNotFound {
			return emitTopLevelHelp(ctx)
		}
		return err
	}
	defer st.Close()
	ref := strings.TrimSpace(os.Getenv("TICKET_CURRENT"))
	if ref == "" {
		data, readErr := os.ReadFile(filepath.Join(st.Root, ".local", "current"))
		if readErr == nil {
			ref = strings.TrimSpace(string(data))
		} else if !os.IsNotExist(readErr) {
			return contract.NewError(contract.ErrIOError, "Cannot read current ticket: "+readErr.Error(), nil)
		}
	}
	if ref == "" {
		return emitTopLevelHelp(ctx)
	}
	full, err := st.ResolveID(ref, true)
	if err != nil {
		return err
	}
	ticket, err := domain.ReadTicket(st, full)
	if err != nil {
		return err
	}
	return renderHumanTo(ctx.stdout, "current", &currentTicketSummary{
		ID: ticket.ID, State: ticket.State, Title: ticket.Title,
	}, false)
}

func rememberCurrentTicket(st *store.Store, result any) {
	var id string
	switch value := result.(type) {
	case *domain.CreateResult:
		id = value.ID
	case *EditResult:
		id = value.ID
	case *domain.ShowView:
		id = value.ID
	case *domain.StatusView:
		id = value.ID
	case *domain.UpdateResult:
		id = value.ID
	case *domain.ClaimResult:
		id = value.ID
	case *domain.ReleaseResult:
		id = value.ID
	case *domain.TransitionResult:
		id = value.ID
	case *domain.NextResult:
		if value.Item != nil {
			id = value.Item.ID
		}
	case map[string]string:
		id = value["id"]
	}
	if id != "" {
		_ = st.RememberCurrent(id)
	}
}
