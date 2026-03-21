package apisurface

import (
	"context"
	"sort"
	"testing"

	"github.com/dejo1307/archmcp/internal/facts"
)

// --- helpers ---

func makeStore(ff ...facts.Fact) *facts.Store {
	s := facts.NewStore()
	s.Add(ff...)
	return s
}

func sym(name, symbolKind, file string, exported bool, rels ...facts.Relation) facts.Fact {
	return facts.Fact{
		Kind: facts.KindSymbol,
		Name: name,
		File: file,
		Props: map[string]any{
			"symbol_kind": symbolKind,
			"exported":    exported,
		},
		Relations: rels,
	}
}

func mod(name, file string, rels ...facts.Relation) facts.Fact {
	return facts.Fact{
		Kind:      facts.KindModule,
		Name:      name,
		File:      file,
		Relations: rels,
	}
}

func rel(kind, target string) facts.Relation {
	return facts.Relation{Kind: kind, Target: target}
}

// evidenceSymbols extracts symbol names from evidence, sorted.
func evidenceSymbols(insights []facts.Insight) []string {
	var names []string
	for _, ins := range insights {
		for _, ev := range ins.Evidence {
			if ev.Symbol != "" {
				names = append(names, ev.Symbol)
			}
		}
	}
	sort.Strings(names)
	return names
}

// --- Acceptance test ---

// TestAcceptance_APISurface verifies the full explainer behavior:
// Given a store with exported symbols that have varying external consumer counts,
// it should classify them as over-exposed (0 refs), narrow (1 ref), or healthy (2+),
// excluding main/test/trait impls, and report per-module API efficiency.
func TestAcceptance_APISurface(t *testing.T) {
	store := makeStore(
		// Module declarations
		mod("pkg/auth", "pkg/auth/auth.go",
			rel(facts.RelDeclares, "Authenticate"),
			rel(facts.RelDeclares, "Authorize"),
			rel(facts.RelDeclares, "HashPassword"),
		),
		mod("pkg/server", "pkg/server/server.go",
			rel(facts.RelDeclares, "ListenAndServe"),
			rel(facts.RelDeclares, "NewRouter"),
		),
		mod("cmd/app", "cmd/app/main.go"),

		// pkg/auth exported symbols
		sym("Authenticate", facts.SymbolFunc, "pkg/auth/auth.go", true), // called by 2 modules => healthy
		sym("Authorize", facts.SymbolFunc, "pkg/auth/auth.go", true),    // called by 1 module => narrow
		sym("HashPassword", facts.SymbolFunc, "pkg/auth/auth.go", true), // called by 0 modules => over-exposed

		// pkg/server exported symbols
		sym("ListenAndServe", facts.SymbolFunc, "pkg/server/server.go", true), // called by 1 module => narrow
		sym("NewRouter", facts.SymbolFunc, "pkg/server/server.go", true),      // called by 0 modules => over-exposed

		// Excluded: main function
		sym("main", facts.SymbolFunc, "cmd/app/main.go", false,
			rel(facts.RelCalls, "Authenticate"),
			rel(facts.RelCalls, "Authorize"),
			rel(facts.RelCalls, "ListenAndServe"),
		),

		// Excluded: test function (exported but test)
		sym("TestAuth", facts.SymbolFunc, "pkg/auth/auth_test.go", false,
			rel(facts.RelCalls, "Authenticate"),
		),

		// Excluded: interface impl
		sym("ServeHTTP", facts.SymbolMethod, "pkg/server/handler.go", true,
			rel(facts.RelImplements, "http.Handler"),
		),

		// Unexported symbol — not part of API surface
		sym("helperFunc", facts.SymbolFunc, "pkg/auth/auth.go", false),

		// Cross-module call: pkg/server calls Authenticate
		sym("handleLogin", facts.SymbolFunc, "pkg/server/login.go", false,
			rel(facts.RelCalls, "Authenticate"),
		),
	)

	e := New()
	if e.Name() != "apisurface" {
		t.Fatalf("Name() = %q, want %q", e.Name(), "apisurface")
	}

	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	// Should produce at least one insight about over-exposed symbols
	if len(insights) == 0 {
		t.Fatal("expected at least one insight, got 0")
	}

	// Find the over-exposed insight
	var overExposedInsight *facts.Insight
	var summaryInsight *facts.Insight
	for i := range insights {
		if containsStr(insights[i].Title, "over-exposed") {
			overExposedInsight = &insights[i]
		}
		if containsStr(insights[i].Title, "efficiency") || containsStr(insights[i].Title, "surface") {
			summaryInsight = &insights[i]
		}
	}

	if overExposedInsight == nil {
		t.Fatal("expected an insight about over-exposed symbols")
	}

	// Over-exposed symbols: HashPassword, NewRouter
	overExposedNames := evidenceSymbols([]facts.Insight{*overExposedInsight})
	wantOverExposed := []string{"HashPassword", "NewRouter"}
	if len(overExposedNames) != len(wantOverExposed) {
		t.Fatalf("over-exposed = %v, want %v", overExposedNames, wantOverExposed)
	}
	for i := range overExposedNames {
		if overExposedNames[i] != wantOverExposed[i] {
			t.Errorf("over-exposed[%d] = %q, want %q", i, overExposedNames[i], wantOverExposed[i])
		}
	}

	// Summary insight should exist with per-module efficiency
	if summaryInsight == nil {
		t.Fatal("expected a summary/efficiency insight")
	}

	// Check confidence is reasonable
	if overExposedInsight.Confidence < 0.5 || overExposedInsight.Confidence > 1.0 {
		t.Errorf("confidence = %f, want between 0.5 and 1.0", overExposedInsight.Confidence)
	}

	// Check suggested actions exist
	if len(overExposedInsight.Actions) == 0 {
		t.Error("expected at least one suggested action")
	}
}

