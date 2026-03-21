package stability

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/dejo1307/archmcp/internal/facts"
)

// Zone classification constants.
const (
	ZoneMainSequence = "Main Sequence"
	ZonePain         = "Zone of Pain"
	ZoneUselessness  = "Zone of Uselessness"
)

// ModuleMetrics holds Robert Martin's package stability metrics.
type ModuleMetrics struct {
	Module       string  // Module path
	Ca           int     // Afferent coupling: number of modules that depend on this one
	Ce           int     // Efferent coupling: number of modules this one depends on
	Instability  float64 // I = Ce / (Ca + Ce); 0=stable, 1=unstable
	Abstractness float64 // A = abstract symbols / total symbols; 0=concrete, 1=abstract
	Distance     float64 // D = |A + I - 1|; distance from Main Sequence
}

// StabilityExplainer computes Robert Martin's stability metrics for modules.
type StabilityExplainer struct{}

// New creates a new StabilityExplainer.
func New() *StabilityExplainer {
	return &StabilityExplainer{}
}

func (e *StabilityExplainer) Name() string {
	return "stability"
}

// Explain computes stability metrics for all modules and produces insights.
func (e *StabilityExplainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	modules := store.ByKind(facts.KindModule)
	if len(modules) == 0 {
		return nil, nil
	}

	metrics := computeMetrics(store)
	if len(metrics) == 0 {
		return nil, nil
	}

	var insights []facts.Insight

	// Summary insight with per-module evidence
	evidence := make([]facts.Evidence, 0, len(metrics))
	// Sort module names for determinism
	names := make([]string, 0, len(metrics))
	for name := range metrics {
		names = append(names, name)
	}
	sort.Strings(names)

	var painModules, uselessModules []string

	for _, name := range names {
		m := metrics[name]
		zone := classifyZone(m)
		evidence = append(evidence, facts.Evidence{
			Fact: name,
			Detail: fmt.Sprintf("Ca=%d Ce=%d I=%.2f A=%.2f D=%.2f zone=%s",
				m.Ca, m.Ce, m.Instability, m.Abstractness, m.Distance, zone),
		})
		switch zone {
		case ZonePain:
			painModules = append(painModules, name)
		case ZoneUselessness:
			uselessModules = append(uselessModules, name)
		}
	}

	insights = append(insights, facts.Insight{
		Title: fmt.Sprintf("Stability metrics (%d modules)", len(metrics)),
		Description: fmt.Sprintf(
			"Computed Robert Martin's stability metrics for %d modules. %d on Main Sequence, %d in Zone of Pain, %d in Zone of Uselessness.",
			len(metrics),
			len(metrics)-len(painModules)-len(uselessModules),
			len(painModules),
			len(uselessModules),
		),
		Confidence: 1.0,
		Evidence:   evidence,
		Actions: []string{
			"Move modules closer to the Main Sequence (A + I ~ 1)",
			"Extract interfaces from Zone of Pain modules to increase abstractness",
			"Remove or consolidate Zone of Uselessness modules",
		},
	})

	// Zone of Pain warnings
	for _, name := range painModules {
		m := metrics[name]
		insights = append(insights, facts.Insight{
			Title: fmt.Sprintf("Zone of Pain: %s", name),
			Description: fmt.Sprintf(
				"Module %q is stable (I=%.2f) and concrete (A=%.2f), placing it in the Zone of Pain (D=%.2f). "+
					"It is depended upon by %d module(s) but has no abstractions, making it rigid and hard to change.",
				name, m.Instability, m.Abstractness, m.Distance, m.Ca,
			),
			Confidence: 0.9,
			Evidence: []facts.Evidence{
				{Fact: name, Detail: fmt.Sprintf("Ca=%d Ce=%d I=%.2f A=%.2f D=%.2f", m.Ca, m.Ce, m.Instability, m.Abstractness, m.Distance)},
			},
			Actions: []string{
				"Introduce interfaces to increase abstractness",
				"Apply Dependency Inversion Principle",
				"Extract abstract types to a separate port/interface package",
			},
		})
	}

	// Zone of Uselessness warnings
	for _, name := range uselessModules {
		m := metrics[name]
		insights = append(insights, facts.Insight{
			Title: fmt.Sprintf("Zone of Uselessness: %s", name),
			Description: fmt.Sprintf(
				"Module %q is unstable (I=%.2f) and abstract (A=%.2f), placing it in the Zone of Uselessness (D=%.2f). "+
					"It defines abstractions that nothing depends on.",
				name, m.Instability, m.Abstractness, m.Distance,
			),
			Confidence: 0.9,
			Evidence: []facts.Evidence{
				{Fact: name, Detail: fmt.Sprintf("Ca=%d Ce=%d I=%.2f A=%.2f D=%.2f", m.Ca, m.Ce, m.Instability, m.Abstractness, m.Distance)},
			},
			Actions: []string{
				"Remove unused abstractions",
				"Consolidate with implementing modules",
				"Verify these interfaces are actually needed",
			},
		})
	}

	return insights, nil
}

