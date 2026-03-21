package deadcode

import (
	"context"
	"sort"
	"testing"

	"github.com/dejo1307/archmcp/internal/facts"
)

// --- helpers ---

// makeStore builds a fact store with symbols and their relations for testing.
func makeStore(symbols []facts.Fact) *facts.Store {
	s := facts.NewStore()
	for _, sym := range symbols {
		s.Add(sym)
	}
	return s
}

func sym(name, symbolKind, file string, exported bool, rels ...facts.Relation) facts.Fact {
	props := map[string]any{
		"symbol_kind": symbolKind,
		"exported":    exported,
	}
	return facts.Fact{
		Kind:      facts.KindSymbol,
		Name:      name,
		File:      file,
		Props:     props,
		Relations: rels,
	}
}

func rel(kind, target string) facts.Relation {
	return facts.Relation{Kind: kind, Target: target}
}

// insightSymbols extracts the dead symbol names from insights' evidence.
func insightSymbols(insights []facts.Insight) []string {
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

// --- Unit tests ---

func TestName(t *testing.T) {
	e := New()
	if got := e.Name(); got != "deadcode" {
		t.Errorf("Name() = %q, want %q", got, "deadcode")
	}
}

func TestExplain_EmptyStore(t *testing.T) {
	e := New()
	store := facts.NewStore()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights for empty store, got %d", len(insights))
	}
}

func TestExplain_AllSymbolsCalled(t *testing.T) {
	// A calls B, B calls C — no dead code
	store := makeStore([]facts.Fact{
		sym("main", facts.SymbolFunc, "cmd/main.go", false,
			rel(facts.RelCalls, "A")),
		sym("A", facts.SymbolFunc, "pkg/a.go", false,
			rel(facts.RelCalls, "B")),
		sym("B", facts.SymbolFunc, "pkg/b.go", false,
			rel(facts.RelCalls, "C")),
		sym("C", facts.SymbolFunc, "pkg/c.go", false),
	})

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights (no dead code), got %d", len(insights))
	}
}

func TestExplain_DeadFunction(t *testing.T) {
	// A calls B. C is never called — dead code.
	store := makeStore([]facts.Fact{
		sym("main", facts.SymbolFunc, "cmd/main.go", false,
			rel(facts.RelCalls, "A")),
		sym("A", facts.SymbolFunc, "pkg/a.go", false,
			rel(facts.RelCalls, "B")),
		sym("B", facts.SymbolFunc, "pkg/b.go", false),
		sym("C", facts.SymbolFunc, "pkg/c.go", false), // dead
	})

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}

	names := insightSymbols(insights)
	if len(names) != 1 || names[0] != "C" {
		t.Errorf("dead symbols = %v, want [C]", names)
	}
}

func TestExplain_ExcludesMain(t *testing.T) {
	// main has 0 incoming calls but should be excluded
	store := makeStore([]facts.Fact{
		sym("main", facts.SymbolFunc, "cmd/main.go", false,
			rel(facts.RelCalls, "A")),
		sym("A", facts.SymbolFunc, "pkg/a.go", false),
	})

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights (main excluded), got %d", len(insights))
	}
}

func TestExplain_ExcludesInit(t *testing.T) {
	// init has 0 incoming calls but should be excluded
	store := makeStore([]facts.Fact{
		sym("init", facts.SymbolFunc, "pkg/setup.go", false),
	})

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights (init excluded), got %d", len(insights))
	}
}

func TestExplain_ExcludesExported(t *testing.T) {
	// Exported symbols should be excluded (may be used externally)
	store := makeStore([]facts.Fact{
		sym("Handler", facts.SymbolFunc, "pkg/handler.go", true),
		sym("ProcessRequest", facts.SymbolFunc, "pkg/process.go", true),
	})

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights (exported excluded), got %d", len(insights))
	}
}

func TestExplain_ExcludesTestFunctions(t *testing.T) {
	// Test functions should be excluded
	store := makeStore([]facts.Fact{
		sym("TestFoo", facts.SymbolFunc, "pkg/foo_test.go", false),
		sym("BenchmarkBar", facts.SymbolFunc, "pkg/bar_test.go", false),
		sym("ExampleBaz", facts.SymbolFunc, "pkg/baz_test.go", false),
	})

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
	// Symbols implementing an interface should be excluded
	store := makeStore([]facts.Fact{
		sym("ServeHTTP", facts.SymbolMethod, "pkg/server.go", false,
			rel(facts.RelImplements, "http.Handler")),
	})

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights (interface impls excluded), got %d", len(insights))
	}
}