// --- Unit tests ---

func TestName(t *testing.T) {
	e := New()
	if got := e.Name(); got != "apisurface" {
		t.Errorf("Name() = %q, want %q", got, "apisurface")
	}
}

func TestExplain_EmptyStore(t *testing.T) {
	e := New()
	insights, err := e.Explain(context.Background(), facts.NewStore())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights for empty store, got %d", len(insights))
	}
}

func TestExplain_NoExportedSymbols(t *testing.T) {
	store := makeStore(
		sym("helper", facts.SymbolFunc, "pkg/a.go", false),
		sym("internal", facts.SymbolFunc, "pkg/b.go", false),
	)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights (no exported symbols), got %d", len(insights))
	}
}

func TestExplain_AllHealthy(t *testing.T) {
	// Exported symbol called by 2+ external modules => healthy, no over-exposed insight
	store := makeStore(
		sym("Foo", facts.SymbolFunc, "pkg/a/a.go", true),
		sym("callerA", facts.SymbolFunc, "pkg/b/b.go", false,
			rel(facts.RelCalls, "Foo")),
		sym("callerB", facts.SymbolFunc, "pkg/c/c.go", false,
			rel(facts.RelCalls, "Foo")),
	)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	// No over-exposed insight
	for _, ins := range insights {
		if containsStr(ins.Title, "over-exposed") {
			t.Errorf("expected no over-exposed insight, got: %s", ins.Title)
		}
	}
}

func TestExplain_OverExposed(t *testing.T) {
	// Exported symbol with 0 external consumers
	store := makeStore(
		sym("Unused", facts.SymbolFunc, "pkg/a/a.go", true),
	)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	names := evidenceSymbols(insights)
	if len(names) != 1 || names[0] != "Unused" {
		t.Errorf("over-exposed = %v, want [Unused]", names)
	}
}

func TestExplain_ExcludesMainFunc(t *testing.T) {
	// main is excluded even though exported-looking
	store := makeStore(
		sym("main", facts.SymbolFunc, "cmd/main.go", false),
	)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights (main excluded), got %d", len(insights))
	}
}

func TestExplain_ExcludesTestFunctions(t *testing.T) {
	store := makeStore(
		sym("TestFoo", facts.SymbolFunc, "pkg/foo_test.go", true),
		sym("BenchmarkBar", facts.SymbolFunc, "pkg/bar_test.go", true),
		sym("ExampleBaz", facts.SymbolFunc, "pkg/baz_test.go", true),
	)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights (test funcs excluded), got %d", len(insights))
	}
}

func TestExplain_ExcludesInterfaceImpls(t *testing.T) {
	store := makeStore(
		sym("ServeHTTP", facts.SymbolMethod, "pkg/server.go", true,
			rel(facts.RelImplements, "http.Handler")),
	)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights (interface impls excluded), got %d", len(insights))
	}
}

func TestExplain_ExcludesUnexported(t *testing.T) {
	// Unexported symbols are not part of API surface
	store := makeStore(
		sym("helper", facts.SymbolFunc, "pkg/a.go", false),
	)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights (unexported excluded), got %d", len(insights))
	}
}

