package cohesion

import (
	"context"
	"math"
	"sort"
	"strings"
	"testing"

	"github.com/dejo1307/archmcp/internal/facts"
)

// --- helpers ---

func makeStore(modules []string, symbols map[string][]symbolDef) *facts.Store {
	s := facts.NewStore()
	for _, m := range modules {
		s.Add(facts.Fact{Kind: facts.KindModule, Name: m})
	}
	for mod, defs := range symbols {
		for _, def := range defs {
			rels := make([]facts.Relation, 0, len(def.calls))
			for _, target := range def.calls {
				rels = append(rels, facts.Relation{Kind: facts.RelCalls, Target: target})
			}
			s.Add(facts.Fact{
				Kind:      facts.KindSymbol,
				Name:      def.name,
				File:      mod + "/file.go",
				Props:     map[string]any{"symbol_kind": def.kind},
				Relations: rels,
			})
		}
	}
	return s
}

type symbolDef struct {
	name  string
	kind  string
	calls []string // targets of "calls" relations
}

// --- union-find unit tests ---

func TestUnionFind_Basic(t *testing.T) {
	uf := newUnionFind(5)
	uf.union(0, 1)
	uf.union(2, 3)

	if uf.find(0) != uf.find(1) {
		t.Error("0 and 1 should be in same component")
	}
	if uf.find(2) != uf.find(3) {
		t.Error("2 and 3 should be in same component")
	}
	if uf.find(0) == uf.find(2) {
		t.Error("0 and 2 should be in different components")
	}
	if uf.find(4) == uf.find(0) {
		t.Error("4 should be isolated")
	}
}

func TestUnionFind_TransitiveUnion(t *testing.T) {
	uf := newUnionFind(4)
	uf.union(0, 1)
	uf.union(1, 2)
	uf.union(2, 3)

	root := uf.find(0)
	for i := 1; i < 4; i++ {
		if uf.find(i) != root {
			t.Errorf("node %d should share root with 0", i)
		}
	}
}

func TestUnionFind_Components(t *testing.T) {
	uf := newUnionFind(6)
	uf.union(0, 1)
	uf.union(1, 2)
	uf.union(3, 4)
	// 5 is isolated

	count := uf.componentCount()
	if count != 3 {
		t.Errorf("expected 3 components, got %d", count)
	}
}

func TestUnionFind_SingleElement(t *testing.T) {
	uf := newUnionFind(1)
	if uf.componentCount() != 1 {
		t.Errorf("expected 1 component for single element")
	}
	if uf.find(0) != 0 {
		t.Error("single element should be its own root")
	}
}

// --- cohesion score tests ---

func TestCohesionScore_FullyConnected(t *testing.T) {
	// 3 symbols all calling each other: all pairs connected
	// total_pairs = 3*2/2 = 3, connected_pairs = 3
	score := computeCohesion([]string{"A", "B", "C"}, map[string]map[string]bool{
		"A": {"B": true, "C": true},
		"B": {"A": true, "C": true},
		"C": {"A": true, "B": true},
	})
	if math.Abs(score-1.0) > 0.001 {
		t.Errorf("fully connected: expected score ~1.0, got %f", score)
	}
}

func TestCohesionScore_TotallyDisconnected(t *testing.T) {
	// 3 symbols with no calls between them
	score := computeCohesion([]string{"A", "B", "C"}, map[string]map[string]bool{})
	if score != 0.0 {
		t.Errorf("totally disconnected: expected score 0.0, got %f", score)
	}
}

func TestCohesionScore_Partial(t *testing.T) {
	// 4 symbols: A-B connected, C-D connected, no cross connection
	// total_pairs = 4*3/2 = 6, connected_pairs = 2 (A-B, C-D)
	score := computeCohesion([]string{"A", "B", "C", "D"}, map[string]map[string]bool{
		"A": {"B": true},
		"B": {"A": true},
		"C": {"D": true},
		"D": {"C": true},
	})
	expected := 2.0 / 6.0
	if math.Abs(score-expected) > 0.001 {
		t.Errorf("partial: expected score ~%f, got %f", expected, score)
	}
}

func TestCohesionScore_SingleSymbol(t *testing.T) {
	// 1 symbol: no pairs, score should be 1.0 (trivially cohesive)
	score := computeCohesion([]string{"A"}, map[string]map[string]bool{})
	if score != 1.0 {
		t.Errorf("single symbol: expected 1.0, got %f", score)
	}
}

