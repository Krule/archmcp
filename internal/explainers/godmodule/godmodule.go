package godmodule

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dejo1307/archmcp/internal/facts"
)

const (
	symbolThreshold = 30 // >30 symbols flags a god module
	fanThreshold    = 15 // fan-in + fan-out >15 flags a god module
)

// GodModuleExplainer detects modules that have grown too large or too
// interconnected — so-called "god modules".  It counts per-module symbols,
// fan-in (modules that import this one), and fan-out (modules this one
// imports), then flags any module that exceeds the thresholds.
type GodModuleExplainer struct{}

// New creates a new GodModuleExplainer.
func New() *GodModuleExplainer {
	return &GodModuleExplainer{}
}

func (e *GodModuleExplainer) Name() string {
	return "godmodule"
}

// moduleMetrics holds the computed metrics for a single module.
type moduleMetrics struct {
	symbols int
	fanIn   int
	fanOut  int
}

// severity returns a comparable score: higher means more "god-like".
func (m moduleMetrics) severity() int {
	s := 0
	if m.symbols > symbolThreshold {
		s += m.symbols - symbolThreshold
	}
	fan := m.fanIn + m.fanOut
	if fan > fanThreshold {
		s += (fan - fanThreshold) * 2 // weight connectivity higher
	}
	return s
}

// isGod returns true if the module exceeds either threshold.
func (m moduleMetrics) isGod() bool {
	return m.symbols > symbolThreshold || (m.fanIn+m.fanOut) > fanThreshold
}

// Explain analyzes the fact store and reports god modules.
func (e *GodModuleExplainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	modules := store.ByKind(facts.KindModule)
	if len(modules) == 0 {
		return nil, nil
	}

	metrics := computeMetrics(store)

	// Collect flagged modules.
	type entry struct {
		name    string
		metrics moduleMetrics
	}
	var flagged []entry
	for name, m := range metrics {
		if m.isGod() {
			flagged = append(flagged, entry{name, m})
		}
	}

	if len(flagged) == 0 {
		return nil, nil
	}

	// Sort by severity descending.
	sort.Slice(flagged, func(i, j int) bool {
		return flagged[i].metrics.severity() > flagged[j].metrics.severity()
	})

	evidence := make([]facts.Evidence, 0, len(flagged))
	for _, f := range flagged {
		evidence = append(evidence, facts.Evidence{
			Fact: f.name,
			Detail: fmt.Sprintf(
				"symbols=%d, fan-in=%d, fan-out=%d",
				f.metrics.symbols, f.metrics.fanIn, f.metrics.fanOut,
			),
		})
	}

	// Compute confidence based on how far above thresholds the worst offender is.
	worst := flagged[0].metrics
	confidence := 0.7
	if worst.symbols > symbolThreshold*2 || (worst.fanIn+worst.fanOut) > fanThreshold*2 {
		confidence = 0.9
	}

	insight := facts.Insight{
		Title: fmt.Sprintf("God module(s) detected: %d module(s) exceed complexity thresholds", len(flagged)),
		Description: fmt.Sprintf(
			"Found %d module(s) exceeding god-module thresholds (>%d symbols or fan-in+fan-out>%d). "+
				"Large, highly-connected modules are hard to understand, test, and refactor.",
			len(flagged), symbolThreshold, fanThreshold,
		),
		Confidence: confidence,
		Evidence:   evidence,
		Actions: []string{
			"Split large modules into smaller, focused packages",
			"Extract shared types into a separate package to reduce fan-in",
			"Introduce interfaces to decouple consumers and reduce fan-out",
		},
	}

	return []facts.Insight{insight}, nil
}

// computeMetrics builds per-module metrics from the fact store.
func computeMetrics(store *facts.Store) map[string]moduleMetrics {
	modules := store.ByKind(facts.KindModule)
	metrics := make(map[string]moduleMetrics, len(modules))

	// Initialize all modules.
	moduleSet := make(map[string]bool, len(modules))
	for _, m := range modules {
		moduleSet[m.Name] = true
		metrics[m.Name] = moduleMetrics{}
	}

	// Count fan-out (imports) and fan-in (reverse).
	for _, m := range modules {
		out := 0
		for _, r := range m.Relations {
			if r.Kind == facts.RelImports && moduleSet[r.Target] {
				out++
				// Increment fan-in of the target.
				tgt := metrics[r.Target]
				tgt.fanIn++
				metrics[r.Target] = tgt
			}
		}
		cur := metrics[m.Name]
		cur.fanOut = out
		metrics[m.Name] = cur
	}

	// Count symbols per module by matching file paths.
	symbols := store.ByKind(facts.KindSymbol)
	for _, sym := range symbols {
		owner := ownerModule(sym.File, moduleSet)
		if owner == "" {
			continue
		}
		cur := metrics[owner]
		cur.symbols++
		metrics[owner] = cur
	}

	return metrics
}

// ownerModule finds the module that owns a given file path.
// It picks the longest module name that is a prefix of the file's directory.
func ownerModule(filePath string, moduleSet map[string]bool) string {
	dir := fileDir(filePath)
	best := ""
	for mod := range moduleSet {
		if strings.HasPrefix(dir, mod) && len(mod) > len(best) {
			best = mod
		}
	}
	return best
}

func fileDir(file string) string {
	parts := strings.Split(file, "/")
	if len(parts) <= 1 {
		return "."
	}
	return strings.Join(parts[:len(parts)-1], "/")
}