func TestExplain_MultipleDeadSymbols(t *testing.T) {
	// Multiple dead symbols reported in one insight
	store := makeStore([]facts.Fact{
		sym("main", facts.SymbolFunc, "cmd/main.go", false,
			rel(facts.RelCalls, "A")),
		sym("A", facts.SymbolFunc, "pkg/a.go", false),
		sym("orphanHelper", facts.SymbolFunc, "pkg/helper.go", false), // dead
		sym("unusedCalc", facts.SymbolFunc, "pkg/calc.go", false),     // dead
		sym("staleParser", facts.SymbolFunc, "pkg/parser.go", false),  // dead
	})

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}

	names := insightSymbols(insights)
	want := []string{"orphanHelper", "staleParser", "unusedCalc"}
	if len(names) != len(want) {
		t.Fatalf("dead symbols = %v, want %v", names, want)
	}
	for i := range names {
		if names[i] != want[i] {
			t.Errorf("dead symbols[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

func TestExplain_InsightFields(t *testing.T) {
	store := makeStore([]facts.Fact{
		sym("orphan", facts.SymbolFunc, "pkg/orphan.go", false),
	})

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}

	ins := insights[0]
	if ins.Confidence < 0.5 || ins.Confidence > 1.0 {
		t.Errorf("confidence = %f, want between 0.5 and 1.0", ins.Confidence)
	}
	if len(ins.Actions) == 0 {
		t.Error("expected at least one suggested action")
	}
	if ins.Title == "" {
		t.Error("expected non-empty title")
	}
	if ins.Description == "" {
		t.Error("expected non-empty description")
	}
}

func TestExplain_MixedExclusionsAndDead(t *testing.T) {
	// Complex scenario: mix of excluded and dead symbols
	store := makeStore([]facts.Fact{
		sym("main", facts.SymbolFunc, "cmd/main.go", false,
			rel(facts.RelCalls, "run")),
		sym("init", facts.SymbolFunc, "cmd/main.go", false),
		sym("run", facts.SymbolFunc, "internal/app.go", false,
			rel(facts.RelCalls, "process")),
		sym("process", facts.SymbolFunc, "internal/processor.go", false),
		sym("PublicAPI", facts.SymbolFunc, "pkg/api.go", true),                 // exported
		sym("TestProcessor", facts.SymbolFunc, "internal/proc_test.go", false), // test
		sym("String", facts.SymbolMethod, "internal/types.go", false,
			rel(facts.RelImplements, "fmt.Stringer")), // interface impl
		sym("deadHelper", facts.SymbolFunc, "internal/helper.go", false), // dead
		sym("unusedUtil", facts.SymbolFunc, "internal/util.go", false),   // dead
	})

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}

	names := insightSymbols(insights)
	want := []string{"deadHelper", "unusedUtil"}
	if len(names) != len(want) {
		t.Fatalf("dead symbols = %v, want %v", names, want)
	}
	for i := range names {
		if names[i] != want[i] {
			t.Errorf("dead symbols[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

func TestExplain_CalledViaImport(t *testing.T) {
	// Symbol referenced via imports relation should not be dead
	store := makeStore([]facts.Fact{
		sym("main", facts.SymbolFunc, "cmd/main.go", false,
			rel(facts.RelImports, "helperFunc")),
		sym("helperFunc", facts.SymbolFunc, "pkg/helper.go", false),
	})

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights (helperFunc imported), got %d", len(insights))
	}
}

func TestExplain_StructsAndInterfaces(t *testing.T) {
	// Structs/interfaces with 0 incoming refs and not exported => dead
	store := makeStore([]facts.Fact{
		sym("main", facts.SymbolFunc, "cmd/main.go", false,
			rel(facts.RelCalls, "usedStruct")),
		sym("usedStruct", facts.SymbolStruct, "pkg/types.go", false),
		sym("deadStruct", facts.SymbolStruct, "pkg/types.go", false), // dead
	})

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	names := insightSymbols(insights)
	if len(names) != 1 || names[0] != "deadStruct" {
		t.Errorf("dead symbols = %v, want [deadStruct]", names)
	}
}
