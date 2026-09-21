// Command dispatch: every v1 command maps to one success object or one
// error object.
package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/scm"
	"github.com/toolsupply/ticket/internal/store"
)

// globalOpts are the flags accepted on every command.
type globalOpts struct {
	json             bool
	machineTransport bool
	actor            string
	debug            bool
	scopeName        string
	configPath       string
	configExplicit   bool
	scopeExplicit    bool
	configLoaded     bool
	config           userConfig
	scope            *scopeConfig
	rootOverride     string
	sessionBound     bool
	liveWriter       io.Writer
}

func (g *globalOpts) register(p *parser) {
	g.registerJSON(p)
	p.str("actor", &g.actor)
	p.boolValue("debug", &g.debug)
	g.registerConfig(p)
}

func (g *globalOpts) registerWithoutActor(p *parser) {
	g.registerJSON(p)
	p.boolValue("debug", &g.debug)
	g.registerConfig(p)
}

func (g *globalOpts) registerJSON(p *parser) {
	if !g.machineTransport {
		p.boolValue("json", &g.json)
		return
	}
	p.flag("json", kindBool, func(value string) error {
		if value != "true" {
			return contract.NewError(contract.ErrInvalidArgument,
				"Persistent JSON transport cannot disable --json.", nil)
		}
		g.json = true
		return nil
	}, false)
}

func (g *globalOpts) registerConfig(p *parser) {
	if g.sessionBound {
		p.flag("config", kindString, func(string) error {
			return contract.NewError(contract.ErrInvalidArgument,
				"Interactive sessions cannot use --config after startup.", nil)
		}, false)
		p.alias("c", "config")
		p.flag("scope", kindString, func(string) error {
			return contract.NewError(contract.ErrInvalidArgument,
				"Interactive sessions cannot use --scope after startup.", nil)
		}, false)
		return
	}
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
	stdout        *bytes.Buffer
	cwd           string
	input         io.Reader
	inputProvided bool
	markdown      bool
	session       *sessionState
	executor      *sessionExecutor
	done          <-chan struct{}
	liveWriter    io.Writer
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
	rememberCurrentTicket(ctx, st, res)
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
		if err := backend.ValidateLocal(st.Root); err != nil {
			st.Close()
			return nil, nil, err
		}
		if err := backend.Update(st.Root); err != nil {
			st.Close()
			return nil, nil, err
		}
		if err := backend.ValidateLocal(st.Root); err != nil {
			st.Close()
			return nil, nil, err
		}
		if err := st.RevalidateAfterSync(); err != nil {
			st.Close()
			return nil, nil, err
		}
	}
	return st, backend, nil
}

func persistMutation(backend scm.Backend, st *store.Store, cmd string, result any) error {
	if backend == nil {
		return nil
	}
	paths := mutationPaths(result)
	if len(paths) == 0 {
		return nil
	}
	if err := backend.Commit(st.Root, commitMessage(cmd, result), paths); err != nil {
		return scmMutationError(err, result)
	}
	if err := backend.Publish(st.Root); err != nil {
		return scmMutationError(err, result)
	}
	return nil
}

func mutationPaths(result any) []string {
	switch value := result.(type) {
	case *domain.CreateResult:
		return []string{value.Path}
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
	case *EditResult:
		return []string{value.Path}
	case *domain.UpdateResult:
		return []string{ticketTaskPath(value.ID)}
	case *domain.ClaimResult:
		return []string{ticketTaskPath(value.ID)}
	case *domain.ReleaseResult:
		return []string{ticketTaskPath(value.ID)}
	case *domain.TransitionResult:
		return []string{ticketTaskPath(value.ID)}
	case *domain.BatchTransitionResult:
		paths := make([]string, 0, len(value.Items))
		for _, item := range value.Items {
			paths = append(paths, ticketTaskPath(item.ID))
		}
		return paths
	case *domain.NextResult:
		if value.Item != nil {
			return []string{ticketTaskPath(value.Item.ID)}
		}
	}
	return nil
}

func ticketTaskPath(id string) string { return id + "/TASK.md" }

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
	metadata, ok := commandInfo(command)
	return ok && metadata.mutation
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
	if ctx.session != nil {
		if ctx.session.current != "" {
			return ctx.session.current, nil
		}
		return "", contract.NewError(contract.ErrInvalidArgument,
			"Command "+command+" requires an ID; no current ticket is set.", nil)
	}
	if current := strings.TrimSpace(os.Getenv("TICKET_CURRENT")); current != "" {
		return current, nil
	}
	data, err := store.ReadLocalFile(st.Root, "current", 128)
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
	ref := ""
	if ctx.session != nil {
		ref = ctx.session.current
	} else {
		ref = strings.TrimSpace(os.Getenv("TICKET_CURRENT"))
	}
	if ref == "" && ctx.session == nil {
		data, readErr := store.ReadLocalFile(st.Root, "current", 128)
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

func rememberCurrentTicket(ctx *commandContext, st *store.Store, result any) {
	if ctx.json {
		return
	}
	id := currentTicketID(result)
	if id == "" {
		return
	}
	if ctx.session != nil {
		ctx.session.current = id
		return
	}
	_ = st.RememberCurrent(id)
}

func currentTicketID(result any) string {
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
	return id
}
