package cli

import (
	"errors"
	"io"
	"os"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/jsonx"
	"github.com/toolsupply/ticket/internal/store"
)

type createInput struct {
	Title     string            `json:"title"`
	Priority  *int              `json:"priority"`
	Tags      []string          `json:"tags"`
	Parent    string            `json:"parent"`
	DependsOn []string          `json:"depends_on"`
	Sections  map[string]string `json:"sections"`
}

func cmdCreate(ctx *commandContext, args []string) error {
	p := &parser{}
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	var title string
	var hasTitle bool
	priority := 2
	var hasPriority bool
	var tags, dependsOn []string
	var parent string
	var inputPath string
	var edit bool
	p.flag("title", kindString, func(v string) error { title, hasTitle = v, true; return nil }, false)
	p.alias("t", "title")
	p.intValue("priority", &priority, &hasPriority)
	p.alias("p", "priority")
	p.repeat("tag", &tags)
	p.str("parent", &parent)
	p.repeat("depends-on", &dependsOn)
	p.str("input", &inputPath)
	p.boolValue("edit", &edit)
	p.alias("e", "edit")
	if err := p.parse(args); err != nil {
		return err
	}
	stdinObjective := len(p.positionals) > 0 && p.positionals[len(p.positionals)-1] == "-"
	if ctx.inputProvided && (!stdinObjective && inputPath != "-" || edit) {
		return unexpectedInvocationInput("create")
	}
	if helpFlag {
		return emitHelpCommand(ctx, "create")
	}
	if ctx.session != nil && ctx.input == nil && (inputPath == "-" || (len(p.positionals) > 0 && p.positionals[len(p.positionals)-1] == "-")) {
		return sessionStdinError("create")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	if edit && ctx.json {
		return contract.NewError(contract.ErrInvalidArgument, "--edit is for interactive human use and cannot be combined with JSON mode.", nil)
	}
	if stdinObjective {
		p.positionals = p.positionals[:len(p.positionals)-1]
	}
	if len(p.positionals) > 2 {
		return contract.NewError(contract.ErrInvalidArgument,
			"Command create accepts a title and optional objective.", nil)
	}
	if len(p.positionals) == 1 {
		if hasTitle {
			return contract.NewError(contract.ErrInvalidArgument,
				"Create accepts either a positional title or --title, not both.", nil)
		}
		title = p.positionals[0]
	} else if len(p.positionals) == 2 {
		if hasTitle {
			return contract.NewError(contract.ErrInvalidArgument,
				"Create accepts either positional title and objective or --title, not both.", nil)
		}
		title = p.positionals[0]
	}
	opts := domain.CreateOptions{Title: title, Priority: priority}
	var positionalObjective string
	if len(p.positionals) == 2 {
		positionalObjective = p.positionals[1]
		opts.Sections = map[string]string{"objective": positionalObjective}
	}
	if hasPriority {
		opts.Priority = priority
	}
	opts.Tags = tags
	opts.Parent = parent
	opts.DependsOn = dependsOn
	flagUsed := hasTitle || len(p.positionals) > 0 || stdinObjective || hasPriority || len(tags) > 0 || parent != "" || len(dependsOn) > 0 || edit
	if inputPath != "" && flagUsed {
		return contract.NewError(contract.ErrInvalidArgument,
			"Create flags cannot be combined with --input.", nil)
	}
	if inputPath != "" {
		in, err := readCreateInput(ctx.input, inputPath)
		if err != nil {
			return err
		}
		opts.Title = in.Title
		if in.Priority != nil {
			opts.Priority = *in.Priority
		}
		opts.Tags = in.Tags
		opts.Parent = in.Parent
		opts.DependsOn = in.DependsOn
		opts.Sections = in.Sections
	} else if stdinObjective {
		data, err := readStdin(ctx.input, maxInputBytes)
		if err != nil {
			return err
		}
		opts.Sections = map[string]string{"objective": string(data)}
	} else if edit {
		// Editor mode owns the interaction with stdin; do not interpret a
		// confirmation response or editor input as piped Markdown.
		opts.Body = ""
	} else if ctx.session != nil {
		// The session transport owns stdin. A create command without an
		// explicit payload must not consume following session commands.
		opts.Body = ""
	} else {
		body, err := readPipedCreateBody(ctx.input)
		if err != nil {
			return err
		}
		if positionalObjective != "" && body != "" {
			return contract.NewError(contract.ErrInvalidArgument,
				"A positional objective cannot be combined with piped Markdown.", nil)
		}
		opts.Body = body
	}
	opts.Tags = ctx.createTags(opts.Tags)
	if opts.Title == "" && edit {
		opts.Title = "New ticket"
	}
	if opts.Title == "" {
		return contract.NewError(contract.ErrInvalidArgument,
			"A title is required for create.", nil)
	}
	if edit {
		return createWithEditor(ctx, opts)
	}
	return runRepoCommand(ctx, "create", func(st *store.Store) (any, error) {
		res, err := domain.Create(st, opts)
		if err != nil {
			return nil, err
		}
		return res, nil
	})
}

func cmdNew(ctx *commandContext, args []string) error {
	if len(args) == 0 {
		return cmdCreate(ctx, []string{"--edit"})
	}
	return cmdCreate(ctx, args)
}
func readPipedCreateBody(input io.Reader) (string, error) {
	if input == nil {
		return "", nil
	}
	if file, ok := input.(*os.File); ok {
		info, err := file.Stat()
		if err != nil {
			return "", contract.NewError(contract.ErrIOError,
				"Could not inspect standard input: "+err.Error(), nil)
		}
		if info.Mode()&os.ModeCharDevice != 0 {
			return "", nil
		}
	}
	data, err := readStdin(input, 2<<20)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func readCreateInput(input io.Reader, path string) (*createInput, error) {
	if path != "-" {
		return nil, contract.NewError(contract.ErrInvalidArgument,
			"Structured input must be provided with --input -.", nil)
	}
	data, err := readStdin(input, 2<<20)
	if err != nil {
		return nil, contract.NewError(contract.ErrFileTooLarge,
			"Input could not be read: "+err.Error(), nil)
	}
	if len(data) > 2<<20 {
		return nil, contract.NewError(contract.ErrFileTooLarge,
			"Input exceeds the 2 MiB limit.", nil)
	}
	var in createInput
	if err := jsonx.Decode(data, &in); err != nil {
		if errors.Is(err, jsonx.ErrUnknownField) {
			return nil, contract.NewError(contract.ErrInvalidArgument,
				"Unknown key in --input JSON: "+err.Error(), nil)
		}
		return nil, contract.NewError(contract.ErrInvalidJSON,
			"Invalid --input JSON: "+err.Error(), nil)
	}
	return &in, nil
}

// list ------------------------------------------------------------------
