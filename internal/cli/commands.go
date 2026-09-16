// Command dispatch: every v1 command maps to one success object or one
// error object.
package cli

import (
	"bytes"
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
	json  bool
	actor string
	debug bool
}

func (g *globalOpts) register(p *parser) {
	p.boolValue("json", &g.json)
	p.str("actor", &g.actor)
	p.boolValue("debug", &g.debug)
}

func (g *globalOpts) registerWithoutActor(p *parser) {
	p.boolValue("json", &g.json)
	p.boolValue("debug", &g.debug)
}

func (g *globalOpts) check() error {
	return nil
}

func (g *globalOpts) openStore(cwd string) (*store.Store, error) {
	st, err := store.Open(cwd, store.OpenOptions{})
	if err != nil {
		return nil, err
	}
	return st, nil
}

func (g *globalOpts) actorToken() (string, error) {
	a := g.actor
	if a == "" {
		a = os.Getenv("TICKET_ACTOR")
	}
	if a == "" {
		return "", contract.NewError(contract.ErrMissingActor,
			"An actor is required; set --actor or TICKET_ACTOR.", nil)
	}
	return a, nil
}

// commandContext carries the invocation through one command.
type commandContext struct {
	stdout   *bytes.Buffer
	cwd      string
	markdown bool
	globalOpts
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
		if err := st.SignalChange(); err != nil {
			return contract.NewError(contract.ErrIOError, "Cannot signal ticket change: "+err.Error(), nil)
		}
	}
	rememberCurrentTicket(st, res)
	if !ctx.json {
		return renderHumanTo(stdout, cmd, res, ctx.markdown)
	}
	return emitSuccess(stdout, res)
}

func openSynchronizedStore(g *globalOpts, cwd string) (*store.Store, scm.Backend, error) {
	st, err := g.openStore(cwd)
	if err != nil {
		return nil, nil, err
	}
	backend, err := scm.Configure()
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
	if err := backend.Commit(st.Root, commitMessage(cmd, result), []string{"."}); err != nil {
		return err
	}
	return backend.Publish(st.Root)
}

func mutationCommand(command string) bool {
	switch command {
	case "create", "new", "delete", "bump", "edit", "update", "claim", "release", "submit", "hold", "open", "approve", "reject", "close":
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
		_ = os.WriteFile(filepath.Join(st.Root, ".local", "current"), []byte(id+"\n"), 0o600)
	}
}
