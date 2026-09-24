package cli

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/toolsupply/ticket/internal/domain"
)

func renderGraph(stdout *bytes.Buffer, graph *domain.GraphResult) {
	unicode := graph.RenderMode == "unicode" || (graph.RenderMode == "" && graphSupportsUnicode())
	byID := make(map[string]domain.GraphNode, len(graph.Nodes))
	for _, node := range graph.Nodes {
		byID[node.ID] = node
	}
	children := make(map[string][]string)
	incoming := make(map[string]bool)
	for _, edge := range graph.Edges {
		children[edge.From] = append(children[edge.From], edge.To)
		incoming[edge.To] = true
	}
	for id := range children {
		sort.Strings(children[id])
	}
	roots := graphRoots(graph, incoming)
	visited := make(map[string]bool, len(graph.Nodes))
	truncated := make(map[string]bool, len(graph.TruncatedAt))
	for _, id := range graph.TruncatedAt {
		truncated[id] = true
	}
	if graph.Root != "" {
		roots = []string{graph.Root}
	}
	for _, root := range roots {
		if visited[root] {
			continue
		}
		renderGraphNode(stdout, root, "", true, true, children, byID, visited, truncated, unicode)
	}
	remaining := make([]string, 0)
	for id := range byID {
		if !visited[id] {
			remaining = append(remaining, id)
		}
	}
	sort.Strings(remaining)
	for _, id := range remaining {
		renderGraphNode(stdout, id, "", true, true, children, byID, visited, truncated, unicode)
	}
}

func graphRoots(graph *domain.GraphResult, incoming map[string]bool) []string {
	roots := make([]string, 0)
	for _, node := range graph.Nodes {
		if !incoming[node.ID] {
			roots = append(roots, node.ID)
		}
	}
	sort.Strings(roots)
	return roots
}

func renderGraphNode(stdout *bytes.Buffer, id, prefix string, last, root bool, children map[string][]string, nodes map[string]domain.GraphNode, visited, truncated map[string]bool, unicode bool) {
	repeated := visited[id]
	if root {
		fmt.Fprintln(stdout, graphNodeText(nodes[id]))
	} else if repeated {
		marker := "(seen)"
		if unicode {
			marker = "↩"
		}
		connector := "`- "
		if unicode {
			connector = "└─ "
			if !last {
				connector = "├─ "
			}
		} else if !last {
			connector = "+- "
		}
		fmt.Fprintf(stdout, "%s%s%s  %s\n", prefix, connector, safeSingleLine(id), marker)
		return
	} else {
		connector := "`- "
		if unicode {
			connector = "└─ "
			if !last {
				connector = "├─ "
			}
		} else if !last {
			connector = "+- "
		}
		fmt.Fprintf(stdout, "%s%s%s\n", prefix, connector, graphNodeText(nodes[id]))
	}
	visited[id] = true
	items := children[id]
	for index, child := range items {
		childPrefix := prefix
		if !root {
			if unicode {
				if last {
					childPrefix += "   "
				} else {
					childPrefix += "│  "
				}
			} else if last {
				childPrefix += "   "
			} else {
				childPrefix += "|  "
			}
		}
		renderGraphNode(stdout, child, childPrefix, index == len(items)-1 && !truncated[id], false, children, nodes, visited, truncated, unicode)
	}
	if truncated[id] {
		marker := "... (depth limit)"
		if unicode {
			marker = "… (depth limit)"
		}
		childPrefix := prefix
		if !root {
			if unicode {
				if last {
					childPrefix += "   "
				} else {
					childPrefix += "│  "
				}
			} else if last {
				childPrefix += "   "
			} else {
				childPrefix += "|  "
			}
		}
		connector := "`- "
		if unicode {
			connector = "└─ "
		} else if len(items) > 0 {
			connector = "`- "
		}
		fmt.Fprintf(stdout, "%s%s%s\n", childPrefix, connector, marker)
	}
}

func graphNodeText(node domain.GraphNode) string {
	if node.Missing {
		return safeSingleLine(node.ID) + "  (missing)"
	}
	parts := []string{safeSingleLine(node.ID), fmt.Sprintf("P%d", node.Priority), safeSingleLine(node.State)}
	if node.Assignee != "" {
		parts = append(parts, "@"+safeSingleLine(node.Assignee))
	}
	if node.Archived {
		parts = append(parts, "archived")
	}
	if node.Title != "" {
		parts = append(parts, safeSingleLine(node.Title))
	}
	return strings.Join(parts, "  ")
}

func graphSupportsUnicode() bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(os.Getenv("WT_SESSION"), "") == false || strings.EqualFold(os.Getenv("ConEmuANSI"), "ON")
	}
	term := strings.ToLower(os.Getenv("TERM"))
	if term == "" || term == "dumb" {
		return false
	}
	info, err := os.Stdout.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	locale := strings.ToLower(os.Getenv("LC_ALL") + " " + os.Getenv("LC_CTYPE") + " " + os.Getenv("LANG"))
	return strings.Contains(locale, "utf-8") || strings.Contains(locale, "utf8") || strings.Contains(term, "xterm") || strings.Contains(term, "screen")
}
