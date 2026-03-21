package coverage

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/dejo1307/archmcp/internal/facts"
)

// --- helpers ---

func makeStore(ff ...facts.Fact) *facts.Store {
	s := facts.NewStore()
	s.Add(ff...)
	return s
}

func iface(name, file string) facts.Fact {
	return facts.Fact{
		Kind: facts.KindSymbol,
		Name: name,
		File: file,
		Props: map[string]any{
			"symbol_kind": facts.SymbolInterface,
		},
	}
}

func trait(name, file string) facts.Fact {
	return facts.Fact{
		Kind: facts.KindSymbol,
		Name: name,
		File: file,
		Props: map[string]any{
			"symbol_kind": "trait",
		},
	}
}

func class(name, file string, implements ...string) facts.Fact {
	rels := make([]facts.Relation, len(implements))
	for i, ifc := range implements {
		rels[i] = facts.Relation{Kind: facts.RelImplements, Target: ifc}
	}
	return facts.Fact{
		Kind: facts.KindSymbol,
		Name: name,
		File: file,
		Props: map[string]any{
			"symbol_kind": facts.SymbolClass,
		},
		Relations: rels,
	}
}

func structFact(name, file string, implements ...string) facts.Fact {
	rels := make([]facts.Relation, len(implements))
	for i, ifc := range implements {
		rels[i] = facts.Relation{Kind: facts.RelImplements, Target: ifc}
	}
	return facts.Fact{
		Kind: facts.KindSymbol,
		Name: name,
		File: file,
		Props: map[string]any{
			"symbol_kind": facts.SymbolStruct,
		},
		Relations: rels,
	}
}

func insightTitles(insights []facts.Insight) []string {
	titles := make([]string, len(insights))
	for i, ins := range insights {
		titles[i] = ins.Title
	}
	sort.Strings(titles)
	return titles
}

// --- Unit tests for internal helpers ---

func TestCollectTraits_FindsInterfacesAndTraits(t *testing.T) {
	store := makeStore(
		iface("Reader", "io/reader.go"),
		trait("Display", "fmt/display.rs"),
		structFact("MyStruct", "pkg/my.go"),
		class("MyClass", "pkg/my.ts"),
	)

	traits := collectTraits(store)

	if len(traits) != 2 {
		t.Fatalf("got %d traits, want 2", len(traits))
	}
	names := make(map[string]bool)
	for _, tr := range traits {
		names[tr.Name] = true
	}
	if !names["Reader"] {
		t.Error("missing Reader interface")
	}
	if !names["Display"] {
		t.Error("missing Display trait")
	}
}

func TestCollectImplementors_FindsImplementsRelations(t *testing.T) {
	store := makeStore(
		iface("Reader", "io/reader.go"),
		iface("Writer", "io/writer.go"),
		structFact("FileReader", "pkg/file.go", "Reader"),
		structFact("BufReader", "pkg/buf.go", "Reader"),
		class("NetWriter", "pkg/net.ts", "Writer"),
	)

	implMap := collectImplementors(store)

	if len(implMap["Reader"]) != 2 {
		t.Errorf("Reader implementors = %d, want 2", len(implMap["Reader"]))
	}
	if len(implMap["Writer"]) != 1 {
		t.Errorf("Writer implementors = %d, want 1", len(implMap["Writer"]))
	}
}

func TestCollectImplementors_NoRelations(t *testing.T) {
	store := makeStore(
		iface("Empty", "pkg/empty.go"),
		structFact("Standalone", "pkg/standalone.go"),
	)

	implMap := collectImplementors(store)
	if len(implMap["Empty"]) != 0 {
		t.Errorf("Empty should have 0 implementors, got %d", len(implMap["Empty"]))
	}
}

// --- Integration / Acceptance tests for Explain ---

func TestExplain_EmptyStore(t *testing.T) {
	store := makeStore()
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("empty store should produce 0 insights, got %d", len(insights))
	}
}

func TestExplain_NoTraits(t *testing.T) {
	store := makeStore(
		structFact("Foo", "pkg/foo.go"),
		class("Bar", "pkg/bar.ts"),
	)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("no traits should produce 0 insights, got %d", len(insights))
	}
}

