package testmap

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dejo1307/archmcp/internal/facts"
)

// TestMapExplainer maps test files to the modules they test and identifies
// untested modules, well-tested modules, and test density.
type TestMapExplainer struct{}

// New creates a new TestMapExplainer.
func New() *TestMapExplainer {
	return &TestMapExplainer{}
}

// Name returns the explainer identifier.
func (e *TestMapExplainer) Name() string {
	return "testmap"
}

// Explain analyzes the fact store and returns test-mapping insights.
func (e *TestMapExplainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	modules := store.ByKind(facts.KindModule)
	if len(modules) == 0 {
		return nil, nil
	}

	// Deduplicate module names.
	moduleSet := make(map[string]bool)
	var uniqueModules []facts.Fact
	for _, m := range modules {
		if !moduleSet[m.Name] {
			moduleSet[m.Name] = true
			uniqueModules = append(uniqueModules, m)
		}
	}

	// Collect all test files from the store.
	testFiles := collectTestFiles(store)

	// Map test files to modules by directory convention.
	mapping := mapTestsToModules(uniqueModules, testFiles)

	// Classify modules.
	var untested, tested []string
	for _, m := range uniqueModules {
		if tests, ok := mapping[m.Name]; ok && len(tests) > 0 {
			tested = append(tested, m.Name)
		} else {
			untested = append(untested, m.Name)
		}
	}

	var insights []facts.Insight

	// Insight 1: Test coverage summary.
	totalModules := len(uniqueModules)
	testedCount := len(tested)
	pct := 0.0
	if totalModules > 0 {
		pct = float64(testedCount) / float64(totalModules) * 100
	}

	summaryEvidence := make([]facts.Evidence, 0)
	for mod, tests := range mapping {
		if len(tests) > 0 {
			summaryEvidence = append(summaryEvidence, facts.Evidence{
				Fact:   mod,
				Detail: fmt.Sprintf("%d test file(s)", len(tests)),
			})
		}
	}

	insights = append(insights, facts.Insight{
		Title:       "Test coverage summary",
		Description: fmt.Sprintf("%d of %d modules have test files (%.0f%% module-level coverage).", testedCount, totalModules, pct),
		Confidence:  0.9,
		Evidence:    summaryEvidence,
	})

	// Insight 2: Untested modules.
	if len(untested) > 0 {
		sort.Strings(untested)
		evidence := make([]facts.Evidence, 0, len(untested))
		for _, mod := range untested {
			evidence = append(evidence, facts.Evidence{
				Fact:   mod,
				Detail: fmt.Sprintf("module %q has no test files", mod),
			})
		}

		insights = append(insights, facts.Insight{
			Title:       fmt.Sprintf("Untested modules (%d)", len(untested)),
			Description: fmt.Sprintf("%d module(s) have no associated test files: %s", len(untested), strings.Join(untested, ", ")),
			Confidence:  0.85,
			Evidence:    evidence,
			Actions: []string{
				"Add test files for untested modules",
				"Prioritize testing modules with high fan-in (many dependents)",
			},
		})
	}

	// Insight 3: Well-tested modules (with high test density).
	symbols := store.ByKind(facts.KindSymbol)
	symbolsByModule := countSymbolsByModule(symbols)

	type densityEntry struct {
		Module  string
		Tests   int
		Symbols int
		Density float64
	}

	var wellTested []densityEntry
	for mod, tests := range mapping {
		if len(tests) == 0 {
			continue
		}
		symCount := symbolsByModule[mod]
		if symCount == 0 {
			continue
		}
		density := float64(len(tests)) / float64(symCount)
		if density >= 0.5 { // At least 1 test file per 2 symbols
			wellTested = append(wellTested, densityEntry{
				Module:  mod,
				Tests:   len(tests),
				Symbols: symCount,
				Density: density,
			})
		}
	}

	if len(wellTested) > 0 {
		sort.Slice(wellTested, func(i, j int) bool {
			return wellTested[i].Density > wellTested[j].Density
		})

		evidence := make([]facts.Evidence, 0, len(wellTested))
		for _, wt := range wellTested {
			evidence = append(evidence, facts.Evidence{
				Fact:   wt.Module,
				Detail: fmt.Sprintf("%d test file(s) for %d symbol(s) (density: %.2f)", wt.Tests, wt.Symbols, wt.Density),
			})
		}

		insights = append(insights, facts.Insight{
			Title:       fmt.Sprintf("Well-tested modules (%d)", len(wellTested)),
			Description: fmt.Sprintf("%d module(s) have high test density (>=0.5 test files per exported symbol).", len(wellTested)),
			Confidence:  0.8,
			Evidence:    evidence,
		})
	}

	return insights, nil
}

// isTestFile checks if a file path matches common test file conventions.
func isTestFile(path string) bool {
	if path == "" {
		return false
	}

	base := filepath.Base(path)

	// Go: *_test.go
	if strings.HasSuffix(base, "_test.go") {
		return true
	}

	// Python: test_*.py (must have content after test_)
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") && len(base) > len("test_.py") {
		return true
	}

	// TypeScript/JavaScript: *.spec.{ts,tsx,js,jsx} or *.test.{ts,tsx,js,jsx}
	for _, ext := range []string{".spec.ts", ".test.ts", ".spec.js", ".test.js",
		".spec.tsx", ".test.tsx", ".spec.jsx", ".test.jsx"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}

	return false
}

// fileDir returns the directory component of a file path.
func fileDir(file string) string {
	if file == "" {
		return "."
	}
	parts := strings.Split(file, "/")
	if len(parts) <= 1 {
		return "."
	}
	return strings.Join(parts[:len(parts)-1], "/")
}

// collectTestFiles scans all facts and returns the set of unique test file paths.
func collectTestFiles(store *facts.Store) map[string]bool {
	testFiles := make(map[string]bool)
	for _, f := range store.All() {
		if f.File != "" && isTestFile(f.File) {
			testFiles[f.File] = true
		}
	}
	return testFiles
}

// mapTestsToModules maps test files to module names by directory convention.
// A test file belongs to a module if the test file's directory matches
// (or is a subdirectory of) the module path.
func mapTestsToModules(modules []facts.Fact, testFiles map[string]bool) map[string][]string {
	mapping := make(map[string][]string)

	for testFile := range testFiles {
		dir := fileDir(testFile)

		// Find the best matching module (longest prefix match).
		bestMatch := ""
		for _, m := range modules {
			if dir == m.Name || strings.HasPrefix(dir, m.Name+"/") {
				if len(m.Name) > len(bestMatch) {
					bestMatch = m.Name
				}
			}
		}
		if bestMatch != "" {
			mapping[bestMatch] = append(mapping[bestMatch], testFile)
		}
	}

	return mapping
}

// countSymbolsByModule counts exported symbols per module directory.
func countSymbolsByModule(symbols []facts.Fact) map[string]int {
	counts := make(map[string]int)
	for _, sym := range symbols {
		if sym.File == "" {
			continue
		}
		// Skip test file symbols.
		if isTestFile(sym.File) {
			continue
		}
		dir := fileDir(sym.File)
		counts[dir] = counts[dir] + 1
	}
	return counts
}