func TestCohesionScore_Empty(t *testing.T) {
	score := computeCohesion(nil, nil)
	if score != 1.0 {
		t.Errorf("empty: expected 1.0, got %f", score)
	}
}

// --- disconnected components detection ---

func TestDetectComponents_FullyConnected(t *testing.T) {
	syms := []string{"A", "B", "C"}
	adj := map[string]map[string]bool{
		"A": {"B": true},
		"B": {"C": true},
	}
	comps := detectComponents(syms, adj)
	if len(comps) != 1 {
		t.Errorf("expected 1 component, got %d", len(comps))
	}
}

func TestDetectComponents_TwoComponents(t *testing.T) {
	syms := []string{"A", "B", "C", "D"}
	adj := map[string]map[string]bool{
		"A": {"B": true},
		"C": {"D": true},
	}
	comps := detectComponents(syms, adj)
	if len(comps) != 2 {
		t.Errorf("expected 2 components, got %d", len(comps))
	}
}

func TestDetectComponents_AllIsolated(t *testing.T) {
	syms := []string{"A", "B", "C"}
	adj := map[string]map[string]bool{}
	comps := detectComponents(syms, adj)
	if len(comps) != 3 {
		t.Errorf("expected 3 components, got %d", len(comps))
	}
}

// --- Explainer interface compliance ---

func TestName(t *testing.T) {
	e := New()
	if e.Name() != "cohesion" {
		t.Errorf("Name() = %q, want %q", e.Name(), "cohesion")
	}
}

// --- Integration: Explain ---

func TestExplain_EmptyStore(t *testing.T) {
	s := facts.NewStore()
	e := New()
	insights, err := e.Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights for empty store, got %d", len(insights))
	}
}

func TestExplain_FullyCohesiveModule(t *testing.T) {
	// Module with all functions calling each other — no insight expected
	store := makeStore(
		[]string{"pkg/api"},
		map[string][]symbolDef{
			"pkg/api": {
				{name: "pkg/api.HandleRequest", kind: facts.SymbolFunc, calls: []string{"pkg/api.Validate"}},
				{name: "pkg/api.Validate", kind: facts.SymbolFunc, calls: []string{"pkg/api.HandleRequest"}},
			},
		},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights for fully cohesive module, got %d", len(insights))
	}
}

func TestExplain_LowCohesionModule(t *testing.T) {
	// Module with many disconnected symbols — should flag low cohesion
	store := makeStore(
		[]string{"pkg/kitchen_sink"},
		map[string][]symbolDef{
			"pkg/kitchen_sink": {
				{name: "pkg/kitchen_sink.A", kind: facts.SymbolFunc, calls: nil},
				{name: "pkg/kitchen_sink.B", kind: facts.SymbolFunc, calls: nil},
				{name: "pkg/kitchen_sink.C", kind: facts.SymbolFunc, calls: nil},
				{name: "pkg/kitchen_sink.D", kind: facts.SymbolFunc, calls: nil},
				{name: "pkg/kitchen_sink.E", kind: facts.SymbolFunc, calls: nil},
			},
		},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) == 0 {
		t.Fatal("expected insight for low cohesion module")
	}
	insight := insights[0]
	if !strings.Contains(insight.Title, "pkg/kitchen_sink") {
		t.Errorf("expected module name in title, got %q", insight.Title)
	}
	if insight.Confidence < 0.5 {
		t.Errorf("confidence too low: %f", insight.Confidence)
	}
	if len(insight.Actions) == 0 {
		t.Error("expected suggested actions")
	}
}

func TestExplain_DisconnectedComponents(t *testing.T) {
	// Module with two disconnected clusters — should flag multiple components
	store := makeStore(
		[]string{"pkg/mixed"},
		map[string][]symbolDef{
			"pkg/mixed": {
				// Cluster 1: A calls B
				{name: "pkg/mixed.A", kind: facts.SymbolFunc, calls: []string{"pkg/mixed.B"}},
				{name: "pkg/mixed.B", kind: facts.SymbolFunc, calls: nil},
				// Cluster 2: C calls D
				{name: "pkg/mixed.C", kind: facts.SymbolFunc, calls: []string{"pkg/mixed.D"}},
				{name: "pkg/mixed.D", kind: facts.SymbolFunc, calls: nil},
			},
		},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) == 0 {
		t.Fatal("expected insight for disconnected components")
	}
	found := false
	for _, ins := range insights {
		if strings.Contains(ins.Description, "disconnected") || strings.Contains(ins.Description, "component") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected insight mentioning disconnected components")
	}
}

