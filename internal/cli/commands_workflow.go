package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/identity"
	"github.com/toolsupply/ticket/internal/store"
)

type EditResult struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
}

func cmdEdit(ctx *commandContext, args []string) error {
	p := ctx.newParser()
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "edit")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	if ctx.json {
		return contract.NewError(contract.ErrInvalidArgument,
			"edit is interactive and cannot be combined with JSON mode.", nil)
	}
	ref, err := optionalTicketRef(p, "edit")
	if err != nil {
		return err
	}
	return editWithEditor(ctx, ref)
}

func cmdSubmit(ctx *commandContext, args []string) error {
	return cmdWorkflowMove(ctx, args, "submit", func(st *store.Store, id, actor string, handoff, message *string) (any, error) {
		return domain.Submit(st, id, domain.SubmitOptions{Actor: actor, Handoff: handoff, Message: message})
	})
}

func cmdHold(ctx *commandContext, args []string) error {
	return cmdWorkflowMove(ctx, args, "hold", func(st *store.Store, id, actor string, handoff, message *string) (any, error) {
		return domain.Hold(st, id, domain.HoldOptions{Actor: actor, Handoff: handoff, Message: message})
	})
}

func cmdOpen(ctx *commandContext, args []string) error {
	p := ctx.newParser()
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var handoff string
	var handoffSet bool
	var message string
	var messageSet bool
	var claim bool
	p.flag("handoff", kindString, func(value string) error { handoff, handoffSet = value, true; return nil }, false)
	p.flag("message", kindString, func(value string) error { message, messageSet = value, true; return nil }, false)
	p.alias("m", "message")
	p.boolValue("claim", &claim)
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "open")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	actor := ctx.actor
	if actor == "" {
		actor = os.Getenv("TICKET_ACTOR")
	}
	if claim {
		var err error
		actor, err = ctx.actorToken()
		if err != nil {
			return err
		}
	}
	if len(p.positionals) > 2 {
		return contract.NewError(contract.ErrInvalidArgument,
			"Command open accepts an optional ID and handoff text.", nil)
	}
	positionals := append([]string(nil), p.positionals...)
	return runRepoCommand(ctx, "open", func(st *store.Store) (any, error) {
		ref := ""
		if len(positionals) == 2 {
			ref = positionals[0]
			if handoffSet {
				return nil, contract.NewError(contract.ErrInvalidArgument,
					"Handoff cannot be supplied both positionally and with --handoff.", nil)
			}
			handoff, handoffSet = positionals[1], true
		} else if len(positionals) == 1 {
			if looksLikeTicketRef(positionals[0]) {
				ref = positionals[0]
			} else {
				if handoffSet {
					return nil, contract.NewError(contract.ErrInvalidArgument,
						"Handoff cannot be supplied both positionally and with --handoff.", nil)
				}
				handoff, handoffSet = positionals[0], true
			}
		}
		resolved, err := resolveTicketRef(st, ctx, ref, "open")
		if err != nil {
			return nil, err
		}
		ref = resolved
		var handoffPtr *string
		var messagePtr *string
		if handoffSet {
			handoffPtr = &handoff
		}
		if messageSet {
			messagePtr = &message
		}
		return domain.Open(st, ref, domain.OpenOptions{Handoff: handoffPtr, Actor: actor, Message: messagePtr, Claim: claim})
	})
}

func cmdState(ctx *commandContext, args []string) error {
	p := ctx.newParser()
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var message string
	var messageSet bool
	p.flag("message", kindString, func(value string) error { message, messageSet = value, true; return nil }, false)
	p.alias("m", "message")
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "state")
	}
	if len(p.positionals) != 2 {
		return contract.NewError(contract.ErrInvalidArgument,
			"Command state requires a full ticket ID and a state.", nil)
	}
	if _, _, ok := store.ParseID(p.positionals[0]); !ok {
		return contract.NewError(contract.ErrInvalidArgument,
			"Command state requires a full canonical ticket ID.", nil)
	}
	state := p.positionals[1]
	if !isTicketState(state) || state == "all" {
		return contract.NewError(contract.ErrInvalidArgument,
			"State must be open, hold, review, signoff, closed, or rejected.", nil)
	}
	if err := ctx.check(); err != nil {
		return err
	}
	var messagePtr *string
	if messageSet {
		messagePtr = &message
	}
	return runRepoCommand(ctx, "state", func(st *store.Store) (any, error) {
		return domain.SetState(st, p.positionals[0], state, domain.StateOptions{
			Actor: os.Getenv("TICKET_ACTOR"), Message: messagePtr,
		})
	})
}

