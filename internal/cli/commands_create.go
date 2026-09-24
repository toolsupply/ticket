package cli

import (
	"errors"
	"io"
	"os"
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/jsonx"
	"github.com/toolsupply/ticket/internal/markdown"
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
	p := ctx.newParser()
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
	var objectivePath string
	var importPath string
	var edit bool
	p.flag("title", kindString, func(v string) error { title, hasTitle = v, true; return nil }, false)
	p.alias("t", "title")
	p.intValue("priority", &priority, &hasPriority)
	p.alias("p", "priority")
	p.repeat("tag", &tags)
	p.str("parent", &parent)
	p.repeat("depends-on", &dependsOn)
	p.str("input", &inputPath)
	p.str("objective", &objectivePath)
	p.alias("o", "objective")
	p.str("import", &importPath)
	p.boolValue("edit", &edit)
	p.alias("e", "edit")
	if err := p.parse(args); err != nil {
		return err
	}
	// Help is a side-effect-free parser result. Emit it before inspecting any
	// source path, input stream, repository, editor, or invocation-input state.
	if helpFlag {
		return emitHelpCommand(ctx, "create")
	}
	if importPath != "" {
		if inputPath != "" || objectivePath != "" || hasTitle || len(p.positionals) > 0 || hasPriority || len(tags) > 0 || parent != "" || len(dependsOn) > 0 || edit {
			return contract.NewError(contract.ErrInvalidArgument,
				"--import cannot be combined with creation options, title, Objective, structured input, or editor options.", nil)
		}
		data, err := readSource(ctx.input, importPath, maxInvocationInputBytes)
		if err != nil {
			return err
		}
		draft, err := domain.ParseTicketFile("import", data)
		if err != nil {
			return contract.NewError(contract.ErrInvalidArgument,
				"Invalid Ticket Markdown import: "+err.Error(), nil)
		}
		return runRepoCommand(ctx, "create", func(st *store.Store) (any, error) {
			return domain.CreateFromDraft(st, draft)
		})
	}
	// Preserve the explicit positional source before rewriting positionals into
	// the title. In particular, TITLE - must remain an input source when a title
	// was supplied by --title or by the first positional.
	stdinObjective := len(p.positionals) > 0 && p.positionals[len(p.positionals)-1] == "-"
	stdinPayload := stdinObjective || inputPath == "-" || objectivePath == "-" || importPath == "-"
	if ctx.inputProvided && (!stdinPayload || edit) {
		return unexpectedInvocationInput("create")
	}
	if ctx.session != nil && ctx.input == nil && (inputPath == "-" || objectivePath == "-" || importPath == "-" || (len(p.positionals) > 0 && p.positionals[len(p.positionals)-1] == "-")) {
		return sessionStdinError("create")
	}
	if err := ctx.check(); err != nil {
		return err
	}
	if edit && ctx.json {
		return contract.NewError(contract.ErrInvalidArgument, "--edit is for interactive human use and cannot be combined with JSON mode.", nil)
	}
	if stdinObjective {
		if objectivePath != "" {
			return contract.NewError(contract.ErrInvalidArgument,
				"An Objective source may be supplied only once.", nil)
		}
		objectivePath = "-"
		p.positionals = p.positionals[:len(p.positionals)-1]
	}
	if len(p.positionals) > 2 {
		return contract.NewError(contract.ErrInvalidArgument,
			"Command create accepts a title and optional Objective source.", nil)
	}
	var positionalObjective string
	var hasPositionalObjective bool
	if len(p.positionals) == 1 {
		if hasTitle {
			return contract.NewError(contract.ErrInvalidArgument,
				"Create accepts either a positional title or --title, not both.", nil)
		} else {
			title = p.positionals[0]
		}
	} else if len(p.positionals) == 2 {
		if hasTitle {
			return contract.NewError(contract.ErrInvalidArgument,
				"Create accepts either positional title and objective or --title, not both.", nil)
		}
		title = p.positionals[0]
		positionalObjective = p.positionals[1]
		hasPositionalObjective = true
	}
	if hasPositionalObjective && objectivePath != "" {
		return contract.NewError(contract.ErrInvalidArgument,
			"An Objective source may be supplied only once.", nil)
	}
	if objectivePath != "" && inputPath != "" {
		return contract.NewError(contract.ErrInvalidArgument,
			"--objective cannot be combined with --input.", nil)
	}
	if inputPath != "" && (objectivePath != "" || len(p.positionals) > 0 || hasTitle || hasPriority || len(tags) > 0 || parent != "" || len(dependsOn) > 0 || edit) {
		return contract.NewError(contract.ErrInvalidArgument, "Create flags cannot be combined with --input.", nil)
	}
	opts := domain.CreateOptions{Title: title, Priority: priority}
	if hasPriority {
		opts.Priority = priority
	}
	opts.Tags = tags
	opts.Parent = parent
	opts.DependsOn = dependsOn
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
	} else if positionalObjective != "" {
		extracted, objective := markdown.PrepareObjective(positionalObjective, false)
		if opts.Title == "" {
			opts.Title = extracted
		}
		if strings.TrimSpace(objective) != "" {
			opts.Sections = map[string]string{"objective": objective}
		}
	} else if objectivePath != "" {
		data, err := readSource(ctx.input, objectivePath, maxInvocationInputBytes)
		if err != nil {
			return err
		}
		extracted, objective := markdown.PrepareObjective(string(data), opts.Title == "")
		if opts.Title == "" {
			opts.Title = extracted
		}
		if strings.TrimSpace(objective) != "" {
			opts.Sections = map[string]string{"objective": objective}
		}
	} else if edit {
		// Editor mode owns the interaction with the input stream; do not interpret a
		// confirmation response or editor input as piped Markdown.
		opts.Body = ""
	} else if ctx.session != nil {
		// The session transport owns its input stream. A create command without an
		// explicit payload must not consume following session commands.
		opts.Body = ""
	} else {
		body, err := readPipedCreateBody(ctx.input)
		if err != nil {
			return err
		}
		if body != "" {
			extracted, objective := markdown.PrepareObjective(body, opts.Title == "")
			if opts.Title == "" {
				opts.Title = extracted
			}
			if strings.TrimSpace(objective) != "" {
				opts.Sections = map[string]string{"objective": objective}
			}
		}
	}
	if title != "" && inputPath == "" {
		cleanTitle, titleTags, err := extractTrailingTitleTags(opts.Title)
		if err != nil {
			return err
		}
		opts.Title = cleanTitle
		opts.Tags = append(opts.Tags, titleTags...)
	}
	opts.Tags = ctx.createTags(opts.Tags)
	if opts.Title == "" && edit {
		opts.Title = "New ticket"
	}
	if opts.Title == "" {
		return contract.NewError(contract.ErrInvalidArgument,
			"A title is required; provide a title or begin Objective input with an H1.", nil)
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
	data, err := readStdin(input, maxInvocationInputBytes)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func readCreateInput(input io.Reader, path string) (*createInput, error) {
	data, err := readSource(input, path, maxInvocationInputBytes)
	if err != nil {
		return nil, err
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
