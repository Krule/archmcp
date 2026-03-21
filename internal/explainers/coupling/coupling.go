package coupling

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dejo1307/archmcp/internal/facts"
)

// Thresholds for coupling analysis.
const (
	HighCouplingThreshold = 10 // edges between a pair to flag as highly coupled
	HubFanOutThreshold    = 5  // unique outgoing module targets to flag as hub
)

// CouplingExplainer analyzes module-to-module coupling by building an edge
// count matrix from calls and imports relations.
type CouplingExplainer struct{}

// New creates a new CouplingExplainer.
func New() *CouplingExplainer {
	return &CouplingExplainer{}
}

func (e *CouplingExplainer) Name() string {
	return "coupling"
}

// Explain builds a module x module edge count matrix from imports/calls,
// then flags highly coupled pairs, hub modules, and reports the coupling ratio.
func (e *CouplingExplainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	modules := store.ByKind(facts.KindModule)
	if len(modules) < 2 {
		return nil, nil
	}

	moduleNames := make(map[string]bool, len(modules))
	for _, m := range modules {
		moduleNames[m.Name] = true
	}

	matrix := buildEdgeMatrix(store)
	fanOut := computeFanOut(store)

	var insights []facts.Insight

	// 1. Flag highly coupled pairs (>threshold edges)
	pairs := highlyCoupledPairs(matrix, HighCouplingThreshold)
	for _, p := range pairs {
		mods := strings.SplitN(p.key, " <-> ", 2)
		modA, modB := mods[0], mods[1]
		insights = append(insights, facts.Insight{
			Title: fmt.Sprintf("Highly coupled: %s <-> %s (%d edges)", modA, modB, p.count),
			Description: fmt.Sprintf(
				"Modules %q and %q share %d edges (imports + calls). "+
					"This exceeds the threshold of %d and indicates tight coupling. "+
					"Consider introducing an interface or extracting shared abstractions.",
				modA, modB, p.count, HighCouplingThreshold,
			),
			Confidence: 0.9,
			Evidence: []facts.Evidence{
				{Fact: modA, Detail: fmt.Sprintf("%d edges to/from %s", p.count, modB)},
				{Fact: modB, Detail: fmt.Sprintf("%d edges to/from %s", p.count, modA)},
			},
			Actions: []string{
				"Introduce an interface to decouple the modules",
				"Extract shared types to a separate package",
				"Review if modules should be merged",
			},
		})
	}

	// 2. Flag hub modules (high fan-out)
	type hubEntry struct {
		name   string
		fanOut int
	}
	var hubs []hubEntry
	for mod, fo := range fanOut {
		if fo >= HubFanOutThreshold {
			hubs = append(hubs, hubEntry{name: mod, fanOut: fo})
		}
	}
	sort.Slice(hubs, func(i, j int) bool { return hubs[i].fanOut > hubs[j].fanOut })
	for _, h := range hubs {
		insights = append(insights, facts.Insight{
			Title: fmt.Sprintf("Hub module: %s (fan-out %d)", h.name, h.fanOut),
			Description: fmt.Sprintf(
				"Module %q depends on %d other modules, making it a coupling hub. "+
					"Changes to its dependencies are likely to affect it. "+
					"Consider splitting into smaller, focused modules.",
				h.name, h.fanOut,
			),
			Confidence: 0.85,
			Evidence: []facts.Evidence{
				{Fact: h.name, Detail: fmt.Sprintf("fan-out to %d unique modules", h.fanOut)},
			},
			Actions: []string{
				"Split the hub module into focused sub-modules",
				"Apply dependency inversion to reduce direct dependencies",
			},
		})
	}

	// 3. Coupling ratio: actual edges / max possible pairs
	totalEdgePairs := len(matrix) // number of unique module pairs with edges
	ratio := couplingRatio(totalEdgePairs, len(modules))
	insights = append(insights, facts.Insight{
		Title: fmt.Sprintf("Coupling ratio: %.0f%% (%d/%d possible pairs)",
			ratio*100, totalEdgePairs, maxPairs(len(modules))),
		Description: fmt.Sprintf(
			"%.0f%% of possible module pairs have at least one coupling edge. "+
				"Lower is generally better for maintainability.",
			ratio*100,
		),
		Confidence: 1.0, // Deterministic metric
		Evidence:   nil,
		Actions:    nil,
	})

	return insights, nil
}

// coupledPair holds a module pair key and its edge count.
type coupledPair struct {
	key   string
	count int
}

