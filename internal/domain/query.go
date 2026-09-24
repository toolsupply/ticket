package domain

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/identity"
	"github.com/toolsupply/ticket/internal/store"
)

// QueryScope selects the storage namespaces that form a query universe.
type QueryScope int

const (
	ScopeActive QueryScope = iota
	ScopeArchived
	ScopeAll
)

// SortSpec describes the deterministic orders supported by domain queries.
type SortSpec string

const (
	SortPriorityID   SortSpec = "priority-id"
	SortIDDesc       SortSpec = "id-desc"
	SortModifiedDesc SortSpec = "modified-desc"
)

// Comparison is an ordered comparison operator for query fields.
type Comparison string

const (
	ComparisonEQ Comparison = "eq"
	ComparisonNE Comparison = "ne"
	ComparisonLT Comparison = "lt"
	ComparisonLE Comparison = "le"
	ComparisonGT Comparison = "gt"
	ComparisonGE Comparison = "ge"
)

// Expr is a parser-independent query expression. Predicate, NotExpr,
// AndExpr, and OrExpr are the supported expression nodes.
type Expr interface{ queryExpr() }

// Predicate is either a bare semantic predicate or a field predicate. A
// nonempty Operator makes it an ordered comparison. Name is used for bare
// predicates such as terminal and ready.
type Predicate struct {
	Name     string
	Field    string
	Value    string
	Operator Comparison
}

func (Predicate) queryExpr() {}

// NotExpr negates its child expression.
type NotExpr struct{ Expr Expr }

func (NotExpr) queryExpr() {}

// AndExpr matches when every child matches. An empty AndExpr matches all
// tickets, which makes it useful as an explicit empty query expression.
type AndExpr struct{ Terms []Expr }

func (AndExpr) queryExpr() {}

// OrExpr matches when any child matches. An empty OrExpr matches no tickets.
type OrExpr struct{ Terms []Expr }

func (OrExpr) queryExpr() {}

// Field builds an exact field:value predicate.
func Field(field, value string) Expr { return Predicate{Field: field, Value: value} }

// Bare builds a semantic predicate such as terminal or ready.
func Bare(name string) Expr { return Predicate{Name: name} }

// Compare builds an ordered field comparison.
func Compare(field string, operator Comparison, value string) Expr {
	return Predicate{Field: field, Operator: operator, Value: value}
}

// Not builds a negated expression.
func Not(expr Expr) Expr { return NotExpr{Expr: expr} }

// And builds an implicit conjunction.
func And(exprs ...Expr) Expr { return AndExpr{Terms: append([]Expr(nil), exprs...)} }

// Or builds a disjunction.
func Or(exprs ...Expr) Expr { return OrExpr{Terms: append([]Expr(nil), exprs...)} }

// QuerySpec is a repository-independent query and its result modifiers.
type QuerySpec struct {
	Expr   Expr
	Scope  QueryScope
	Sort   SortSpec
	Limit  int
	Offset int
}

// TargetSpec represents explicit ticket references unioned with one query.
type TargetSpec struct {
	ExplicitRefs []string
	Query        *QuerySpec
}

// TicketSet is an ordered, unique set of parsed tickets. Tickets are ordered
// according to the query or explicit-reference order and are never formatted
// output strings.
type TicketSet struct {
	Tickets []*Ticket
	// More reports whether the query had additional matches after Offset and
	// beyond Limit. It is consumed by the existing ListResult projection.
	More bool
}

type queryContext struct {
	st          *store.Store
	readiness   *graphIndex
	readinessOK bool
}

// EvaluateQuery enumerates the selected storage scope once, validates its
// collection, evaluates the expression, then applies deterministic ordering,
// offset, and limit. It never mutates repository state.
func EvaluateQuery(st *store.Store, spec QuerySpec) (*TicketSet, error) {
	candidates, err := queryScopeTickets(st, spec.Scope)
	if err != nil {
		return nil, err
	}
	if spec.Offset < 0 {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Query offset must not be negative.", nil)
	}
	if spec.Limit < 0 {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Query limit must not be negative.", nil)
	}
	if err := validateSort(spec.Sort); err != nil {
		return nil, err
	}

	ctx := &queryContext{st: st}
	matcher, needsReadiness, err := compileQueryExpr(st, spec.Expr)
	if err != nil {
		return nil, err
	}
	// Archived tickets are never implementation-ready or blocked in the
	// readiness sense. Avoid loading the active graph for archive-only queries;
	// besides being unnecessary, unrelated active corruption must not affect an
	// archive-only result.
	if needsReadiness && spec.Scope != ScopeArchived {
		ctx.readiness, err = queryReadinessIndex(st, spec.Scope, candidates)
		if err != nil {
			return nil, err
		}
		ctx.readinessOK = true
	}

	selected := make([]*Ticket, 0, len(candidates))
	for _, ticket := range candidates {
		matched, err := matcher(ctx, ticket)
		if err != nil {
			return nil, err
		}
		if matched {
			selected = append(selected, ticket)
		}
	}
	if err := sortQueryTickets(st, selected, spec.Sort); err != nil {
		return nil, err
	}
	if spec.Offset >= len(selected) {
		selected = nil
	} else if spec.Offset > 0 {
		selected = selected[spec.Offset:]
	}
	more := spec.Limit > 0 && len(selected) > spec.Limit
	if more {
		selected = selected[:spec.Limit]
	}
	return &TicketSet{Tickets: selected, More: more}, nil
}