func TestExplain_RankedByLowestCohesion(t *testing.T) {
	// Two modules: one with lower cohesion should appear first
	store := makeStore(
		[]string{"pkg/good", "pkg/bad"},
		map[string][]symbolDef{
			"pkg/good": {
				// Somewhat connected (A->B, but C isolated) — cohesion = 1/3
				{name: "pkg/good.A", kind: facts.SymbolFunc, calls: []string{"pkg/good.B"}},
				{name: "pkg/good.B", kind: facts.SymbolFunc, calls: nil},
				{name: "pkg/good.C", kind: facts.SymbolFunc, calls: nil},
			},
			"pkg/bad": {
				// All isolated — cohesion = 0
				{name: "pkg/bad.W", kind: facts.SymbolFunc, calls: nil},
				{name: "pkg/bad.X", kind: facts.SymbolFunc, calls: nil},
				{name: "pkg/bad.Y", kind: facts.SymbolFunc, calls: nil},
				{name: "pkg/bad.Z", kind: facts.SymbolFunc, calls: nil},
			},
		},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) < 2 {
		t.Fatalf("expected at least 2 insights, got %d", len(insights))
	}
	// First insight should be for the worse module (pkg/bad)
	if !strings.Contains(insights[0].Title, "pkg/bad") {
		t.Errorf("expected pkg/bad first (lowest cohesion), got %q", insights[0].Title)
	}
}

func TestExplain_SkipsModulesWithFewSymbols(t *testing.T) {
	// Module with only 1 symbol should not produce insights
	store := makeStore(
		[]string{"pkg/tiny"},
		map[string][]symbolDef{
			"pkg/tiny": {
				{name: "pkg/tiny.OnlyFunc", kind: facts.SymbolFunc, calls: nil},
			},
		},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights for single-symbol module, got %d", len(insights))
	}
}

func TestExplain_OnlyCountsFunctionsAndMethods(t *testing.T) {
	// Module with structs/interfaces should not count those as cohesion symbols
	store := makeStore(
		[]string{"pkg/types"},
		map[string][]symbolDef{
			"pkg/types": {
				{name: "pkg/types.MyStruct", kind: facts.SymbolStruct, calls: nil},
				{name: "pkg/types.MyInterface", kind: facts.SymbolInterface, calls: nil},
				{name: "pkg/types.Handler", kind: facts.SymbolFunc, calls: nil},
			},
		},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	// Only 1 function — too few to flag
	if len(insights) != 0 {
		t.Errorf("expected 0 insights (only 1 function), got %d", len(insights))
	}
}

// --- buildModuleCallGraph tests ---

func TestBuildModuleCallGraph(t *testing.T) {
	store := makeStore(
		[]string{"pkg/a"},
		map[string][]symbolDef{
			"pkg/a": {
				{name: "pkg/a.Foo", kind: facts.SymbolFunc, calls: []string{"pkg/a.Bar"}},
				{name: "pkg/a.Bar", kind: facts.SymbolFunc, calls: []string{"pkg/a.Baz"}},
				{name: "pkg/a.Baz", kind: facts.SymbolFunc, calls: nil},
			},
		},
	)

	modSyms, modAdj := buildModuleCallGraphs(store)

	syms, ok := modSyms["pkg/a"]
	if !ok {
		t.Fatal("expected pkg/a in module symbols")
	}
	sort.Strings(syms)
	expected := []string{"pkg/a.Bar", "pkg/a.Baz", "pkg/a.Foo"}
	if len(syms) != len(expected) {
		t.Fatalf("syms = %v, want %v", syms, expected)
	}
	for i, s := range syms {
		if s != expected[i] {
			t.Errorf("syms[%d] = %q, want %q", i, s, expected[i])
		}
	}

	adj := modAdj["pkg/a"]
	if !adj["pkg/a.Foo"]["pkg/a.Bar"] {
		t.Error("expected Foo->Bar edge")
	}
	if !adj["pkg/a.Bar"]["pkg/a.Baz"] {
		t.Error("expected Bar->Baz edge")
	}
}
