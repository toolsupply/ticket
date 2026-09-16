// Create operation: validate input, resolve references, retry on ID
// collision, and publish a complete temporary sibling directory.
package domain

import (
	"errors"
	"strings"

	"ticket/internal/contract"
	"ticket/internal/markdown"
	"ticket/internal/store"
)

// CreateOptions are the create request fields.
type CreateOptions struct {
	Title     string
	Priority  int
	Tags      []string
	Parent    string
	DependsOn []string
	Sections  map[string]string // known keys -> content
	Body      string            // optional complete Markdown after the managed title
}

// CreateResult is the create response.
type CreateResult struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Changed   bool   `json:"changed"`
	State     string `json:"-"`
	Priority  int    `json:"-"`
	Objective string `json:"-"`
}

// maxCollisionRetries bounds ID collision retries.
const maxCollisionRetries = 16

// Create runs the create operation on an open store.
func Create(st *store.Store, opts CreateOptions) (*CreateResult, error) {
	// Validate and normalise section content before touching the repository.
	sections, err := validateAndNormalizeSections(opts.Sections)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(opts.Title) == "" {
		return nil, contract.NewError(contract.ErrInvalidArgument, "A nonempty title is required.", nil)
	}
	if len([]rune(opts.Title)) > 240 {
		return nil, contract.NewError(contract.ErrInvalidArgument, "The title exceeds 240 characters.", nil)
	}
	if strings.ContainsAny(opts.Title, "\n\r") {
		return nil, contract.NewError(contract.ErrInvalidArgument, "The title must not contain line breaks.", nil)
	}
	body, err := validateCreateBody(opts.Title, opts.Body)
	if err != nil {
		return nil, err
	}
	if opts.Priority < 0 || opts.Priority > 4 {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Priority must be an integer 0-4.", nil)
	}
	tags, err := normalizeTags(opts.Tags)
	if err != nil {
		return nil, err
	}
	depends, err := validateUnique(opts.DependsOn, "depends_on")
	if err != nil {
		return nil, err
	}
	for _, id := range append([]string{}, depends...) {
		if _, _, ok := store.ParseID(id); !ok {
			return nil, contract.NewError(contract.ErrInvalidArgument,
				"Reference "+id+" is not a valid ticket ID.", nil)
		}
	}
	if _, _, ok := store.ParseID(opts.Parent); !ok && opts.Parent != "" {
		return nil, contract.NewError(contract.ErrInvalidArgument,
			"Reference "+opts.Parent+" is not a valid ticket ID.", nil)
	}
	// Resolve references: existence and shorthand disambiguation.
	parent, err := resolveRef(st, opts.Parent, "parent")
	if err != nil {
		return nil, err
	}
	for i, id := range depends {
		full, err := resolveRef(st, id, "depends_on")
		if err != nil {
			return nil, err
		}
		depends[i] = full
	}
	graphTickets, err := loadGraph(st)
	if err != nil {
		return nil, err
	}
	data := RenderNew(opts.Title, opts.Priority, tags, parent, depends, sections)
	if body != "" {
		data = RenderNewWithBody(opts.Title, opts.Priority, tags, parent, depends, body)
	}
	var lastErr error
	for attempt := 0; attempt < maxCollisionRetries; attempt++ {
		id, err := st.NewID("")
		if err != nil {
			return nil, contract.NewError(contract.ErrRandomnessUnavailable,
				"Randomness is unavailable; cannot generate an ID.", nil)
		}
		// An occupied ID is a publication collision, not a proposed graph
		// node. Let the exclusive publisher perform the authoritative race
		// check before validating a candidate that is genuinely new.
		occupied, statErr := st.TicketExists(id)
		if statErr != nil {
			return nil, statErr
		}
		if !occupied {
			candidate := &Ticket{ID: id, Parent: parent, DependsOn: depends}
			if err := validateGraphCandidate(graphTickets, candidate); err != nil {
				return nil, err
			}
		}
		parsed, parseErr := ParseTicketFile(id, data)
		if parseErr != nil {
			return nil, parseErr
		}
		err = st.PublishTicket(id, data, TaskMaxBytes)
		if err == nil {
			return &CreateResult{ID: id, Path: id + "/TASK.md", Changed: true,
				State: parsed.State, Priority: parsed.Priority, Objective: objectivePreview(parsed)}, nil
		}
		if !errors.Is(err, store.ErrTargetExists) {
			return nil, err
		}
		lastErr = err
	}
	_ = lastErr
	return nil, contract.NewError(contract.ErrInternalError, "ID collisions exceeded the retry budget.", nil)
}

func validateCreateBody(title, body string) (string, error) {
	if body == "" {
		return "", nil
	}
	full := []byte("# " + title + "\n\n" + body)
	parsed := markdown.ParseBody(full)
	for _, diagnostic := range parsed.Diagnostics {
		if diagnostic.IsError() {
			return "", contract.NewError(contract.ErrInvalidArgument,
				"The supplied Markdown body is invalid: "+diagnostic.Message, map[string]any{"line": diagnostic.Line})
		}
	}
	return normalizeSection(body), nil
}

// validateAndNormalizeSections validates and normalises the create sections.
// It is pure content processing (no external state) so it can run before
// reference resolution.
func validateAndNormalizeSections(sectionsIn map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for key, content := range sectionsIn {
		if key == "outcome" {
			return nil, contract.NewError(contract.ErrInvalidArgument, "Outcome is not accepted at creation.", nil)
		}
		if key != "objective" && key != "acceptance" && key != "handoff" {
			return nil, contract.NewError(contract.ErrInvalidArgument, "Unknown section key "+key+" at creation.", nil)
		}
		norm, err := ValidateSectionContent(key, content)
		if err != nil {
			return nil, err
		}
		out[key] = norm
	}
	return out, nil
}

// resolveRef resolves full or shorthand ticket IDs to full IDs,
// enforcing existence for references.
func resolveRef(st *store.Store, id, field string) (string, error) {
	if id == "" {
		return "", nil
	}
	full, err := st.ResolveID(id, true)
	if err != nil {
		if cErr, ok := err.(*contract.Error); ok {
			cErr.Details = mergeDetails(cErr.Details, map[string]any{"field": field, "id": id})
		}
		return "", err
	}
	return full, nil
}

func mergeDetails(details, extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range details {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func validateUnique(values []string, field string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if seen[v] {
			return nil, contract.NewError(contract.ErrInvalidArgument, field+" values must be unique.", nil)
		}
		seen[v] = true
		out = append(out, v)
	}
	return out, nil
}