func TestExplain_OrphanTrait(t *testing.T) {
	store := makeStore(
		iface("Serializer", "pkg/serial.go"),
		iface("Logger", "pkg/log.go"),
		structFact("FileLogger", "pkg/file_log.go", "Logger"),
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	// Should flag Serializer as orphan
	var foundOrphan bool
	for _, ins := range insights {
		if strings.Contains(ins.Title, "orphan") || strings.Contains(ins.Title, "Orphan") {
			foundOrphan = true
			// Verify evidence points to Serializer
			hasSerializer := false
			for _, ev := range ins.Evidence {
				if ev.Symbol == "Serializer" || ev.Fact == "Serializer" {
					hasSerializer = true
				}
			}
			if !hasSerializer {
				t.Error("orphan insight should reference Serializer in evidence")
			}
		}
	}
	if !foundOrphan {
		t.Errorf("expected orphan trait insight for Serializer, got insights: %v", insightTitles(insights))
	}
}

func TestExplain_AllImplemented(t *testing.T) {
	store := makeStore(
		iface("Reader", "io/reader.go"),
		iface("Writer", "io/writer.go"),
		structFact("FileReader", "pkg/file.go", "Reader"),
		class("NetWriter", "pkg/net.ts", "Writer"),
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	// No orphan insights
	for _, ins := range insights {
		if strings.Contains(ins.Title, "orphan") || strings.Contains(ins.Title, "Orphan") {
			t.Errorf("should not flag orphan when all traits are implemented, got: %s", ins.Title)
		}
	}
}

func TestExplain_ImplementationDensity(t *testing.T) {
	store := makeStore(
		iface("Handler", "pkg/handler.go"),
		iface("Middleware", "pkg/mw.go"),
		structFact("AuthHandler", "pkg/auth.go", "Handler"),
		structFact("LogHandler", "pkg/log.go", "Handler"),
		structFact("CacheHandler", "pkg/cache.go", "Handler"),
		structFact("AuthMW", "pkg/auth_mw.go", "Middleware"),
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	// Should produce a density/summary insight
	var foundDensity bool
	for _, ins := range insights {
		if strings.Contains(ins.Title, "density") || strings.Contains(ins.Title, "coverage") || strings.Contains(ins.Title, "Coverage") {
			foundDensity = true
			if ins.Confidence <= 0 || ins.Confidence > 1.0 {
				t.Errorf("density insight confidence out of range: %f", ins.Confidence)
			}
		}
	}
	if !foundDensity {
		t.Errorf("expected density/coverage insight, got: %v", insightTitles(insights))
	}
}

func TestExplain_MultipleOrphans(t *testing.T) {
	store := makeStore(
		iface("A", "pkg/a.go"),
		iface("B", "pkg/b.go"),
		iface("C", "pkg/c.go"),
		// No implementors for any
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	// Should flag all three as orphans (could be single insight or multiple)
	orphanEvidence := make(map[string]bool)
	for _, ins := range insights {
		if strings.Contains(ins.Title, "orphan") || strings.Contains(ins.Title, "Orphan") {
			for _, ev := range ins.Evidence {
				if ev.Symbol != "" {
					orphanEvidence[ev.Symbol] = true
				}
			}
		}
	}
	for _, name := range []string{"A", "B", "C"} {
		if !orphanEvidence[name] {
			t.Errorf("missing orphan evidence for %s", name)
		}
	}
}

func TestExplain_Name(t *testing.T) {
	e := New()
	if e.Name() != "coverage" {
		t.Errorf("Name() = %q, want %q", e.Name(), "coverage")
	}
}

func TestExplain_ConfidenceValues(t *testing.T) {
	store := makeStore(
		iface("Orphan", "pkg/orphan.go"),
		iface("Implemented", "pkg/impl.go"),
		structFact("Impl", "pkg/impl_struct.go", "Implemented"),
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	for _, ins := range insights {
		if ins.Confidence < 0 || ins.Confidence > 1.0 {
			t.Errorf("insight %q has confidence out of [0,1]: %f", ins.Title, ins.Confidence)
		}
	}
}

func TestExplain_SuggestedActions(t *testing.T) {
	store := makeStore(
		iface("Unused", "pkg/unused.go"),
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	for _, ins := range insights {
		if strings.Contains(ins.Title, "orphan") || strings.Contains(ins.Title, "Orphan") {
			if len(ins.Actions) == 0 {
				t.Error("orphan insights should suggest actions")
			}
		}
	}
}

func TestExplain_TraitKindVariants(t *testing.T) {
	// Ensure both "interface" and "trait" symbol_kinds are detected
	store := makeStore(
		iface("GoInterface", "pkg/go.go"),
		trait("RustTrait", "pkg/rust.rs"),
		// Neither implemented
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	orphanSymbols := make(map[string]bool)
	for _, ins := range insights {
		for _, ev := range ins.Evidence {
			if ev.Symbol != "" {
				orphanSymbols[ev.Symbol] = true
			}
		}
	}
	if !orphanSymbols["GoInterface"] {
		t.Error("GoInterface should be flagged as orphan")
	}
	if !orphanSymbols["RustTrait"] {
		t.Error("RustTrait should be flagged as orphan")
	}
}
