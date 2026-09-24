package domain

import (
	"errors"
	"sort"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/store"
)

// GraphOptions selects the authoritative dependency graph projection.
type GraphOptions struct {
	RootID          string
	Reverse         bool
	Depth           int
	DepthSet        bool
	IncludeArchived bool
	ArchivedOnly    bool
}

// GraphNode is a ticket or a missing dependency reference. Missing nodes are
// presentation markers for the same dangling relationship readiness reports.
type GraphNode struct {
	ID       string `json:"id"`
	Title    string `json:"title,omitempty"`
	State    string `json:"state,omitempty"`
	Priority int    `json:"priority,omitempty"`
	Assignee string `json:"assignee,omitempty"`
	Archived bool   `json:"archived"`
	Missing  bool   `json:"missing,omitempty"`
}

// GraphEdge is a traversal-oriented dependency relationship. In normal mode
// it points from a ticket to its dependency; in reverse mode it points from a
// dependency to a ticket that depends on it.
type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

// GraphResult is the structured graph response. RenderMode is an internal CLI
// hint and is intentionally omitted from JSON.
type GraphResult struct {
	Root        string      `json:"root,omitempty"`
	Direction   string      `json:"direction"`
	Nodes       []GraphNode `json:"nodes"`
	Edges       []GraphEdge `json:"edges"`
	Truncated   bool        `json:"truncated,omitempty"`
	RenderMode  string      `json:"-"`
	TruncatedAt []string    `json:"-"`
}

// Graph builds a bounded, deterministic dependency/dependent projection from
// the same parsed tickets and archive-aware graph loaders used by readiness.
func Graph(st *store.Store, opts GraphOptions) (*GraphResult, error) {
	if opts.DepthSet && opts.Depth < 0 {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Depth must not be negative.", nil)
	}
	if opts.IncludeArchived && opts.ArchivedOnly {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Include archived and archived-only graph modes conflict.", nil)
	}
	var tickets []*Ticket
	var err error
	switch {
	case opts.ArchivedOnly:
		var diags []DiagnosticSummary
		tickets, diags, err = scanArchivedWith(st)
		if err == nil && len(diags) > 0 {
			err = collectionError(diags)
		}
	case opts.IncludeArchived:
		tickets, err = loadAllGraph(st)
	default:
		tickets, err = loadDependencyGraph(st)
	}
	if err != nil {
		return nil, err
	}

	byID := make(map[string]*Ticket, len(tickets))
	for _, ticket := range tickets {
		byID[ticket.ID] = ticket
	}
	if opts.RootID != "" {
		if _, ok := byID[opts.RootID]; !ok {
			root, readErr := ReadTicket(st, opts.RootID)
			if readErr != nil {
				return nil, readErr
			}
			tickets = append(tickets, root)
			byID[root.ID] = root
			if opts.ArchivedOnly && !root.Archived {
				return nil, contract.NewError(contract.ErrInvalidArgument, "Archived-only graph requires an archived root.", nil)
			}
			if !opts.IncludeArchived && !opts.ArchivedOnly {
				var expandErr error
				tickets, expandErr = expandGraphReferences(st, tickets, byID)
				if expandErr != nil {
					return nil, expandErr
				}
			}
		}
	}

	result := &GraphResult{Root: opts.RootID, Direction: "dependencies", Nodes: []GraphNode{}, Edges: []GraphEdge{}}
	if opts.Reverse {
		result.Direction = "dependents"
	}
	if opts.RootID == "" {
		return buildUnboundedGraph(result, tickets, byID, opts)
	}
	return buildRootedGraph(result, byID, opts)
}

// GraphForTicketSet builds an induced dependency projection over exactly the
// tickets in set. It deliberately does not load repository graph references:
// selected-set callers use query scope and selection as the archive boundary,
// and dependencies outside the set must remain absent from both nodes and
// edges. A selected-set graph has no traversal root, so depth is invalid.
func GraphForTicketSet(set *TicketSet, opts GraphOptions) (*GraphResult, error) {
	if opts.DepthSet {
		return nil, contract.NewError(contract.ErrInvalidArgument,
			"Selected-set graph does not support --depth.", nil)
	}
	byID := make(map[string]*Ticket)
	if set != nil {
		for _, ticket := range set.Tickets {
			if ticket != nil {
				byID[ticket.ID] = ticket
			}
		}
	}
	result := &GraphResult{Direction: "dependencies", Nodes: []GraphNode{}, Edges: []GraphEdge{}}
	if opts.Reverse {
		result.Direction = "dependents"
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		result.Nodes = append(result.Nodes, graphNode(byID[id]))
	}
	for _, id := range ids {
		for _, neighbor := range graphNeighbors(byID[id], byID, opts.Reverse) {
			if _, ok := byID[neighbor]; !ok {
				continue
			}
			result.Edges = append(result.Edges, GraphEdge{From: id, To: neighbor, Type: "depends_on"})
		}
	}
	sort.SliceStable(result.Edges, func(i, j int) bool {
		if result.Edges[i].From != result.Edges[j].From {
			return result.Edges[i].From < result.Edges[j].From
		}
		return result.Edges[i].To < result.Edges[j].To
	})
	return result, nil
}

