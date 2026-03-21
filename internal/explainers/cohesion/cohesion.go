package cohesion

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dejo1307/archmcp/internal/facts"
)

// CohesionExplainer measures intra-module cohesion using call-graph connectivity.
// It computes cohesion = connected_pairs / total_pairs (LCOM variant) and detects
// disconnected components via union-find.
type CohesionExplainer struct{}

// New creates a new CohesionExplainer.
func New() *CohesionExplainer {
	return &CohesionExplainer{}
}

func (e *CohesionExplainer) Name() string {
	return "cohesion"
}

// minSymbols is the minimum number of functions/methods in a module to analyze.
const minSymbols = 2

// cohesionThreshold flags modules with cohesion below this value.
const cohesionThreshold = 0.1

// Explain analyzes intra-module call graphs and reports low-cohesion modules.
func (e *CohesionExplainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	modSyms, modAdj := buildModuleCallGraphs(store)

	type moduleResult struct {
		name       string
		cohesion   float64
		components int
		symbols    int
	}

	var results []moduleResult
	for mod, syms := range modSyms {
		if len(syms) < minSymbols {
			continue
		}
		adj := modAdj[mod]
		score := computeCohesion(syms, adj)
		comps := detectComponents(syms, adj)

		if score < cohesionThreshold || len(comps) > 1 {
			results = append(results, moduleResult{
				name:       mod,
				cohesion:   score,
				components: len(comps),
				symbols:    len(syms),
			})
		}
	}

	// Rank by lowest cohesion first.
	sort.Slice(results, func(i, j int) bool {
		return results[i].cohesion < results[j].cohesion
	})

	var insights []facts.Insight
	for _, r := range results {
		evidence := make([]facts.Evidence, 0)
		evidence = append(evidence, facts.Evidence{
			Fact:   r.name,
			Detail: fmt.Sprintf("cohesion=%.2f, %d symbols, %d connected component(s)", r.cohesion, r.symbols, r.components),
		})

		// Add component details if multiple.
		if r.components > 1 {
			comps := detectComponents(modSyms[r.name], modAdj[r.name])
			for i, comp := range comps {
				sort.Strings(comp)
				evidence = append(evidence, facts.Evidence{
					Fact:   r.name,
					Detail: fmt.Sprintf("component %d: %s", i+1, strings.Join(comp, ", ")),
				})
			}
		}

		desc := fmt.Sprintf("Module %q has low cohesion (%.2f). ", r.name, r.cohesion)
		if r.components > 1 {
			desc += fmt.Sprintf("It contains %d disconnected components, suggesting it handles unrelated responsibilities.", r.components)
		} else {
			desc += "Most symbols are not connected through calls, suggesting the module lacks a focused purpose."
		}

		insights = append(insights, facts.Insight{
			Title:       fmt.Sprintf("Low cohesion in %s (%.2f)", r.name, r.cohesion),
			Description: desc,
			Confidence:  computeConfidence(r.cohesion, r.components, r.symbols),
			Evidence:    evidence,
			Actions: []string{
				"Split the module into smaller, focused packages",
				"Group related functions together",
				"Consider if unrelated utilities belong in a shared package",
			},
		})
	}

	return insights, nil
}

// computeConfidence returns a confidence score based on how clear the signal is.
func computeConfidence(cohesion float64, components, symbols int) float64 {
	conf := 0.7
	if cohesion == 0 {
		conf = 0.9
	}
	if components > 2 {
		conf += 0.1
	}
	if symbols >= 5 {
		conf += 0.1
	}
	if conf > 1.0 {
		conf = 1.0
	}
	return conf
}

