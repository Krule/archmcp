package depdepth

import (
	"context"
	"strings"
	"testing"

	"github.com/dejo1307/archmcp/internal/facts"
)

// --- helpers ---

func makeStore(modules []string, deps map[string][]string) *facts.Store {
	s := facts.NewStore()
	for _, m := range modules {
		s.Add(facts.Fact{Kind: facts.KindModule, Name: m})
	}
	for src, targets := range deps {
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
	return s
}

// ============================================================
// Acceptance tests (OUTER LOOP)
// ============================================================

func TestAcceptance_Name(t *testing.T) {
	e := New()
	if e.Name() != "depdepth" {
		t.Errorf("Name() = %q, want %q", e.Name(), "depdepth")
	}
}

func TestAcceptance_EmptyStore(t *testing.T) {
	e := New()
	insights, err := e.Explain(context.Background(), facts.NewStore())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights for empty store, got %d", len(insights))
	}
}

func TestAcceptance_LinearChain(t *testing.T) {
	// src/a -> src/b -> src/c -> src/d -> src/e -> src/f -> src/g (depth 6 from src/a)
	store := makeStore(
		[]string{"src/a", "src/b", "src/c", "src/d", "src/e", "src/f", "src/g"},
		map[string][]string{
			"src/a": {"src/b"},
			"src/b": {"src/c"},
			"src/c": {"src/d"},
			"src/d": {"src/e"},
			"src/e": {"src/f"},
			"src/f": {"src/g"},
		},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should produce insights: deepest chain report + modules with depth > 5
	if len(insights) == 0 {
		t.Fatal("expected at least 1 insight for a deep chain")
	}

	// Should report deepest chain
	var foundDeepest bool
	var foundDeepModules bool
	for _, ins := range insights {
		if strings.Contains(ins.Title, "Deepest dependency chain") {
			foundDeepest = true
			// Chain depth should be 6
			if !strings.Contains(ins.Description, "6") {
				t.Errorf("expected chain depth 6 in description, got: %s", ins.Description)
			}
		}
		if strings.Contains(ins.Title, "depth") && strings.Contains(ins.Title, "exceed") {
			foundDeepModules = true
		}
	}
	if !foundDeepest {
		t.Errorf("missing 'Deepest dependency chain' insight, got: %v", insightTitles(insights))
	}
	if !foundDeepModules {
		t.Errorf("missing deep modules insight, got: %v", insightTitles(insights))
	}
}

func TestAcceptance_WithCycle(t *testing.T) {
	// src/a -> src/b -> src/c -> src/a (cycle) + src/d -> src/a
	// Should handle the cycle gracefully (not hang/crash)
	store := makeStore(
		[]string{"src/a", "src/b", "src/c", "src/d"},
		map[string][]string{
			"src/a": {"src/b"},
			"src/b": {"src/c"},
			"src/c": {"src/a"},
			"src/d": {"src/a"},
		},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should not crash/hang — the result should be valid insights
	_ = insights
}

func TestAcceptance_ShallowGraph_NoDeepInsight(t *testing.T) {
	// src/a -> src/b, src/c -> src/d (max depth 1)
	store := makeStore(
		[]string{"src/a", "src/b", "src/c", "src/d"},
		map[string][]string{
			"src/a": {"src/b"},
			"src/c": {"src/d"},
		},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No module exceeds depth 5, so no "exceed" insight
	for _, ins := range insights {
		if strings.Contains(ins.Title, "exceed") {
			t.Errorf("unexpected 'exceed' insight for shallow graph: %s", ins.Title)
		}
	}
}

func insightTitles(insights []facts.Insight) []string {
	titles := make([]string, len(insights))
	for i, ins := range insights {
		titles[i] = ins.Title
	}
	return titles
}

// ============================================================
// Unit tests (INNER LOOP)
// ============================================================

// --- tarjanSCC ---

func TestTarjanSCC_EmptyGraph(t *testing.T) {
	sccs := tarjanSCC(map[string][]string{})
	if len(sccs) != 0 {
		t.Errorf("expected 0 SCCs, got %d", len(sccs))
	}
}

func TestTarjanSCC_SingleNode(t *testing.T) {
	sccs := tarjanSCC(map[string][]string{"A": nil})
	if len(sccs) != 1 {
		t.Fatalf("expected 1 SCC, got %d", len(sccs))
	}
	if len(sccs[0]) != 1 || sccs[0][0] != "A" {
		t.Errorf("expected SCC [A], got %v", sccs[0])
	}
}

func TestTarjanSCC_SimpleCycle(t *testing.T) {
	sccs := tarjanSCC(map[string][]string{"A": {"B"}, "B": {"A"}})
	var cycles [][]string
	for _, scc := range sccs {
		if len(scc) > 1 {
			cycles = append(cycles, scc)
		}
	}
	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle, got %d", len(cycles))
	}
	if len(cycles[0]) != 2 {
		t.Errorf("expected cycle of size 2, got %d", len(cycles[0]))
	}
}

func TestTarjanSCC_Chain(t *testing.T) {
	sccs := tarjanSCC(map[string][]string{"A": {"B"}, "B": {"C"}, "C": nil})
	// All singletons, no cycles.
	for _, scc := range sccs {
		if len(scc) > 1 {
			t.Errorf("expected no cycles, got SCC of size %d: %v", len(scc), scc)
		}
	}
}

func TestTarjanSCC_SelfLoop(t *testing.T) {
	sccs := tarjanSCC(map[string][]string{"A": {"A"}})
	for _, scc := range sccs {
		if len(scc) > 1 {
			t.Errorf("self-loop should not produce SCC > 1, got %v", scc)
		}
	}
}

// --- mapNodesToSCC ---

func TestMapNodesToSCC(t *testing.T) {
	sccs := [][]string{{"X", "Y", "Z"}, {"A"}}
	m := mapNodesToSCC(sccs)
	if m["X"] != "X" || m["Y"] != "X" || m["Z"] != "X" {
		t.Errorf("X,Y,Z should map to rep X, got %v", m)
	}
	if m["A"] != "A" {
		t.Errorf("A should map to A, got %q", m["A"])
	}
}

// --- collapseSCCsToDAG ---

func TestCollapseSCCsToDAG_NoCycles(t *testing.T) {
	graph := map[string][]string{"A": {"B"}, "B": {"C"}, "C": nil}
	sccID := map[string]string{"A": "A", "B": "B", "C": "C"}
	dag := collapseSCCsToDAG(graph, sccID)

	if len(dag["A"]) != 1 || dag["A"][0] != "B" {
		t.Errorf("A -> B expected, got %v", dag["A"])
	}
	if len(dag["B"]) != 1 || dag["B"][0] != "C" {
		t.Errorf("B -> C expected, got %v", dag["B"])
	}
}

func TestCollapseSCCsToDAG_WithCycle(t *testing.T) {
	// A <-> B form a cycle, C is separate
	graph := map[string][]string{"A": {"B"}, "B": {"A", "C"}, "C": nil}
	sccID := map[string]string{"A": "A", "B": "A", "C": "C"} // A,B collapsed to A
	dag := collapseSCCsToDAG(graph, sccID)

	// The collapsed A should point to C.
	if len(dag["A"]) != 1 || dag["A"][0] != "C" {
		t.Errorf("collapsed A -> C expected, got %v", dag["A"])
	}
	// No self-edges.
	for node, targets := range dag {
		for _, tgt := range targets {
			if node == tgt {
				t.Errorf("self-edge found: %s -> %s", node, tgt)
			}
		}
	}
}

// --- longestPathDepths ---

func TestLongestPathDepths_Chain(t *testing.T) {
	dag := map[string][]string{"A": {"B"}, "B": {"C"}, "C": nil}
	depths := longestPathDepths(dag)
	if depths["A"] != 0 {
		t.Errorf("A depth = %d, want 0", depths["A"])
	}
	if depths["B"] != 1 {
		t.Errorf("B depth = %d, want 1", depths["B"])
	}
	if depths["C"] != 2 {
		t.Errorf("C depth = %d, want 2", depths["C"])
	}
}

func TestLongestPathDepths_Diamond(t *testing.T) {
	//   A
	//  / \
	// B   C
	//  \ /
	//   D
	dag := map[string][]string{
		"A": {"B", "C"},
		"B": {"D"},
		"C": {"D"},
		"D": nil,
	}
	depths := longestPathDepths(dag)
	if depths["D"] != 2 {
		t.Errorf("D depth = %d, want 2 (A->B->D or A->C->D)", depths["D"])
	}
}

func TestLongestPathDepths_Disconnected(t *testing.T) {
	dag := map[string][]string{"A": nil, "B": nil}
	depths := longestPathDepths(dag)
	if depths["A"] != 0 || depths["B"] != 0 {
		t.Errorf("disconnected nodes should have depth 0, got A=%d B=%d", depths["A"], depths["B"])
	}
}

func TestLongestPathDepths_Empty(t *testing.T) {
	depths := longestPathDepths(map[string][]string{})
	if len(depths) != 0 {
		t.Errorf("expected empty depths, got %v", depths)
	}
}

// --- buildInsights ---

func TestBuildInsights_Empty(t *testing.T) {
	insights := buildInsights(map[string]int{})
	if insights != nil {
		t.Errorf("expected nil for empty depths, got %v", insights)
	}
}

func TestBuildInsights_NoDeepModules(t *testing.T) {
	depths := map[string]int{"A": 0, "B": 1, "C": 2}
	insights := buildInsights(depths)
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight (deepest chain only), got %d", len(insights))
	}
	if !strings.Contains(insights[0].Title, "Deepest dependency chain") {
		t.Errorf("unexpected title: %s", insights[0].Title)
	}
}

func TestBuildInsights_WithDeepModules(t *testing.T) {
	depths := map[string]int{"A": 0, "B": 3, "C": 6, "D": 7}
	insights := buildInsights(depths)
	if len(insights) != 2 {
		t.Fatalf("expected 2 insights (deepest + exceeding), got %d", len(insights))
	}
	var deepInsight *facts.Insight
	for i, ins := range insights {
		if strings.Contains(ins.Title, "exceed") {
			deepInsight = &insights[i]
		}
	}
	if deepInsight == nil {
		t.Fatal("missing exceed insight")
	}
	// C (6) and D (7) exceed 5.
	if len(deepInsight.Evidence) != 2 {
		t.Errorf("expected 2 deep modules, got %d", len(deepInsight.Evidence))
	}
}

// --- buildDependencyGraph ---

func TestBuildDependencyGraph_FiltersExternal(t *testing.T) {
	store := makeStore(
		[]string{"src/a", "src/b"},
		map[string][]string{
			"src/a": {"src/b", "fmt", "github.com/foo/bar"},
		},
	)
	graph := buildDependencyGraph(store)
	edges := graph["src/a"]
	if len(edges) != 1 || edges[0] != "src/b" {
		t.Errorf("expected [src/b], got %v", edges)
	}
}

func TestBuildDependencyGraph_OnlyModulesInStore(t *testing.T) {
	// src/a imports src/x which isn't a registered module — should be skipped.
	store := makeStore(
		[]string{"src/a", "src/b"},
		map[string][]string{
			"src/a": {"src/b", "src/x"},
		},
	)
	graph := buildDependencyGraph(store)
	edges := graph["src/a"]
	if len(edges) != 1 || edges[0] != "src/b" {
		t.Errorf("expected [src/b], got %v", edges)
	}
}

// --- ExplainerInterface ---

func TestImplementsExplainerInterface(t *testing.T) {
	// Compile-time check that DepDepthExplainer satisfies explainers.Explainer.
	var _ interface {
		Name() string
		Explain(context.Context, *facts.Store) ([]facts.Insight, error)
	} = New()
}
