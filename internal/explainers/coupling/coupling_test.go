package coupling

import (
	"context"
	"strings"
	"testing"

	"github.com/dejo1307/archmcp/internal/facts"
)

// --- helpers ---

// makeStore creates a store with modules and inter-module relations (imports + calls).
func makeStore(modules []string, imports map[string][]string, calls map[string][]string) *facts.Store {
	s := facts.NewStore()
	for _, m := range modules {
		s.Add(facts.Fact{Kind: facts.KindModule, Name: m})
	}
	for src, targets := range imports {
		for _, tgt := range targets {
			s.Add(facts.Fact{
				Kind: facts.KindDependency,
				File: src + "/file.go",
				Relations: []facts.Relation{
					{Kind: facts.RelImports, Target: tgt},
				},
			})
		}
	}
	for src, targets := range calls {
		for _, tgt := range targets {
			s.Add(facts.Fact{
				Kind: facts.KindSymbol,
				Name: src + ".Func",
				File: src + "/file.go",
				Relations: []facts.Relation{
					{Kind: facts.RelCalls, Target: tgt + ".Func"},
				},
			})
		}
	}
	return s
}

// --- Acceptance tests (OUTER LOOP) ---

func TestExplain_HighlyCoupledPair(t *testing.T) {
	// Two modules with >10 edges between them should produce a "highly coupled" insight.
	imports := map[string][]string{}
	calls := map[string][]string{}
	// Create 11 import edges from src/a -> src/b
	for i := 0; i < 6; i++ {
		imports["src/a"] = append(imports["src/a"], "src/b")
	}
	for i := 0; i < 6; i++ {
		calls["src/a"] = append(calls["src/a"], "src/b")
	}

	store := makeStore([]string{"src/a", "src/b"}, imports, calls)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	var found bool
	for _, ins := range insights {
		if strings.Contains(ins.Title, "Highly coupled") {
			found = true
			if ins.Confidence < 0.8 {
				t.Errorf("highly coupled insight confidence = %f, want >= 0.8", ins.Confidence)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected 'Highly coupled' insight for >10 edges, got %d insights: %+v", len(insights), insights)
	}
}

func TestExplain_HubModule(t *testing.T) {
	// src/hub imports 6 other modules — should be flagged as hub (high fan-out).
	imports := map[string][]string{
		"src/hub": {"src/a", "src/b", "src/c", "src/d", "src/e", "src/f"},
	}
	modules := []string{"src/hub", "src/a", "src/b", "src/c", "src/d", "src/e", "src/f"}

	store := makeStore(modules, imports, nil)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	var found bool
	for _, ins := range insights {
		if strings.Contains(ins.Title, "Hub module") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'Hub module' insight for high fan-out, got %d insights: %+v", len(insights), insights)
	}
}

func TestExplain_CouplingRatio(t *testing.T) {
	// Any non-trivial graph should produce a coupling ratio insight.
	imports := map[string][]string{
		"src/a": {"src/b"},
		"src/b": {"src/c"},
	}
	modules := []string{"src/a", "src/b", "src/c"}

	store := makeStore(modules, imports, nil)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	var found bool
	for _, ins := range insights {
		if strings.Contains(ins.Title, "Coupling ratio") {
			found = true
			if ins.Confidence != 1.0 {
				t.Errorf("coupling ratio confidence = %f, want 1.0", ins.Confidence)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected 'Coupling ratio' insight, got %d insights: %+v", len(insights), insights)
	}
}

func TestExplain_NoCouplingInsightsForDisconnected(t *testing.T) {
	// Isolated modules with no edges: only coupling ratio at 0%.
	modules := []string{"src/a", "src/b", "src/c"}
	store := makeStore(modules, nil, nil)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	for _, ins := range insights {
		if strings.Contains(ins.Title, "Highly coupled") || strings.Contains(ins.Title, "Hub module") {
			t.Errorf("unexpected insight for disconnected graph: %s", ins.Title)
		}
	}
}

func TestName(t *testing.T) {
	e := New()
	if e.Name() != "coupling" {
		t.Errorf("Name() = %q, want %q", e.Name(), "coupling")
	}
}

// --- Unit tests (INNER LOOP) ---

func TestBuildEdgeMatrix_Empty(t *testing.T) {
	store := makeStore(nil, nil, nil)
	matrix := buildEdgeMatrix(store)
	if len(matrix) != 0 {
		t.Errorf("expected empty matrix, got %d entries", len(matrix))
	}
}

func TestBuildEdgeMatrix_ImportsAndCalls(t *testing.T) {
	imports := map[string][]string{"src/a": {"src/b"}}
	calls := map[string][]string{"src/a": {"src/b"}}
	store := makeStore([]string{"src/a", "src/b"}, imports, calls)

	matrix := buildEdgeMatrix(store)
	key := edgeKey("src/a", "src/b")
	if matrix[key] != 2 {
		t.Errorf("matrix[%q] = %d, want 2", key, matrix[key])
	}
}

func TestBuildEdgeMatrix_BothDirections(t *testing.T) {
	imports := map[string][]string{
		"src/a": {"src/b"},
		"src/b": {"src/a"},
	}
	store := makeStore([]string{"src/a", "src/b"}, imports, nil)

	matrix := buildEdgeMatrix(store)
	// Both directions should be counted under the same canonical key
	key := edgeKey("src/a", "src/b")
	if matrix[key] != 2 {
		t.Errorf("matrix[%q] = %d, want 2", key, matrix[key])
	}
}

func TestEdgeKey_Canonical(t *testing.T) {
	// edgeKey should be order-independent
	k1 := edgeKey("src/a", "src/b")
	k2 := edgeKey("src/b", "src/a")
	if k1 != k2 {
		t.Errorf("edgeKey not canonical: %q != %q", k1, k2)
	}
}

func TestFanOut(t *testing.T) {
	imports := map[string][]string{
		"src/hub": {"src/a", "src/b", "src/c"},
	}
	store := makeStore([]string{"src/hub", "src/a", "src/b", "src/c"}, imports, nil)

	fo := computeFanOut(store)
	if fo["src/hub"] != 3 {
		t.Errorf("fanOut[src/hub] = %d, want 3", fo["src/hub"])
	}
}

func TestFanOut_NoDuplicates(t *testing.T) {
	// Multiple edges to the same target should count as fan-out=1
	imports := map[string][]string{
		"src/a": {"src/b", "src/b", "src/b"},
	}
	store := makeStore([]string{"src/a", "src/b"}, imports, nil)

	fo := computeFanOut(store)
	if fo["src/a"] != 1 {
		t.Errorf("fanOut[src/a] = %d, want 1 (unique targets)", fo["src/a"])
	}
}

func TestHighlyCoupledPairs(t *testing.T) {
	matrix := map[string]int{
		edgeKey("src/a", "src/b"): 15,
		edgeKey("src/c", "src/d"): 3,
	}
	pairs := highlyCoupledPairs(matrix, 10)
	if len(pairs) != 1 {
		t.Fatalf("expected 1 highly coupled pair, got %d", len(pairs))
	}
	if pairs[0].count != 15 {
		t.Errorf("pair count = %d, want 15", pairs[0].count)
	}
}

func TestHighlyCoupledPairs_Sorted(t *testing.T) {
	matrix := map[string]int{
		edgeKey("src/a", "src/b"): 15,
		edgeKey("src/c", "src/d"): 20,
		edgeKey("src/e", "src/f"): 12,
	}
	pairs := highlyCoupledPairs(matrix, 10)
	if len(pairs) != 3 {
		t.Fatalf("expected 3 pairs, got %d", len(pairs))
	}
	// Should be sorted descending by count
	for i := 1; i < len(pairs); i++ {
		if pairs[i].count > pairs[i-1].count {
			t.Errorf("pairs not sorted descending: %d > %d at index %d", pairs[i].count, pairs[i-1].count, i)
		}
	}
}

func TestCouplingRatio(t *testing.T) {
	tests := []struct {
		name     string
		edges    int
		modules  int
		wantZero bool
	}{
		{"no modules", 0, 0, true},
		{"one module", 1, 1, true},
		{"two modules 1 edge", 1, 2, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ratio := couplingRatio(tt.edges, tt.modules)
			if tt.wantZero && ratio != 0 {
				t.Errorf("ratio = %f, want 0", ratio)
			}
			if !tt.wantZero && ratio <= 0 {
				t.Errorf("ratio = %f, want > 0", ratio)
			}
		})
	}
}

func TestCouplingRatio_MaxOne(t *testing.T) {
	// With 3 modules, max possible pairs = 3. If edges = 3, ratio = 1.0
	ratio := couplingRatio(3, 3)
	if ratio != 1.0 {
		t.Errorf("ratio = %f, want 1.0 for fully connected 3-module graph", ratio)
	}
}