// EvaluateTarget evaluates a query and unions it with explicit references.
// Explicit references retain argv order; query results retain query order;
// first occurrence wins.
func EvaluateTarget(st *store.Store, target TargetSpec) (*TicketSet, error) {
	result := &TicketSet{Tickets: make([]*Ticket, 0, len(target.ExplicitRefs))}
	seen := make(map[string]bool, len(target.ExplicitRefs))
	for _, ref := range target.ExplicitRefs {
		full, err := mustResolve(st, ref)
		if err != nil {
			return nil, err
		}
		if seen[full] {
			continue
		}
		ticket, err := ReadTicket(st, full)
		if err != nil {
			return nil, err
		}
		seen[full] = true
		result.Tickets = append(result.Tickets, ticket)
	}
	if target.Query == nil {
		return result, nil
	}
	queried, err := EvaluateQuery(st, *target.Query)
	if err != nil {
		return nil, err
	}
	result.More = queried.More
	for _, ticket := range queried.Tickets {
		if ticket == nil || seen[ticket.ID] {
			continue
		}
		seen[ticket.ID] = true
		result.Tickets = append(result.Tickets, ticket)
	}
	return result, nil
}

type queryMatcher func(*queryContext, *Ticket) (bool, error)

func queryScopeTickets(st *store.Store, scope QueryScope) ([]*Ticket, error) {
	switch scope {
	case ScopeActive:
		tickets, diags, err := scanWith(st)
		if err != nil {
			return nil, err
		}
		if len(diags) > 0 {
			return nil, collectionError(diags)
		}
		return tickets, nil
	case ScopeArchived:
		tickets, diags, err := scanArchivedWith(st)
		if err != nil {
			return nil, err
		}
		if len(diags) > 0 {
			return nil, collectionError(diags)
		}
		return tickets, nil
	case ScopeAll:
		return loadAllGraph(st)
	default:
		return nil, contract.NewError(contract.ErrInvalidArgument, "Unknown query scope.", map[string]any{"scope": scope})
	}
}

func queryReadinessIndex(st *store.Store, scope QueryScope, candidates []*Ticket) (*graphIndex, error) {
	var tickets []*Ticket
	var err error
	switch scope {
	case ScopeActive:
		// Candidates already contain the complete active population. Expand only
		// relationships that point into the archive or another explicit scope.
		tickets = append([]*Ticket(nil), candidates...)
		byID := make(map[string]*Ticket, len(tickets))
		for _, ticket := range tickets {
			byID[ticket.ID] = ticket
		}
		tickets, err = expandGraphReferences(st, tickets, byID)
	case ScopeArchived:
		// Archive-only predicates still use full readiness semantics. The
		// archive population was already enumerated by the query; enumerate the
		// active namespace once to complete the shared graph context.
		active, diags, scanErr := scanWith(st)
		if scanErr != nil {
			return nil, scanErr
		}
		if len(diags) > 0 {
			return nil, collectionError(diags)
		}
		tickets = append(active, candidates...)
	case ScopeAll:
		tickets = candidates
	}
	if err != nil {
		return nil, err
	}
	return newGraphIndex(tickets), nil
}