// buildModuleCallGraphs groups symbols by module and builds intra-module call adjacency.
func buildModuleCallGraphs(store *facts.Store) (
	modSymbols map[string][]string,
	modAdj map[string]map[string]map[string]bool,
) {
	modSymbols = make(map[string][]string)
	modAdj = make(map[string]map[string]map[string]bool)

	// Collect all function/method symbols by module.
	allSymbols := store.ByKind(facts.KindSymbol)
	symbolModule := make(map[string]string) // symbol name -> module name

	for _, sym := range allSymbols {
		kind, _ := sym.Props["symbol_kind"].(string)
		if kind != facts.SymbolFunc && kind != facts.SymbolMethod {
			continue
		}
		mod := fileDir(sym.File)
		modSymbols[mod] = append(modSymbols[mod], sym.Name)
		symbolModule[sym.Name] = mod
	}

	// Build intra-module adjacency from "calls" relations.
	for _, sym := range allSymbols {
		kind, _ := sym.Props["symbol_kind"].(string)
		if kind != facts.SymbolFunc && kind != facts.SymbolMethod {
			continue
		}
		srcMod := fileDir(sym.File)

		for _, rel := range sym.Relations {
			if rel.Kind != facts.RelCalls {
				continue
			}
			tgtMod, ok := symbolModule[rel.Target]
			if !ok || tgtMod != srcMod {
				continue // skip cross-module calls
			}

			if modAdj[srcMod] == nil {
				modAdj[srcMod] = make(map[string]map[string]bool)
			}
			if modAdj[srcMod][sym.Name] == nil {
				modAdj[srcMod][sym.Name] = make(map[string]bool)
			}
			modAdj[srcMod][sym.Name][rel.Target] = true
		}
	}

	return modSymbols, modAdj
}

// computeCohesion calculates connected_pairs / total_pairs using union-find.
// Returns 1.0 for modules with 0 or 1 symbols (trivially cohesive).
func computeCohesion(symbols []string, adj map[string]map[string]bool) float64 {
	n := len(symbols)
	if n <= 1 {
		return 1.0
	}

	totalPairs := n * (n - 1) / 2

	// Map symbol names to indices.
	idx := make(map[string]int, n)
	for i, s := range symbols {
		idx[s] = i
	}

	uf := newUnionFind(n)

	// Union connected symbols (treat edges as undirected).
	for src, targets := range adj {
		srcIdx, ok := idx[src]
		if !ok {
			continue
		}
		for tgt := range targets {
			tgtIdx, ok := idx[tgt]
			if !ok {
				continue
			}
			uf.union(srcIdx, tgtIdx)
		}
	}

	// Count connected pairs: for each component of size k, pairs = k*(k-1)/2.
	componentSizes := make(map[int]int)
	for i := 0; i < n; i++ {
		root := uf.find(i)
		componentSizes[root]++
	}

	connectedPairs := 0
	for _, size := range componentSizes {
		connectedPairs += size * (size - 1) / 2
	}

	return float64(connectedPairs) / float64(totalPairs)
}

// detectComponents returns the disconnected components as groups of symbol names.
func detectComponents(symbols []string, adj map[string]map[string]bool) [][]string {
	n := len(symbols)
	if n == 0 {
		return nil
	}

	idx := make(map[string]int, n)
	for i, s := range symbols {
		idx[s] = i
	}

	uf := newUnionFind(n)
	for src, targets := range adj {
		srcIdx, ok := idx[src]
		if !ok {
			continue
		}
		for tgt := range targets {
			tgtIdx, ok := idx[tgt]
			if !ok {
				continue
			}
			uf.union(srcIdx, tgtIdx)
		}
	}

	groups := make(map[int][]string)
	for i, sym := range symbols {
		root := uf.find(i)
		groups[root] = append(groups[root], sym)
	}

	result := make([][]string, 0, len(groups))
	for _, g := range groups {
		result = append(result, g)
	}

	// Sort by size descending for stable output.
	sort.Slice(result, func(i, j int) bool {
		return len(result[i]) > len(result[j])
	})

	return result
}

// --- union-find ---

type unionFind struct {
	parent []int
	rank   []int
}

func newUnionFind(n int) *unionFind {
	parent := make([]int, n)
	rank := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	return &unionFind{parent: parent, rank: rank}
}

func (uf *unionFind) find(x int) int {
	for uf.parent[x] != x {
		uf.parent[x] = uf.parent[uf.parent[x]] // path compression
		x = uf.parent[x]
	}
	return x
}

func (uf *unionFind) union(x, y int) {
	rx, ry := uf.find(x), uf.find(y)
	if rx == ry {
		return
	}
	if uf.rank[rx] < uf.rank[ry] {
		rx, ry = ry, rx
	}
	uf.parent[ry] = rx
	if uf.rank[rx] == uf.rank[ry] {
		uf.rank[rx]++
	}
}

func (uf *unionFind) componentCount() int {
	roots := make(map[int]bool)
	for i := range uf.parent {
		roots[uf.find(i)] = true
	}
	return len(roots)
}

// fileDir extracts the directory from a file path.
func fileDir(file string) string {
	parts := strings.Split(file, "/")
	if len(parts) <= 1 {
		return "."
	}
	return strings.Join(parts[:len(parts)-1], "/")
}