// loadDependencyGraph keeps the no-flag graph focused on dependency
// relationships. The broader loadGraph projection also follows parent
// references for readiness validation, which would make an archived parent
// appear in an otherwise unrelated dependency view.
func loadDependencyGraph(st *store.Store) ([]*Ticket, error) {
	tickets, diags, err := scanWith(st)
	if err != nil {
		return nil, err
	}
	if len(diags) > 0 {
		return nil, collectionError(diags)
	}
	byID := make(map[string]*Ticket, len(tickets))
	for _, ticket := range tickets {
		byID[ticket.ID] = ticket
	}
	return expandGraphReferences(st, tickets, byID)
}

func expandGraphReferences(st *store.Store, tickets []*Ticket, byID map[string]*Ticket) ([]*Ticket, error) {
	for index := 0; index < len(tickets); index++ {
		for _, ref := range tickets[index].DependsOn {
			if _, ok := byID[ref]; ok {
				continue
			}
			ticket, err := ReadTicket(st, ref)
			if err != nil {
				var ce *contract.Error
				if isNotFoundError(err, &ce) {
					continue
				}
				return nil, err
			}
			byID[ticket.ID] = ticket
			tickets = append(tickets, ticket)
		}
	}
	return tickets, nil
}

func isNotFoundError(err error, target **contract.Error) bool {
	return errors.As(err, target) && (*target).Code == contract.ErrNotFound
}

func buildUnboundedGraph(result *GraphResult, tickets []*Ticket, byID map[string]*Ticket, opts GraphOptions) (*GraphResult, error) {
	ids := make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		ids = append(ids, ticket.ID)
	}
	sort.Strings(ids)
	for _, id := range ids {
		result.Nodes = append(result.Nodes, graphNode(byID[id]))
	}
	for _, id := range ids {
		for _, neighbor := range graphNeighbors(byID[id], byID, opts.Reverse) {
			result.Edges = append(result.Edges, GraphEdge{From: id, To: neighbor, Type: "depends_on"})
		}
	}
	return addMissingAndSortEdges(result), nil
}

func buildRootedGraph(result *GraphResult, byID map[string]*Ticket, opts GraphOptions) (*GraphResult, error) {
	if _, ok := byID[opts.RootID]; !ok {
		return nil, contract.NewError(contract.ErrNotFound, "Ticket not found.", map[string]any{"id": opts.RootID})
	}
	depthLimit := -1
	if opts.DepthSet {
		depthLimit = opts.Depth
	}
	seen := map[string]bool{}
	var visit func(string, int)
	visit = func(id string, depth int) {
		if seen[id] {
			return
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, graphNode(byID[id]))
		neighbors := graphNeighbors(byID[id], byID, opts.Reverse)
		if depthLimit >= 0 && depth >= depthLimit {
			if len(neighbors) > 0 {
				result.Truncated = true
				result.TruncatedAt = append(result.TruncatedAt, id)
			}
			return
		}
		for _, neighbor := range neighbors {
			result.Edges = append(result.Edges, GraphEdge{From: id, To: neighbor, Type: "depends_on"})
			if _, ok := byID[neighbor]; ok {
				visit(neighbor, depth+1)
			}
		}
	}
	visit(opts.RootID, 0)
	return addMissingAndSortEdges(result), nil
}

func graphNeighbors(ticket *Ticket, byID map[string]*Ticket, reverse bool) []string {
	if !reverse {
		return append([]string(nil), ticket.DependsOn...)
	}
	neighbors := make([]string, 0)
	for _, candidate := range byID {
		for _, dependency := range candidate.DependsOn {
			if dependency == ticket.ID {
				neighbors = append(neighbors, candidate.ID)
				break
			}
		}
	}
	sort.Strings(neighbors)
	return neighbors
}

func graphNode(ticket *Ticket) GraphNode {
	if ticket == nil {
		return GraphNode{}
	}
	return GraphNode{ID: ticket.ID, Title: ticket.Title, State: ticket.State,
		Priority: ticket.Priority, Assignee: ticket.Assignee, Archived: ticket.Archived}
}

func addMissingAndSortEdges(result *GraphResult) *GraphResult {
	known := make(map[string]bool, len(result.Nodes))
	for _, node := range result.Nodes {
		known[node.ID] = true
	}
	for _, edge := range result.Edges {
		if !known[edge.To] {
			result.Nodes = append(result.Nodes, GraphNode{ID: edge.To, Missing: true})
			known[edge.To] = true
		}
	}
	// A no-ID graph includes every selected ticket, while a rooted graph only
	// includes reachable nodes and its omitted neighbors are represented by the
	// truncated marker.
	if result.Root == "" {
		sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].ID < result.Nodes[j].ID })
	}
	sort.SliceStable(result.Edges, func(i, j int) bool {
		if result.Edges[i].From != result.Edges[j].From {
			return result.Edges[i].From < result.Edges[j].From
		}
		return result.Edges[i].To < result.Edges[j].To
	})
	return result
}