func compileQueryExpr(st *store.Store, expr Expr) (queryMatcher, bool, error) {
	switch node := expr.(type) {
	case nil:
		return func(*queryContext, *Ticket) (bool, error) { return true, nil }, false, nil
	case Predicate:
		return compilePredicate(st, node)
	case *Predicate:
		if node == nil {
			return nil, false, contract.NewError(contract.ErrInvalidArgument, "Query predicate is nil.", nil)
		}
		return compilePredicate(st, *node)
	case NotExpr:
		child, needs, err := compileQueryExpr(st, node.Expr)
		if err != nil {
			return nil, false, err
		}
		return func(ctx *queryContext, ticket *Ticket) (bool, error) {
			matched, err := child(ctx, ticket)
			return !matched, err
		}, needs, nil
	case *NotExpr:
		if node == nil {
			return nil, false, contract.NewError(contract.ErrInvalidArgument, "Query expression is nil.", nil)
		}
		return compileQueryExpr(st, *node)
	case AndExpr:
		return compileCompound(st, node.Terms, true)
	case *AndExpr:
		if node == nil {
			return nil, false, contract.NewError(contract.ErrInvalidArgument, "Query expression is nil.", nil)
		}
		return compileCompound(st, node.Terms, true)
	case OrExpr:
		return compileCompound(st, node.Terms, false)
	case *OrExpr:
		if node == nil {
			return nil, false, contract.NewError(contract.ErrInvalidArgument, "Query expression is nil.", nil)
		}
		return compileCompound(st, node.Terms, false)
	default:
		return nil, false, contract.NewError(contract.ErrInvalidArgument, "Unsupported query expression.", nil)
	}
}

