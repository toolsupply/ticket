package cli

import (
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/identity"
)

// targetSpecOptions describes the compatibility surface of a set-aware
// command. Legacy selectors are classified here, while the consumer remains
// responsible for applying command-specific rules such as whether "all" is
// meaningful.
type targetSpecOptions struct {
	AllowLegacyStates bool
	AllowLegacyAll    bool
	Scope             domain.QueryScope
}

// preparedTarget is the shared result of target classification. Target is
// the common domain selection model. The compatibility fields let a command
// retain legacy behavior (notably list's historical ID/state intersection)
// without creating another parser or selector engine.
type preparedTarget struct {
	Target       domain.TargetSpec
	ExplicitRefs []string
	LegacyStates []string
	LegacyAll    bool
	HasQuery     bool
	Legacy       bool
}

// prepareTargetSpec classifies explicit IDs, legacy selectors, and one TQL
// expression from argv tokens. queryTokens are captured by the parser's
// -q/--query tail option; when present, all of them are TQL and options after
// -q are intentionally not reinterpreted as command options.
func prepareTargetSpec(positionals, queryTokens []string, querySet bool, opts targetSpecOptions) (*preparedTarget, error) {
	if querySet && len(queryTokens) == 0 {
		return nil, contract.NewError(contract.ErrInvalidArgument,
			"The --query option requires at least one TQL token.", nil)
	}
	refs, remaining, err := splitTargetReferences(positionals)
	if err != nil {
		return nil, err
	}

	legacy, queryPositionals, err := splitLegacyTargetTokens(remaining, opts)
	if err != nil {
		return nil, err
	}
	if legacy.all && len(refs) > 0 {
		return nil, contract.NewError(contract.ErrInvalidArgument,
			"The all-target selector cannot be combined with ticket IDs.", nil)
	}
	if len(legacy.states) > 0 || legacy.all {
		if querySet || len(queryPositionals) > 0 {
			return nil, ambiguousTargetError()
		}
	}
	if querySet && len(queryPositionals) > 0 {
		return nil, contract.NewError(contract.ErrInvalidArgument,
			"TQL must be supplied either positionally or with --query, not both.", nil)
	}

	queryTokens = append([]string(nil), queryTokens...)
	if !querySet {
		queryTokens = queryPositionals
	}
	var expr domain.Expr
	hasQuery := querySet || len(queryTokens) > 0
	if hasQuery {
		expr, err = parseTQL(queryTokens)
		if err != nil {
			return nil, err
		}
	}

	prepared := &preparedTarget{
		ExplicitRefs: append([]string(nil), refs...),
		LegacyStates: append([]string(nil), legacy.states...),
		LegacyAll:    legacy.all,
		HasQuery:     hasQuery,
		Legacy:       len(legacy.states) > 0 || legacy.all,
	}
	prepared.Target.ExplicitRefs = append([]string(nil), refs...)
	switch {
	case hasQuery:
		prepared.Target.Query = &domain.QuerySpec{Expr: expr, Scope: opts.Scope}
	case len(legacy.states) > 0:
		prepared.Target.Query = &domain.QuerySpec{Expr: legacyStateExpr(legacy.states), Scope: opts.Scope}
	case legacy.all:
		prepared.Target.Query = &domain.QuerySpec{Scope: opts.Scope}
	}
	return prepared, nil
}

type legacyTarget struct {
	states []string
	all    bool
}

func splitLegacyTargetTokens(tokens []string, opts targetSpecOptions) (legacyTarget, []string, error) {
	var legacy legacyTarget
	var query []string
	for _, token := range tokens {
		lower := strings.ToLower(token)
		if opts.AllowLegacyAll && (lower == "all" || lower == "*") {
			legacy.all = true
			continue
		}
		if opts.AllowLegacyStates {
			if state, ok := domain.NormalizeLifecycleState(lower); ok {
				legacy.states = append(legacy.states, state)
				continue
			}
		}
		query = append(query, token)
	}
	if legacy.all && (len(legacy.states) > 0 || len(query) > 0) {
		return legacyTarget{}, nil, contract.NewError(contract.ErrInvalidArgument,
			"The all-target selector cannot be combined with ticket IDs, states, or TQL.", nil)
	}
	return legacy, query, nil
}

func legacyStateExpr(states []string) domain.Expr {
	terms := make([]domain.Expr, 0, len(states))
	for _, state := range states {
		terms = append(terms, domain.Field("state", state))
	}
	if len(terms) == 1 {
		return terms[0]
	}
	return domain.Or(terms...)
}

func ambiguousTargetError() error {
	return contract.NewError(contract.ErrInvalidArgument,
		"Do not mix legacy state selectors with TQL; use \"state:open or state:review\".", nil)
}

// splitTargetReferences removes bare ticket references only when the complete
// input is not already a valid TQL expression. This context-sensitive step is
// important for values such as `tag eq 2026`, where a numeric value can also
// be a valid ticket shorthand.
func splitTargetReferences(tokens []string) ([]string, []string, error) {
	if len(tokens) == 0 {
		return nil, nil, nil
	}
	if _, err := parseTQL(tokens); err == nil {
		return nil, append([]string(nil), tokens...), nil
	}

	refs := make([]string, 0)
	remaining := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if looksLikeTicketRef(token) {
			refs = append(refs, token)
			continue
		}
		if malformedTicketRef(token) {
			return nil, nil, contract.NewError(contract.ErrInvalidArgument,
				"Invalid target "+token+": expected a valid ticket ID or TQL expression.", map[string]any{"target": token})
		}
		remaining = append(remaining, token)
	}
	if len(refs) == 0 {
		// Preserve the parser's precise query diagnostic for unknown words and
		// malformed expressions.
		return nil, append([]string(nil), tokens...), nil
	}
	return refs, remaining, nil
}

func malformedTicketRef(value string) bool {
	if len(value) < 9 || value[8] != '-' {
		return false
	}
	for i := 0; i < 8; i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return !identity.ValidID(value) && !identity.ValidShorthand(value)
}

// registerQueryTail adds the parser-aware query boundary used by set-aware
// commands. It is deliberately separate from command-specific target setup so
// every consumer gets identical duplicate/ordering behavior.
func registerQueryTail(p *parser, queryTokens *[]string, presence ...*bool) {
	p.tail("query", queryTokens, presence...)
	p.alias("q", "query")
}