// computeMetrics calculates stability metrics for every module in the store.
func computeMetrics(store *facts.Store) map[string]ModuleMetrics {
	modules := store.ByKind(facts.KindModule)
	if len(modules) == 0 {
		return nil
	}

	moduleSet := make(map[string]bool, len(modules))
	for _, m := range modules {
		moduleSet[m.Name] = true
	}

	// Build the internal dependency graph (same pattern as cycles.go)
	graph := buildDependencyGraph(store)

	// Count Ca (afferent: who depends on me) and Ce (efferent: who do I depend on)
	caCount := make(map[string]int)
	ceCount := make(map[string]int)

	for src, targets := range graph {
		// Deduplicate targets for this source
		seen := make(map[string]bool)
		for _, tgt := range targets {
			if seen[tgt] || tgt == src {
				continue
			}
			seen[tgt] = true
			ceCount[src]++
			caCount[tgt]++
		}
	}

	// Count abstract vs total symbols per module
	symbols := store.ByKind(facts.KindSymbol)
	abstractCount := make(map[string]int)
	totalCount := make(map[string]int)
	for _, sym := range symbols {
		mod := fileDir(sym.File)
		if !moduleSet[mod] {
			continue
		}
		totalCount[mod]++
		if isAbstract(sym) {
			abstractCount[mod]++
		}
	}

	// Compute metrics for each module
	result := make(map[string]ModuleMetrics, len(modules))
	for _, m := range modules {
		name := m.Name
		ca := caCount[name]
		ce := ceCount[name]

		var instability float64
		if ca+ce > 0 {
			instability = float64(ce) / float64(ca+ce)
		}

		var abstractness float64
		total := totalCount[name]
		if total > 0 {
			abstractness = float64(abstractCount[name]) / float64(total)
		}

		distance := math.Abs(abstractness + instability - 1.0)

		result[name] = ModuleMetrics{
			Module:       name,
			Ca:           ca,
			Ce:           ce,
			Instability:  instability,
			Abstractness: abstractness,
			Distance:     distance,
		}
	}

	return result
}

// classifyZone determines which zone a module falls in based on its metrics.
// Threshold: D > 0.5 triggers zone classification; otherwise Main Sequence.
func classifyZone(m ModuleMetrics) string {
	const threshold = 0.5
	if m.Distance <= threshold {
		return ZoneMainSequence
	}
	// High distance: determine which zone based on quadrant
	// Zone of Pain: low I (stable) + low A (concrete)
	// Zone of Uselessness: high I (unstable) + high A (abstract)
	if m.Instability < 0.5 && m.Abstractness < 0.5 {
		return ZonePain
	}
	if m.Instability >= 0.5 && m.Abstractness >= 0.5 {
		return ZoneUselessness
	}
	return ZoneMainSequence
}

// buildDependencyGraph extracts module-level import relationships, filtering externals.
func buildDependencyGraph(store *facts.Store) map[string][]string {
	graph := make(map[string][]string)

	modules := store.ByKind(facts.KindModule)
	moduleNames := make(map[string]bool)
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

// isAbstract returns true if the symbol is an interface (abstract type).
func isAbstract(sym facts.Fact) bool {
	if sk, ok := sym.Props["symbol_kind"]; ok {
		return sk == facts.SymbolInterface
	}
	return false
}

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
