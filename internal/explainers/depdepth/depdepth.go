package depdepth

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/dejo1307/archmcp/internal/facts"
)

// DepDepthExplainer measures dependency chain depth using topological sort + DP.
// Cycles are detected via Tarjan's SCC and collapsed before depth calculation.
type DepDepthExplainer struct{}

// New creates a new DepDepthExplainer.
func New() *DepDepthExplainer {
	return &DepDepthExplainer{}
}

// Name returns the explainer identifier.
func (e *DepDepthExplainer) Name() string {
	return "depdepth"
}

// Explain builds a dependency graph from import relations, computes the longest
// dependency chain depth for every module, and reports insights about:
//   - The deepest dependency chain found
//   - Average dependency depth
//   - Modules whose depth exceeds 5
func (e *DepDepthExplainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	graph := buildDependencyGraph(store)
	if len(graph) == 0 {
		return nil, nil
	}

	// 1. Detect SCCs and collapse cycles into single super-nodes.
	sccs := tarjanSCC(graph)
	sccID := mapNodesToSCC(sccs)
	dag := collapseSCCsToDAG(graph, sccID)

	// 2. Compute longest-path depth via topo sort + DP on the DAG.
	depths := longestPathDepths(dag)

	// 3. Map depths back to original module names.
	moduleDepths := make(map[string]int, len(graph))
	for mod := range graph {
		id := sccID[mod]
		moduleDepths[mod] = depths[id]
	}

	return buildInsights(moduleDepths), nil
}

// buildDependencyGraph extracts module-level import relationships from the store.
func buildDependencyGraph(store *facts.Store) map[string][]string {
	graph := make(map[string][]string)

	modules := store.ByKind(facts.KindModule)
	moduleNames := make(map[string]bool, len(modules))
	for _, m := range modules {
		moduleNames[m.Name] = true
		if _, ok := graph[m.Name]; !ok {
			graph[m.Name] = nil
		}
	}

	deps := store.ByKind(facts.KindDependency)
	for _, dep := range deps {
		sourceModule := fileDir(dep.File)
		for _, rel := range dep.Relations {
			if rel.Kind != facts.RelImports {
				continue
			}
			target := rel.Target
			if isExternalImport(target) {
				continue
			}
			if strings.HasPrefix(target, ".") {
				target = resolveRelativeImport(sourceModule, target)
			}
			if moduleNames[target] {
				graph[sourceModule] = append(graph[sourceModule], target)
			}
		}
	}

	return graph
}

// --- SCC detection (Tarjan's algorithm) ---

func tarjanSCC(graph map[string][]string) [][]string {
	var (
		index    int
		stack    []string
		onStack  = make(map[string]bool)
		indices  = make(map[string]int)
		lowlinks = make(map[string]int)
		sccs     [][]string
	)

	var strongConnect func(v string)
	strongConnect = func(v string) {
		indices[v] = index
		lowlinks[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true

		for _, w := range graph[v] {
			if _, visited := indices[w]; !visited {
				strongConnect(w)
				if lowlinks[w] < lowlinks[v] {
					lowlinks[v] = lowlinks[w]
				}
			} else if onStack[w] {
				if indices[w] < lowlinks[v] {
					lowlinks[v] = indices[w]
				}
			}
		}

		if lowlinks[v] == indices[v] {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sccs = append(sccs, scc)
		}
	}

	for v := range graph {
		if _, visited := indices[v]; !visited {
			strongConnect(v)
		}
	}

	return sccs
}

// mapNodesToSCC assigns each node to its SCC representative (first element).
func mapNodesToSCC(sccs [][]string) map[string]string {
	m := make(map[string]string)
	for _, scc := range sccs {
		rep := scc[0]
		for _, node := range scc {
			m[node] = rep
		}
	}
	return m
}

// collapseSCCsToDAG builds a DAG where each SCC is collapsed into a single node.
func collapseSCCsToDAG(graph map[string][]string, sccID map[string]string) map[string][]string {
	dag := make(map[string][]string)

	// Ensure all SCC reps are in the DAG.
	seen := make(map[string]bool)
	for _, rep := range sccID {
		if !seen[rep] {
			seen[rep] = true
			dag[rep] = nil
		}
	}

	// Add edges between distinct SCC reps.
	edgeSeen := make(map[[2]string]bool)
	for src, targets := range graph {
		srcRep := sccID[src]
		for _, tgt := range targets {
			tgtRep := sccID[tgt]
			if srcRep == tgtRep {
				continue // internal to the same SCC
			}
			edge := [2]string{srcRep, tgtRep}
			if !edgeSeen[edge] {
				edgeSeen[edge] = true
				dag[srcRep] = append(dag[srcRep], tgtRep)
			}
		}
	}

	return dag
}

// longestPathDepths computes the longest path from each node in a DAG.
// Uses Kahn's topo sort + DP.
func longestPathDepths(dag map[string][]string) map[string]int {
	// Compute in-degrees.
	inDeg := make(map[string]int, len(dag))
	for node := range dag {
		if _, ok := inDeg[node]; !ok {
			inDeg[node] = 0
		}
		for _, tgt := range dag[node] {
			inDeg[tgt]++
		}
	}

	// Seed the queue with nodes that have no incoming edges.
	var queue []string
	for node, deg := range inDeg {
		if deg == 0 {
			queue = append(queue, node)
		}
	}

	depth := make(map[string]int, len(dag))
	// Process in topological order.
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]

		for _, tgt := range dag[node] {
			if depth[node]+1 > depth[tgt] {
				depth[tgt] = depth[node] + 1
			}
			inDeg[tgt]--
			if inDeg[tgt] == 0 {
				queue = append(queue, tgt)
			}
		}
	}

	return depth
}

