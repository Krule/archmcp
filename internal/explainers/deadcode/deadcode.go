package deadcode

import (
	"context"
	"fmt"
	"strings"

	"github.com/dejo1307/archmcp/internal/facts"
)

// DeadCodeExplainer finds symbols with zero incoming calls/imports that are
// likely unreachable dead code. It excludes main/init functions, exported
// symbols, test functions, and interface implementations.
type DeadCodeExplainer struct{}

// New creates a new DeadCodeExplainer.
func New() *DeadCodeExplainer {
	return &DeadCodeExplainer{}
}

func (e *DeadCodeExplainer) Name() string {
	return "deadcode"
}

// Explain analyzes the fact store and reports unreachable symbols.
func (e *DeadCodeExplainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	symbols := store.ByKind(facts.KindSymbol)
	if len(symbols) == 0 {
		return nil, nil
	}

	// Build a set of all symbol names that are targets of calls/imports.
	referenced := buildReferencedSet(symbols)

	// Find dead symbols: no incoming references and not excluded.
	var dead []facts.Fact
	for _, sym := range symbols {
		if isExcluded(sym) {
			continue
		}
		if !referenced[sym.Name] {
			dead = append(dead, sym)
		}
	}

	if len(dead) == 0 {
		return nil, nil
	}

	evidence := make([]facts.Evidence, 0, len(dead))
	for _, d := range dead {
		symbolKind, _ := d.Props["symbol_kind"].(string)
		evidence = append(evidence, facts.Evidence{
			Symbol: d.Name,
			File:   d.File,
			Detail: fmt.Sprintf("%s %q in %s has no callers or importers", symbolKind, d.Name, d.File),
		})
	}

	insight := facts.Insight{
		Title:       fmt.Sprintf("Potentially dead code: %d unreachable symbol(s)", len(dead)),
		Description: fmt.Sprintf("Found %d symbol(s) with zero incoming calls or imports. These symbols are not exported, not entrypoints (main/init), not test functions, and do not implement interfaces — they may be safely removable.", len(dead)),
		Confidence:  0.7, // Static analysis can't catch all dynamic references
		Evidence:    evidence,
		Actions: []string{
			"Verify these symbols are truly unused (check for reflection, code generation, build tags)",
			"Remove confirmed dead code to reduce maintenance burden",
			"Consider adding tests if the code is actually needed",
		},
	}

	return []facts.Insight{insight}, nil
}

// buildReferencedSet collects all symbol names that are targets of calls or
// imports relations from any symbol in the store.
func buildReferencedSet(symbols []facts.Fact) map[string]bool {
	ref := make(map[string]bool)
	for _, sym := range symbols {
		for _, r := range sym.Relations {
			if r.Kind == facts.RelCalls || r.Kind == facts.RelImports {
				ref[r.Target] = true
			}
		}
	}
	return ref
}

// isExcluded returns true if a symbol should be excluded from dead code analysis.
func isExcluded(sym facts.Fact) bool {
	name := sym.Name

	// Exclude main and init entrypoints (bare "main" or qualified "src.main")
	simpleName := name
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		simpleName = name[idx+1:]
	}
	if simpleName == "main" || simpleName == "init" {
		return true
	}

	// Exclude exported symbols (may be used by external consumers)
	if exported, ok := sym.Props["exported"].(bool); ok && exported {
		return true
	}

	// Exclude test functions (Go: Test*, Benchmark*, Example*)
	if isTestFunction(simpleName) {
		return true
	}

	// Exclude interface/trait implementations
	for _, r := range sym.Relations {
		if r.Kind == facts.RelImplements {
			return true
		}
	}

	return false
}

// isTestFunction checks if a name matches Go test function patterns.
func isTestFunction(name string) bool {
	return strings.HasPrefix(name, "Test") ||
		strings.HasPrefix(name, "Benchmark") ||
		strings.HasPrefix(name, "Example")
}