func compileCompound(st *store.Store, terms []Expr, conjunction bool) (queryMatcher, bool, error) {
	children := make([]queryMatcher, 0, len(terms))
	needsReadiness := false
	for _, term := range terms {
		child, needs, err := compileQueryExpr(st, term)
		if err != nil {
			return nil, false, err
		}
		children = append(children, child)
		needsReadiness = needsReadiness || needs
	}
	return func(ctx *queryContext, ticket *Ticket) (bool, error) {
		if conjunction {
			for _, child := range children {
				matched, err := child(ctx, ticket)
				if err != nil || !matched {
					return false, err
				}
			}
			return true, nil
		}
		for _, child := range children {
			matched, err := child(ctx, ticket)
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
		return false, nil
	}, needsReadiness, nil
}

func compilePredicate(st *store.Store, predicate Predicate) (queryMatcher, bool, error) {
	if predicate.Name != "" {
		name := strings.ToLower(predicate.Name)
		switch name {
		case "terminal":
			return func(_ *queryContext, ticket *Ticket) (bool, error) { return ticket.IsTerminal(), nil }, false, nil
		case "claimed":
			return func(_ *queryContext, ticket *Ticket) (bool, error) { return ticket.Assignee != "", nil }, false, nil
		case "unclaimed":
			return func(_ *queryContext, ticket *Ticket) (bool, error) { return ticket.Assignee == "", nil }, false, nil
		case "archived":
			return func(_ *queryContext, ticket *Ticket) (bool, error) { return ticket.Archived, nil }, false, nil
		case "ready", "blocked":
			wantReady := name == "ready"
			return func(ctx *queryContext, ticket *Ticket) (bool, error) {
				if !ticket.Archived && ticket.State == "open" && ctx.readinessOK {
					ready := readinessForWithChildren(ticket, ctx.readiness.byID, ctx.readiness.children).Ready
					if wantReady {
						return ready, nil
					}
					return !ready, nil
				}
				return false, nil
			}, true, nil
		default:
			return nil, false, invalidQueryPredicate("unknown semantic predicate " + predicate.Name)
		}
	}
	field := strings.ToLower(strings.TrimSpace(predicate.Field))
	if field == "" {
		return nil, false, invalidQueryPredicate("predicate is missing a field or semantic name")
	}
	operator := predicate.Operator
	if operator != "" {
		if field != "priority" {
			return nil, false, invalidQueryPredicate("only priority comparisons are supported")
		}
		want, err := parseQueryPriority(predicate.Value)
		if err != nil {
			return nil, false, err
		}
		if !validComparison(operator) {
			return nil, false, invalidQueryPredicate("unknown comparison operator " + string(operator))
		}
		return func(_ *queryContext, ticket *Ticket) (bool, error) {
			return compareInt(ticket.Priority, want, operator), nil
		}, false, nil
	}

	switch field {
	case "id":
		if _, _, ok := store.ParseID(predicate.Value); !ok && !identity.ValidShorthand(predicate.Value) {
			return nil, false, invalidQueryPredicate("invalid ticket ID " + predicate.Value)
		}
		return func(_ *queryContext, ticket *Ticket) (bool, error) {
			return strings.HasPrefix(ticket.ID, predicate.Value), nil
		}, false, nil
	case "state":
		state, ok := NormalizeLifecycleState(predicate.Value)
		if !ok {
			return nil, false, invalidQueryPredicate("invalid lifecycle state " + predicate.Value)
		}
		return func(_ *queryContext, ticket *Ticket) (bool, error) { return ticket.State == state, nil }, false, nil
	case "tag":
		tag, err := normalizeQueryTag(predicate.Value)
		if err != nil {
			return nil, false, err
		}
		return func(_ *queryContext, ticket *Ticket) (bool, error) {
			for _, candidate := range ticket.Tags {
				if candidate == tag {
					return true, nil
				}
			}
			return false, nil
		}, false, nil
	case "assignee":
		if predicate.Value == "" {
			return nil, false, invalidQueryPredicate("assignee value must not be empty")
		}
		return func(_ *queryContext, ticket *Ticket) (bool, error) { return ticket.Assignee == predicate.Value, nil }, false, nil
	case "priority":
		want, err := parseQueryPriority(predicate.Value)
		if err != nil {
			return nil, false, err
		}
		return func(_ *queryContext, ticket *Ticket) (bool, error) { return ticket.Priority == want, nil }, false, nil
	case "parent":
		if predicate.Value == "" {
			return nil, false, invalidQueryPredicate("parent value must not be empty")
		}
		full, err := st.ResolveID(predicate.Value, true)
		if err != nil {
			return nil, false, err
		}
		return func(_ *queryContext, ticket *Ticket) (bool, error) { return ticket.Parent == full, nil }, false, nil
	default:
		return nil, false, invalidQueryPredicate("unknown query field " + predicate.Field)
	}
}

func invalidQueryPredicate(message string) error {
	return contract.NewError(contract.ErrInvalidArgument, "Invalid query predicate: "+message+".", nil)
}

func normalizeQueryTag(value string) (string, error) {
	tags, err := normalizeTags([]string{value})
	if err != nil {
		return "", contract.NewError(contract.ErrInvalidArgument, "Invalid query tag: "+err.Error(), nil)
	}
	return tags[0], nil
}

func parseQueryPriority(value string) (int, error) {
	value = strings.TrimSpace(strings.ToUpper(value))
	if strings.HasPrefix(value, "P") {
		value = strings.TrimPrefix(value, "P")
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 || n > 4 {
		return 0, invalidQueryPredicate("priority must be P0 through P4")
	}
	return n, nil
}

func validComparison(operator Comparison) bool {
	switch operator {
	case ComparisonEQ, ComparisonNE, ComparisonLT, ComparisonLE, ComparisonGT, ComparisonGE:
		return true
	default:
		return false
	}
}

func compareInt(left, right int, operator Comparison) bool {
	switch operator {
	case ComparisonEQ:
		return left == right
	case ComparisonNE:
		return left != right
	case ComparisonLT:
		return left < right
	case ComparisonLE:
		return left <= right
	case ComparisonGT:
		return left > right
	case ComparisonGE:
		return left >= right
	default:
		return false
	}
}

func validateSort(sortSpec SortSpec) error {
	switch sortSpec {
	case "", SortPriorityID, SortIDDesc, SortModifiedDesc:
		return nil
	default:
		return contract.NewError(contract.ErrInvalidArgument, "Invalid query sort.", map[string]any{"sort": sortSpec})
	}
}

func sortQueryTickets(st *store.Store, tickets []*Ticket, sortSpec SortSpec) error {
	switch sortSpec {
	case "", SortPriorityID:
		sortTickets(tickets)
	case SortIDDesc:
		sort.SliceStable(tickets, func(i, j int) bool { return tickets[i].ID > tickets[j].ID })
	case SortModifiedDesc:
		modified := make(map[string]int64, len(tickets))
		for _, ticket := range tickets {
			info, err := st.TaskModTime(ticket.ID)
			if err != nil {
				return wrapStoreError("ticket file", err)
			}
			modified[ticket.ID] = info.UnixNano()
		}
		sort.SliceStable(tickets, func(i, j int) bool {
			if modified[tickets[i].ID] != modified[tickets[j].ID] {
				return modified[tickets[i].ID] > modified[tickets[j].ID]
			}
			return tickets[i].ID > tickets[j].ID
		})
	}
	return nil
}

// String is useful in diagnostics and keeps scope values stable for callers.
func (scope QueryScope) String() string {
	switch scope {
	case ScopeActive:
		return "active"
	case ScopeArchived:
		return "archived"
	case ScopeAll:
		return "all"
	default:
		return fmt.Sprintf("scope(%d)", scope)
	}
}