// buildEdgeMatrix counts edges (imports + calls) between module pairs.
// Keys are canonical "modA <-> modB" strings (alphabetically ordered).
func buildEdgeMatrix(store *facts.Store) map[string]int {
	modules := store.ByKind(facts.KindModule)
	moduleNames := make(map[string]bool, len(modules))
	for _, m := range modules {
		moduleNames[m.Name] = true
	}

	matrix := make(map[string]int)

	// Count import edges
	deps := store.ByKind(facts.KindDependency)
	for _, dep := range deps {
		src := fileDir(dep.File)
		if !moduleNames[src] {
			continue
		}
		for _, rel := range dep.Relations {
			if rel.Kind != facts.RelImports {
				continue
			}
			tgt := rel.Target
			if !moduleNames[tgt] || tgt == src {
				continue
			}
			matrix[edgeKey(src, tgt)]++
		}
	}

	// Count call edges
	symbols := store.ByKind(facts.KindSymbol)
	for _, sym := range symbols {
		src := fileDir(sym.File)
		if !moduleNames[src] {
			continue
		}
		for _, rel := range sym.Relations {
			if rel.Kind != facts.RelCalls {
				continue
			}
			// Resolve target module from the target symbol's file
			tgt := resolveTargetModule(rel.Target, store, moduleNames)
			if tgt == "" || tgt == src {
				continue
			}
			matrix[edgeKey(src, tgt)]++
		}
	}

	return matrix
}

// resolveTargetModule finds which module a called symbol belongs to.
func resolveTargetModule(targetSymbol string, store *facts.Store, moduleNames map[string]bool) string {
	// Try to find the target symbol in the store
	targetFacts := store.LookupByExactName(targetSymbol)
	for _, f := range targetFacts {
		if f.File != "" {
			mod := fileDir(f.File)
			if moduleNames[mod] {
				return mod
			}
		}
	}
	// Fallback: extract module from qualified symbol name (e.g. "src/b.Func" -> "src/b")
	if idx := strings.LastIndex(targetSymbol, "."); idx > 0 {
		candidate := targetSymbol[:idx]
		if moduleNames[candidate] {
			return candidate
		}
	}
	return ""
}

// computeFanOut counts unique outgoing module targets for each module.
func computeFanOut(store *facts.Store) map[string]int {
	modules := store.ByKind(facts.KindModule)
	moduleNames := make(map[string]bool, len(modules))
	for _, m := range modules {
		moduleNames[m.Name] = true
	}

	// Per-module set of unique targets
	targets := make(map[string]map[string]bool)

	deps := store.ByKind(facts.KindDependency)
	for _, dep := range deps {
		src := fileDir(dep.File)
		if !moduleNames[src] {
			continue
		}
		for _, rel := range dep.Relations {
			if rel.Kind != facts.RelImports {
				continue
			}
			tgt := rel.Target
			if !moduleNames[tgt] || tgt == src {
				continue
			}
			if targets[src] == nil {
				targets[src] = make(map[string]bool)
			}
			targets[src][tgt] = true
		}
	}

	symbols := store.ByKind(facts.KindSymbol)
	for _, sym := range symbols {
		src := fileDir(sym.File)
		if !moduleNames[src] {
			continue
		}
		for _, rel := range sym.Relations {
			if rel.Kind != facts.RelCalls {
				continue
			}
			tgt := resolveTargetModule(rel.Target, store, moduleNames)
			if tgt == "" || tgt == src {
				continue
			}
			if targets[src] == nil {
				targets[src] = make(map[string]bool)
			}
			targets[src][tgt] = true
		}
	}

	result := make(map[string]int, len(targets))
	for mod, tgts := range targets {
		result[mod] = len(tgts)
	}
	return result
}

// highlyCoupledPairs returns pairs exceeding the threshold, sorted descending.
func highlyCoupledPairs(matrix map[string]int, threshold int) []coupledPair {
	var pairs []coupledPair
	for key, count := range matrix {
		if count > threshold {
			pairs = append(pairs, coupledPair{key: key, count: count})
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].count > pairs[j].count })
	return pairs
}

// edgeKey returns a canonical key for a module pair (alphabetically ordered).
func edgeKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + " <-> " + b
}

// couplingRatio computes actual coupled pairs / max possible pairs.
func couplingRatio(edgePairs, moduleCount int) float64 {
	max := maxPairs(moduleCount)
	if max == 0 {
		return 0
	}
	ratio := float64(edgePairs) / float64(max)
	if ratio > 1.0 {
		ratio = 1.0
	}
	return ratio
}

// maxPairs returns n*(n-1)/2 — the max number of undirected pairs.
func maxPairs(n int) int {
	if n < 2 {
		return 0
	}
	return n * (n - 1) / 2
}

// fileDir extracts the directory from a file path.
func fileDir(file string) string {
	parts := strings.Split(file, "/")
	if len(parts) <= 1 {
		return "."
	}
	return strings.Join(parts[:len(parts)-1], "/")
}