func cmdReview(ctx *commandContext, args []string) error {
	p := ctx.newParser()
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var message string
	var messageSet bool
	p.flag("message", kindString, func(value string) error { message, messageSet = value, true; return nil }, false)
	p.alias("m", "message")
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "review")
	}
	ref, err := optionalTicketRef(p, "review")
	if err != nil {
		return err
	}
	if err := ctx.check(); err != nil {
		return err
	}
	var messagePtr *string
	if messageSet {
		messagePtr = &message
	}
	actor, _ := ctx.effectiveActor()
	return runRepoCommand(ctx, "review", func(st *store.Store) (any, error) {
		ref, err = resolveTicketRef(st, ctx, ref, "review")
		if err != nil {
			return nil, err
		}
		return domain.Review(st, ref, domain.StateOptions{Actor: actor, Message: messagePtr})
	})
}

func looksLikeTicketRef(value string) bool {
	if _, _, ok := store.ParseID(value); ok {
		return true
	}
	return identity.ValidShorthand(value)
}

func cmdWorkflowMove(ctx *commandContext, args []string, command string, run func(*store.Store, string, string, *string, *string) (any, error)) error {
	p := ctx.newParser()
	ctx.register(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var handoff string
	var handoffSet bool
	var message string
	var messageSet bool
	p.flag("handoff", kindString, func(value string) error { handoff, handoffSet = value, true; return nil }, false)
	p.flag("message", kindString, func(value string) error { message, messageSet = value, true; return nil }, false)
	p.alias("m", "message")
	if err := p.parse(args); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, command)
	}
	if err := ctx.check(); err != nil {
		return err
	}
	ref, err := optionalTicketRef(p, command)
	if err != nil {
		return err
	}
	actor := ctx.actor
	if actor == "" {
		actor = os.Getenv("TICKET_ACTOR")
	}
	return runRepoCommand(ctx, command, func(st *store.Store) (any, error) {
		ref, err = resolveTicketRef(st, ctx, ref, command)
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
		return run(st, ref, actor, handoffPtr, messagePtr)
	})
}

func selectedEditor(configs ...*globalOpts) string {
	if value := strings.TrimSpace(os.Getenv("TICKET_EDITOR")); value != "" {
		return value
	}
	if len(configs) > 0 && configs[0] != nil {
		if value := strings.TrimSpace(configs[0].config.Editor); value != "" {
			return value
		}
	}
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	if runtime.GOOS == "windows" {
		return "notepad.exe"
	}
	return "vi"
}

func editorInvocation(editor, path string, line int) (string, []string, error) {
	parts, err := splitEditorCommand(editor)
	if err != nil {
		return "", nil, err
	}
	if len(parts) == 0 {
		return "", nil, contract.NewError(contract.ErrInvalidArgument, "The configured editor is empty.", nil)
	}
	command := parts[0]
	args := append([]string(nil), parts[1:]...)
	base := strings.ToLower(filepath.Base(command))
	base = strings.TrimSuffix(base, ".exe")
	if base == "vi" || base == "vim" || base == "nvim" {
		args = append(args, fmt.Sprintf("+%d", line), path)
	} else {
		args = append(args, path)
	}
	return command, args, nil
}

// splitEditorCommand tokenizes the small command form accepted for editor
// configuration. Quotes group an argument and backslash escapes quotes or
// backslashes in double-quoted text, or whitespace/quotes outside quotes.
// It deliberately never invokes a shell.
func splitEditorCommand(input string) ([]string, error) {
	var parts []string
	var current strings.Builder
	var quote rune
	started := false
	for i := 0; i < len(input); i++ {
		c := rune(input[i])
		if quote != 0 {
			if c == quote {
				quote = 0
				started = true
				continue
			}
			if c == '\\' && quote == '"' && i+1 < len(input) &&
				(input[i+1] == '\\' || input[i+1] == '"') {
				i++
				current.WriteByte(input[i])
				started = true
				continue
			}
			current.WriteRune(c)
			started = true
			continue
		}
		switch {
		case c == '\'' || c == '"':
			quote = c
			started = true
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			if started {
				parts = append(parts, current.String())
				current.Reset()
				started = false
			}
		case c == '\\' && i+1 < len(input) && isEditorEscape(input[i+1]):
			i++
			current.WriteByte(input[i])
			started = true
		default:
			current.WriteRune(c)
			started = true
		}
	}
	if quote != 0 {
		return nil, contract.NewError(contract.ErrInvalidArgument, "The configured editor has an unterminated quote.", nil)
	}
	if started {
		parts = append(parts, current.String())
	}
	return parts, nil
}

func isEditorEscape(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\\' || c == '\'' || c == '"'
}

func objectiveEditLine(t *domain.Ticket) int {
	if section, ok := t.Sections["objective"]; ok {
		// The canonical section has an empty separator line after its
		// heading. Start editing on the content line so typing does not
		// consume that separator.
		return section.HeadingLine + 2
	}
	return 1
}

// path ------------------------------------------------------------------
