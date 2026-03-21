package apisurface

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dejo1307/archmcp/internal/facts"
)

// APISurfaceExplainer finds exported symbols with zero external consumers
// (over-exposed API surface) and classifies symbols as over-exposed (0 refs),
// narrow (1 ref), or healthy (2+ refs). Excludes main/init, test functions,
// and interface implementations. Reports per-module API efficiency.
type APISurfaceExplainer struct{}

// New creates a new APISurfaceExplainer.
func New() *APISurfaceExplainer {
	return &APISurfaceExplainer{}
}

func (e *APISurfaceExplainer) Name() string {
	return "apisurface"
}

// symbolInfo holds a classified exported symbol.
type symbolInfo struct {
	Name   string
	File   string
	Module string
	Kind   string // "over-exposed", "narrow", "healthy"
	Refs   int
}

// moduleStats holds per-module API surface stats.
type moduleStats struct {
	Module      string
	Total       int
	OverExposed int
	Narrow      int
	Healthy     int
}

// Explain analyzes the fact store for API surface issues.
func (e *APISurfaceExplainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	symbols := store.ByKind(facts.KindSymbol)
	if len(symbols) == 0 {
		return nil, nil
	}

	// Collect exported symbols that pass exclusion filters.
	var apiSymbols []symbolInfo
	for _, sym := range symbols {
		if !isAPISymbol(sym) {
			continue
		}
		mod := moduleFromFile(sym.File)
		refs := countExternalConsumers(sym.Name, mod, symbols)
		apiSymbols = append(apiSymbols, symbolInfo{
			Name:   sym.Name,
			File:   sym.File,
			Module: mod,
			Kind:   classify(refs),
			Refs:   refs,
		})
	}

	if len(apiSymbols) == 0 {
		return nil, nil
	}

	var insights []facts.Insight

	// Insight 1: over-exposed symbols
	overExposed := filterByKind(apiSymbols, "over-exposed")
	if len(overExposed) > 0 {
		sort.Slice(overExposed, func(i, j int) bool {
			return overExposed[i].Name < overExposed[j].Name
		})
		evidence := make([]facts.Evidence, len(overExposed))
		for i, s := range overExposed {
			evidence[i] = facts.Evidence{
				Symbol: s.Name,
				File:   s.File,
				Detail: fmt.Sprintf("exported symbol %q in %s has 0 external consumers", s.Name, s.Module),
			}
		}
		insights = append(insights, facts.Insight{
			Title: fmt.Sprintf("%d over-exposed exported symbol(s) with 0 external consumers", len(overExposed)),
			Description: fmt.Sprintf(
				"Found %d exported symbol(s) that are not consumed by any external module. "+
					"These symbols expand the public API surface without demonstrated need, "+
					"increasing maintenance burden and coupling risk.",
				len(overExposed)),
			Confidence: 0.8,
			Evidence:   evidence,
			Actions: []string{
				"Consider making over-exposed symbols unexported if they are only used internally",
				"If the symbol is part of a public library API, add tests or documentation demonstrating intended use",
				"Remove truly unused exported symbols to shrink the API surface",
			},
		})
	}

	// Insight 2: per-module API surface efficiency summary
	modMap := buildModuleStats(apiSymbols)
	if len(modMap) > 0 {
		modules := sortedModules(modMap)
		evidence := make([]facts.Evidence, len(modules))
		for i, ms := range modules {
			efficiency := 0.0
			if ms.Total > 0 {
				efficiency = float64(ms.Healthy+ms.Narrow) / float64(ms.Total) * 100
			}
			evidence[i] = facts.Evidence{
				Fact: ms.Module,
				Detail: fmt.Sprintf(
					"%s: %d exported, %d healthy, %d narrow, %d over-exposed (%.0f%% efficiency)",
					ms.Module, ms.Total, ms.Healthy, ms.Narrow, ms.OverExposed, efficiency),
			}
		}

		totalOverExposed := len(overExposed)
		totalAPI := len(apiSymbols)
		overallEfficiency := 0.0
		if totalAPI > 0 {
			overallEfficiency = float64(totalAPI-totalOverExposed) / float64(totalAPI) * 100
		}

		insights = append(insights, facts.Insight{
			Title: fmt.Sprintf("API surface efficiency: %.0f%% (%d/%d symbols used externally)",
				overallEfficiency, totalAPI-totalOverExposed, totalAPI),
			Description: fmt.Sprintf(
				"Analyzed %d exported symbols across %d module(s). "+
					"%.0f%% of the API surface has at least one external consumer. "+
					"%d over-exposed, %d narrow (1 consumer), %d healthy (2+ consumers).",
				totalAPI, len(modules), overallEfficiency,
				totalOverExposed,
				countByKind(apiSymbols, "narrow"),
				countByKind(apiSymbols, "healthy")),
			Confidence: 0.9,
			Evidence:   evidence,
			Actions: []string{
				"Focus on modules with low efficiency — they may be over-abstracted",
				"Narrow symbols (1 consumer) may indicate tight coupling — consider inlining",
			},
		})
	}

	return insights, nil
}