// buildInsights converts module depths into architectural insights.
func buildInsights(moduleDepths map[string]int) []facts.Insight {
	if len(moduleDepths) == 0 {
		return nil
	}

	// Find deepest chain.
	var maxDepth int
	var deepestModule string
	var totalDepth int
	var deepModules []string // depth > 5

	for mod, d := range moduleDepths {
		totalDepth += d
		if d > maxDepth {
			maxDepth = d
			deepestModule = mod
		}
		if d > 5 {
			deepModules = append(deepModules, mod)
		}
	}

	avgDepth := float64(totalDepth) / float64(len(moduleDepths))
	avgDepth = math.Round(avgDepth*100) / 100

	var insights []facts.Insight

	// Always report the deepest chain summary.
	insights = append(insights, facts.Insight{
		Title: "Deepest dependency chain",
		Description: fmt.Sprintf(
			"The longest dependency chain has depth %d (deepest module: %q). Average depth across %d modules: %.2f.",
			maxDepth, deepestModule, len(moduleDepths), avgDepth,
		),
		Confidence: 1.0,
		Evidence: []facts.Evidence{
			{Fact: deepestModule, Detail: fmt.Sprintf("depth %d", maxDepth)},
		},
		Actions: []string{
			"Consider breaking long dependency chains with interfaces",
			"Review deeply-nested modules for unnecessary coupling",
		},
	})

	// Report modules with depth > 5.
	if len(deepModules) > 0 {
		evidence := make([]facts.Evidence, 0, len(deepModules))
		for _, mod := range deepModules {
			evidence = append(evidence, facts.Evidence{
				Fact:   mod,
				Detail: fmt.Sprintf("depth %d", moduleDepths[mod]),
			})
		}
		insights = append(insights, facts.Insight{
			Title: fmt.Sprintf("Modules with depth exceeding 5 (%d found)", len(deepModules)),
			Description: fmt.Sprintf(
				"%d module(s) have a dependency depth greater than 5, indicating deep coupling chains that may hinder maintainability.",
				len(deepModules),
			),
			Confidence: 0.9,
			Evidence:   evidence,
			Actions: []string{
				"Flatten dependency hierarchy where possible",
				"Introduce facade modules to reduce transitive depth",
			},
		})
	}

	return insights
}

// --- utility functions (matching cycles.go patterns) ---

func fileDir(file string) string {
	parts := strings.Split(file, "/")
	if len(parts) <= 1 {
		return "."
	}
	return strings.Join(parts[:len(parts)-1], "/")
}

func isExternalImport(path string) bool {
	if strings.HasPrefix(path, ".") || strings.HasPrefix(path, "/") {
		return false
	}
	if strings.Contains(path, ".") || !strings.Contains(path, "/") {
		return true
	}
	return false
}

func resolveRelativeImport(sourceModule, target string) string {
	if !strings.HasPrefix(target, ".") {
		return target
	}
	parts := strings.Split(sourceModule, "/")
	targetParts := strings.Split(target, "/")
	for _, tp := range targetParts {
		switch tp {
		case ".":
			continue
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, tp)
		}
	}
	return strings.Join(parts, "/")
}