func TestClassifySymbol(t *testing.T) {
	tests := []struct {
		name     string
		refs     int
		wantKind string
	}{
		{"zero refs", 0, "over-exposed"},
		{"one ref", 1, "narrow"},
		{"two refs", 2, "healthy"},
		{"many refs", 10, "healthy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classify(tt.refs)
			if got != tt.wantKind {
				t.Errorf("classify(%d) = %q, want %q", tt.refs, got, tt.wantKind)
			}
		})
	}
}

func TestCountExternalConsumers(t *testing.T) {
	// "Foo" is in pkg/a/a.go. Callers from pkg/b and pkg/c are external.
	// A caller from pkg/a is same-module (not external).
	symbols := []facts.Fact{
		sym("Foo", facts.SymbolFunc, "pkg/a/a.go", true),
		sym("sameModule", facts.SymbolFunc, "pkg/a/helper.go", false,
			rel(facts.RelCalls, "Foo")),
		sym("externalA", facts.SymbolFunc, "pkg/b/b.go", false,
			rel(facts.RelCalls, "Foo")),
		sym("externalB", facts.SymbolFunc, "pkg/c/c.go", false,
			rel(facts.RelCalls, "Foo")),
	}
	got := countExternalConsumers("Foo", "pkg/a", symbols)
	if got != 2 {
		t.Errorf("countExternalConsumers = %d, want 2", got)
	}
}

func TestModuleFromFile(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"pkg/auth/auth.go", "pkg/auth"},
		{"cmd/main.go", "cmd"},
		{"main.go", "."},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got := moduleFromFile(tt.file)
			if got != tt.want {
				t.Errorf("moduleFromFile(%q) = %q, want %q", tt.file, got, tt.want)
			}
		})
	}
}

func TestExplain_NarrowSymbol(t *testing.T) {
	// Symbol called by exactly 1 external module => narrow
	store := makeStore(
		sym("NarrowFunc", facts.SymbolFunc, "pkg/a/a.go", true),
		sym("caller", facts.SymbolFunc, "pkg/b/b.go", false,
			rel(facts.RelCalls, "NarrowFunc")),
	)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	// Should not have over-exposed insight for narrow symbols
	for _, ins := range insights {
		names := evidenceSymbols([]facts.Insight{ins})
		for _, n := range names {
			if n == "NarrowFunc" && containsStr(ins.Title, "over-exposed") {
				t.Errorf("NarrowFunc should not be in over-exposed insight")
			}
		}
	}
}

func TestExplain_SameModuleCallNotCounted(t *testing.T) {
	// Only same-module callers should count as 0 external consumers
	store := makeStore(
		sym("InternalOnly", facts.SymbolFunc, "pkg/a/a.go", true),
		sym("sameModCaller", facts.SymbolFunc, "pkg/a/b.go", false,
			rel(facts.RelCalls, "InternalOnly")),
	)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	names := evidenceSymbols(insights)
	found := false
	for _, n := range names {
		if n == "InternalOnly" {
			found = true
		}
	}
	if !found {
		t.Error("InternalOnly should be over-exposed (only same-module caller)")
	}
}

func TestExplain_PerModuleEfficiency(t *testing.T) {
	// 2 modules, each with exported symbols at varying usage levels
	store := makeStore(
		// Module A: 2 symbols, 1 over-exposed, 1 healthy => 50% efficiency
		sym("UsedA", facts.SymbolFunc, "pkg/a/a.go", true),
		sym("UnusedA", facts.SymbolFunc, "pkg/a/a.go", true),
		sym("c1", facts.SymbolFunc, "pkg/b/b.go", false,
			rel(facts.RelCalls, "UsedA")),
		sym("c2", facts.SymbolFunc, "pkg/c/c.go", false,
			rel(facts.RelCalls, "UsedA")),

		// Module B: 1 symbol, over-exposed => 0% efficiency
		sym("UnusedB", facts.SymbolFunc, "pkg/d/d.go", true),
	)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	// Should have a summary insight
	var found bool
	for _, ins := range insights {
		if containsStr(ins.Title, "surface") || containsStr(ins.Title, "efficiency") {
			found = true
			// Should have per-module evidence
			if len(ins.Evidence) == 0 {
				t.Error("summary insight should have per-module evidence")
			}
		}
	}
	if !found {
		t.Error("expected a summary/efficiency insight")
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && strContains(s, substr))
}

func strContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