// isAPISymbol returns true if the symbol should be included in API surface analysis.
// Excludes: unexported, main/init, test functions, interface implementations.
func isAPISymbol(sym facts.Fact) bool {
	name := sym.Name

	// Must be exported
	exported, ok := sym.Props["exported"].(bool)
	if !ok || !exported {
		return false
	}

	// Exclude main and init
	if name == "main" || name == "init" {
		return false
	}

	// Exclude test functions
	if isTestFunction(name) {
		return false
	}

	// Exclude interface/trait implementations
	for _, r := range sym.Relations {
		if r.Kind == facts.RelImplements {
			return false
		}
	}

	return true
}

// isTestFunction checks if a name matches Go test function patterns.
func isTestFunction(name string) bool {
	return strings.HasPrefix(name, "Test") ||
		strings.HasPrefix(name, "Benchmark") ||
		strings.HasPrefix(name, "Example")
}

// moduleFromFile extracts a module path from a file path by removing the filename.
func moduleFromFile(file string) string {
	parts := strings.Split(file, "/")
	if len(parts) <= 1 {
		return "."
	}
	return strings.Join(parts[:len(parts)-1], "/")
}

// countExternalConsumers counts distinct external modules that reference
// the given symbol via calls or imports relations.
func countExternalConsumers(symbolName, ownerModule string, symbols []facts.Fact) int {
	externalModules := make(map[string]bool)
	for _, sym := range symbols {
		callerModule := moduleFromFile(sym.File)
		if callerModule == ownerModule {
			continue
		}
		for _, r := range sym.Relations {
			if (r.Kind == facts.RelCalls || r.Kind == facts.RelImports) && r.Target == symbolName {
				externalModules[callerModule] = true
				break
			}
		}
	}
	return len(externalModules)
}

// classify returns the classification for a symbol based on external consumer count.
func classify(refs int) string {
	switch {
	case refs == 0:
		return "over-exposed"
	case refs == 1:
		return "narrow"
	default:
		return "healthy"
	}
}

func filterByKind(symbols []symbolInfo, kind string) []symbolInfo {
	var result []symbolInfo
	for _, s := range symbols {
		if s.Kind == kind {
			result = append(result, s)
		}
	}
	return result
}

func countByKind(symbols []symbolInfo, kind string) int {
	count := 0
	for _, s := range symbols {
		if s.Kind == kind {
			count++
		}
	}
	return count
}

func buildModuleStats(symbols []symbolInfo) map[string]*moduleStats {
	m := make(map[string]*moduleStats)
	for _, s := range symbols {
		ms, ok := m[s.Module]
		if !ok {
			ms = &moduleStats{Module: s.Module}
			m[s.Module] = ms
		}
		ms.Total++
		switch s.Kind {
		case "over-exposed":
			ms.OverExposed++
		case "narrow":
			ms.Narrow++
		case "healthy":
			ms.Healthy++
		}
	}
	return m
}

func sortedModules(m map[string]*moduleStats) []moduleStats {
	result := make([]moduleStats, 0, len(m))
	for _, ms := range m {
		result = append(result, *ms)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Module < result[j].Module
	})
	return result
}
