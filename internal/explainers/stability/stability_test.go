package stability

import (
	"context"
	"math"
	"sort"
	"strings"
	"testing"

	"github.com/dejo1307/archmcp/internal/facts"
)

// --- helpers ---

// makeStore builds a facts.Store with modules, symbols (with symbol_kind props),
// and dependency/import relations.
func makeStore(modules []string, symbols map[string][]symbolDef, deps map[string][]string) *facts.Store {
	s := facts.NewStore()
	for _, m := range modules {
		s.Add(facts.Fact{Kind: facts.KindModule, Name: m})
	}
	for mod, syms := range symbols {
		for _, sym := range syms {
			s.Add(facts.Fact{
				Kind: facts.KindSymbol,
				Name: sym.name,
				File: mod + "/file.go",
				Props: map[string]any{
					"symbol_kind": sym.kind,
				},
			})
		}
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

type symbolDef struct {
	name string
	kind string
}

func approxEqual(a, b, epsilon float64) bool {
	return math.Abs(a-b) < epsilon
}

// --- Unit tests for computeMetrics ---

func TestComputeMetrics_SingleModuleNoDepsConcrete(t *testing.T) {
	// Module with no deps, only concrete symbols: Ca=0, Ce=0, I=0, A=0, D=1.0
	store := makeStore(
		[]string{"pkg/core"},
		map[string][]symbolDef{
			"pkg/core": {
				{name: "Foo", kind: facts.SymbolStruct},
				{name: "Bar", kind: facts.SymbolFunc},
			},
		},
		nil,
	)

	metrics := computeMetrics(store)
	m, ok := metrics["pkg/core"]
	if !ok {
		t.Fatal("expected metrics for pkg/core")
	}
	if m.Ca != 0 {
		t.Errorf("Ca = %d, want 0", m.Ca)
	}
	if m.Ce != 0 {
		t.Errorf("Ce = %d, want 0", m.Ce)
	}
	if !approxEqual(m.Instability, 0, 0.001) {
		t.Errorf("I = %f, want 0", m.Instability)
	}
	if !approxEqual(m.Abstractness, 0, 0.001) {
		t.Errorf("A = %f, want 0", m.Abstractness)
	}
	if !approxEqual(m.Distance, 1.0, 0.001) {
		t.Errorf("D = %f, want 1.0", m.Distance)
	}
}

func TestComputeMetrics_PureAbstractModule(t *testing.T) {
	// Module with only interfaces: A=1.0
	store := makeStore(
		[]string{"pkg/ports"},
		map[string][]symbolDef{
			"pkg/ports": {
				{name: "Reader", kind: facts.SymbolInterface},
				{name: "Writer", kind: facts.SymbolInterface},
			},
		},
		nil,
	)

	metrics := computeMetrics(store)
	m := metrics["pkg/ports"]
	if !approxEqual(m.Abstractness, 1.0, 0.001) {
		t.Errorf("A = %f, want 1.0", m.Abstractness)
	}
}

func TestComputeMetrics_AfferentAndEfferentCoupling(t *testing.T) {
	// A -> B -> C
	// Ca(B)=1 (A depends on B), Ce(B)=1 (B depends on C)
	// I(B) = 1/(1+1) = 0.5
	store := makeStore(
		[]string{"pkg/a", "pkg/b", "pkg/c"},
		map[string][]symbolDef{
			"pkg/a": {{name: "A", kind: facts.SymbolStruct}},
			"pkg/b": {{name: "B", kind: facts.SymbolStruct}},
			"pkg/c": {{name: "C", kind: facts.SymbolStruct}},
		},
		map[string][]string{
			"pkg/a": {"pkg/b"},
			"pkg/b": {"pkg/c"},
		},
	)

	metrics := computeMetrics(store)

	// pkg/b: Ca=1, Ce=1, I=0.5
	mb := metrics["pkg/b"]
	if mb.Ca != 1 {
		t.Errorf("pkg/b Ca = %d, want 1", mb.Ca)
	}
	if mb.Ce != 1 {
		t.Errorf("pkg/b Ce = %d, want 1", mb.Ce)
	}
	if !approxEqual(mb.Instability, 0.5, 0.001) {
		t.Errorf("pkg/b I = %f, want 0.5", mb.Instability)
	}

	// pkg/c: Ca=1, Ce=0, I=0.0 (stable)
	mc := metrics["pkg/c"]
	if mc.Ca != 1 {
		t.Errorf("pkg/c Ca = %d, want 1", mc.Ca)
	}
	if mc.Ce != 0 {
		t.Errorf("pkg/c Ce = %d, want 0", mc.Ce)
	}
	if !approxEqual(mc.Instability, 0.0, 0.001) {
		t.Errorf("pkg/c I = %f, want 0.0", mc.Instability)
	}

	// pkg/a: Ca=0, Ce=1, I=1.0 (unstable)
	ma := metrics["pkg/a"]
	if ma.Ca != 0 {
		t.Errorf("pkg/a Ca = %d, want 0", ma.Ca)
	}
	if ma.Ce != 1 {
		t.Errorf("pkg/a Ce = %d, want 1", ma.Ce)
	}
	if !approxEqual(ma.Instability, 1.0, 0.001) {
		t.Errorf("pkg/a I = %f, want 1.0", ma.Instability)
	}
}

func TestComputeMetrics_MixedAbstractness(t *testing.T) {
	// 1 interface + 1 struct = A=0.5
	store := makeStore(
		[]string{"pkg/mixed"},
		map[string][]symbolDef{
			"pkg/mixed": {
				{name: "Iface", kind: facts.SymbolInterface},
				{name: "Impl", kind: facts.SymbolStruct},
			},
		},
		nil,
	)

	metrics := computeMetrics(store)
	m := metrics["pkg/mixed"]
	if !approxEqual(m.Abstractness, 0.5, 0.001) {
		t.Errorf("A = %f, want 0.5", m.Abstractness)
	}
}

func TestComputeMetrics_Distance(t *testing.T) {
	// D = |A + I - 1|
	// Stable + concrete: A=0, I=0 -> D=1.0 (Zone of Pain)
	// Stable + abstract: A=1, I=0 -> D=0.0 (Main Sequence)
	// Unstable + concrete: A=0, I=1 -> D=0.0 (Main Sequence)
	// Unstable + abstract: A=1, I=1 -> D=1.0 (Zone of Uselessness)

	// Test zone of pain: stable, concrete, depended upon
	store := makeStore(
		[]string{"pkg/pain", "pkg/user"},
		map[string][]symbolDef{
			"pkg/pain": {{name: "Concrete", kind: facts.SymbolStruct}},
			"pkg/user": {{name: "User", kind: facts.SymbolStruct}},
		},
		map[string][]string{
			"pkg/user": {"pkg/pain"},
		},
	)

	metrics := computeMetrics(store)
	mp := metrics["pkg/pain"]
	// Ca=1, Ce=0 -> I=0, A=0 -> D=1.0
	if !approxEqual(mp.Distance, 1.0, 0.001) {
		t.Errorf("pkg/pain D = %f, want 1.0", mp.Distance)
	}
}

func TestComputeMetrics_ModuleWithNoSymbols(t *testing.T) {
	// Module with no symbols: A=0 (no abstract out of 0 total, default to 0)
	store := makeStore([]string{"pkg/empty"}, nil, nil)
	metrics := computeMetrics(store)
	m := metrics["pkg/empty"]
	if !approxEqual(m.Abstractness, 0, 0.001) {
		t.Errorf("A = %f, want 0", m.Abstractness)
	}
}

func TestComputeMetrics_ExternalDepsIgnored(t *testing.T) {
	// External deps (fmt, github.com/...) should not count as Ce
	store := makeStore(
		[]string{"pkg/a"},
		map[string][]symbolDef{
			"pkg/a": {{name: "A", kind: facts.SymbolStruct}},
		},
		nil, // no internal deps
	)
	// Add an external dependency manually
	store.Add(facts.Fact{
		Kind: facts.KindDependency,
		File: "pkg/a/file.go",
		Relations: []facts.Relation{
			{Kind: facts.RelImports, Target: "fmt"},
			{Kind: facts.RelImports, Target: "github.com/foo/bar"},
		},
	})

	metrics := computeMetrics(store)
	m := metrics["pkg/a"]
	if m.Ce != 0 {
		t.Errorf("Ce = %d, want 0 (externals should be ignored)", m.Ce)
	}
}

// --- Unit tests for classifyZone ---

func TestClassifyZone(t *testing.T) {
	tests := []struct {
		name string
		m    ModuleMetrics
		want string
	}{
		{
			name: "main sequence ideal stable abstract",
			m:    ModuleMetrics{Instability: 0.0, Abstractness: 1.0, Distance: 0.0},
			want: ZoneMainSequence,
		},
		{
			name: "main sequence ideal unstable concrete",
			m:    ModuleMetrics{Instability: 1.0, Abstractness: 0.0, Distance: 0.0},
			want: ZoneMainSequence,
		},
		{
			name: "zone of pain",
			m:    ModuleMetrics{Instability: 0.1, Abstractness: 0.1, Distance: 0.8},
			want: ZonePain,
		},
		{
			name: "zone of uselessness",
			m:    ModuleMetrics{Instability: 0.9, Abstractness: 0.9, Distance: 0.8},
			want: ZoneUselessness,
		},
		{
			name: "near main sequence",
			m:    ModuleMetrics{Instability: 0.5, Abstractness: 0.5, Distance: 0.0},
			want: ZoneMainSequence,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyZone(tt.m)
			if got != tt.want {
				t.Errorf("classifyZone(%+v) = %q, want %q", tt.m, got, tt.want)
			}
		})
	}
}

// --- Acceptance test for Explain ---

func TestExplain_Integration_ProducesInsights(t *testing.T) {
	// Setup: 3 modules with clear stability characteristics
	// pkg/core: depended on by everyone, has only interfaces -> stable, abstract (main sequence)
	// pkg/impl: depends on core, depended on by app -> mixed
	// pkg/app: depends on impl, not depended on -> unstable, concrete
	store := makeStore(
		[]string{"pkg/core", "pkg/impl", "pkg/app"},
		map[string][]symbolDef{
			"pkg/core": {
				{name: "Service", kind: facts.SymbolInterface},
				{name: "Repo", kind: facts.SymbolInterface},
			},
			"pkg/impl": {
				{name: "ServiceImpl", kind: facts.SymbolStruct},
				{name: "RepoImpl", kind: facts.SymbolStruct},
			},
			"pkg/app": {
				{name: "Handler", kind: facts.SymbolStruct},
				{name: "Run", kind: facts.SymbolFunc},
			},
		},
		map[string][]string{
			"pkg/app":  {"pkg/impl"},
			"pkg/impl": {"pkg/core"},
		},
	)

	e := New()
	if e.Name() != "stability" {
		t.Errorf("Name() = %q, want %q", e.Name(), "stability")
	}

	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	if len(insights) == 0 {
		t.Fatal("expected at least one insight")
	}

	// Should have a summary insight
	var foundSummary bool
	for _, ins := range insights {
		if strings.Contains(ins.Title, "Stability") || strings.Contains(ins.Title, "stability") {
			foundSummary = true
			// Should have evidence for each module
			if len(ins.Evidence) < 3 {
				t.Errorf("summary insight evidence count = %d, want >= 3", len(ins.Evidence))
			}
			break
		}
	}
	if !foundSummary {
		titles := make([]string, len(insights))
		for i, ins := range insights {
			titles[i] = ins.Title
		}
		t.Errorf("no stability summary insight found in: %v", titles)
	}
}

func TestExplain_Integration_ZonePainWarning(t *testing.T) {
	// Create a module that's stable (depended upon) and concrete (no interfaces)
	// = Zone of Pain
	store := makeStore(
		[]string{"pkg/pain", "pkg/user1", "pkg/user2"},
		map[string][]symbolDef{
			"pkg/pain":  {{name: "HardcodedThing", kind: facts.SymbolStruct}},
			"pkg/user1": {{name: "U1", kind: facts.SymbolStruct}},
			"pkg/user2": {{name: "U2", kind: facts.SymbolStruct}},
		},
		map[string][]string{
			"pkg/user1": {"pkg/pain"},
			"pkg/user2": {"pkg/pain"},
		},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	var foundPain bool
	for _, ins := range insights {
		if strings.Contains(ins.Title, "Pain") || strings.Contains(ins.Description, "pain") ||
			strings.Contains(ins.Description, "Pain") {
			foundPain = true
			break
		}
	}
	if !foundPain {
		titles := make([]string, len(insights))
		for i, ins := range insights {
			titles[i] = ins.Title
		}
		t.Errorf("no Zone of Pain warning found in: %v", titles)
	}
}

func TestExplain_Integration_ZoneUselessnessWarning(t *testing.T) {
	// Module that's unstable (deps outward, nobody depends on it) and abstract
	// = Zone of Uselessness
	store := makeStore(
		[]string{"pkg/useless", "pkg/target"},
		map[string][]symbolDef{
			"pkg/useless": {
				{name: "UnusedIface", kind: facts.SymbolInterface},
				{name: "AnotherIface", kind: facts.SymbolInterface},
			},
			"pkg/target": {{name: "T", kind: facts.SymbolStruct}},
		},
		map[string][]string{
			"pkg/useless": {"pkg/target"},
		},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	var foundUseless bool
	for _, ins := range insights {
		if strings.Contains(ins.Title, "Uselessness") || strings.Contains(ins.Description, "uselessness") ||
			strings.Contains(ins.Description, "Uselessness") {
			foundUseless = true
			break
		}
	}
	if !foundUseless {
		titles := make([]string, len(insights))
		for i, ins := range insights {
			titles[i] = ins.Title
		}
		t.Errorf("no Zone of Uselessness warning found in: %v", titles)
	}
}

func TestExplain_EmptyStore(t *testing.T) {
	store := facts.NewStore()
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights for empty store, got %d", len(insights))
	}
}

func TestExplain_SingleModule(t *testing.T) {
	store := makeStore(
		[]string{"pkg/only"},
		map[string][]symbolDef{
			"pkg/only": {{name: "X", kind: facts.SymbolStruct}},
		},
		nil,
	)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	// Single module with no deps should still produce a summary
	if len(insights) == 0 {
		t.Error("expected at least one insight for single module")
	}
}

// --- Unit test for buildDependencyGraph ---

func TestBuildDependencyGraph_FiltersExternals(t *testing.T) {
	store := makeStore(
		[]string{"pkg/a", "pkg/b"},
		nil,
		map[string][]string{
			"pkg/a": {"pkg/b"},
		},
	)
	// Add external dep
	store.Add(facts.Fact{
		Kind: facts.KindDependency,
		File: "pkg/a/ext.go",
		Relations: []facts.Relation{
			{Kind: facts.RelImports, Target: "github.com/external/lib"},
		},
	})

	graph := buildDependencyGraph(store)
	edges := graph["pkg/a"]
	for _, e := range edges {
		if strings.Contains(e, "github.com") {
			t.Errorf("external dep leaked into graph: %s", e)
		}
	}
	if len(edges) != 1 || edges[0] != "pkg/b" {
		t.Errorf("edges = %v, want [pkg/b]", edges)
	}
}

// --- Determinism test ---

func TestExplain_Deterministic(t *testing.T) {
	store := makeStore(
		[]string{"pkg/a", "pkg/b", "pkg/c"},
		map[string][]symbolDef{
			"pkg/a": {{name: "A", kind: facts.SymbolInterface}},
			"pkg/b": {{name: "B", kind: facts.SymbolStruct}},
			"pkg/c": {{name: "C", kind: facts.SymbolFunc}},
		},
		map[string][]string{
			"pkg/a": {"pkg/b"},
			"pkg/b": {"pkg/c"},
		},
	)

	e := New()
	insights1, _ := e.Explain(context.Background(), store)
	insights2, _ := e.Explain(context.Background(), store)

	if len(insights1) != len(insights2) {
		t.Fatalf("non-deterministic: %d vs %d insights", len(insights1), len(insights2))
	}

	titles1 := make([]string, len(insights1))
	titles2 := make([]string, len(insights2))
	for i := range insights1 {
		titles1[i] = insights1[i].Title
		titles2[i] = insights2[i].Title
	}
	sort.Strings(titles1)
	sort.Strings(titles2)
	for i := range titles1 {
		if titles1[i] != titles2[i] {
			t.Errorf("non-deterministic titles: %v vs %v", titles1, titles2)
			break
		}
	}
}
