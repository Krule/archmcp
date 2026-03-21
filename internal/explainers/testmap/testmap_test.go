package testmap

import (
	"context"
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

// --- Unit: isTestFile ---

func TestIsTestFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// Go
		{"src/auth/auth_test.go", true},
		{"auth_test.go", true},
		{"src/auth/auth.go", false},

		// Python
		{"tests/test_auth.py", true},
		{"test_main.py", true},
		{"src/auth.py", false},

		// TypeScript/JavaScript
		{"src/auth.spec.ts", true},
		{"src/auth.test.ts", true},
		{"src/auth.spec.js", true},
		{"src/auth.test.js", true},
		{"src/auth.test.tsx", true},
		{"src/auth.spec.jsx", true},
		{"src/auth.ts", false},

		// Edge cases
		{"", false},
		{"_test.go", true},
		{"test_.py", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isTestFile(tt.path); got != tt.want {
				t.Errorf("isTestFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// --- Unit: fileDir ---

func TestFileDir(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"src/auth/auth.go", "src/auth"},
		{"auth.go", "."},
		{"a/b/c/d.go", "a/b/c"},
		{"", "."},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := fileDir(tt.path); got != tt.want {
				t.Errorf("fileDir(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// --- Unit: collectTestFiles ---

func TestCollectTestFiles(t *testing.T) {
	store := makeStore(
		facts.Fact{Kind: facts.KindModule, Name: "src/auth", File: "src/auth/auth.go"},
		facts.Fact{Kind: facts.KindModule, Name: "src/auth", File: "src/auth/auth_test.go"},
		facts.Fact{Kind: facts.KindSymbol, Name: "Foo", File: "src/auth/foo_test.go"},
		facts.Fact{Kind: facts.KindDependency, File: "src/db/test_db.py"},
	)

	testFiles := collectTestFiles(store)
	// Should find: auth_test.go, foo_test.go, test_db.py
	if len(testFiles) != 3 {
		t.Errorf("expected 3 test files, got %d: %v", len(testFiles), testFiles)
	}
}

// --- Unit: mapTestsToModules ---

func TestMapTestsToModules(t *testing.T) {
	modules := []facts.Fact{
		{Kind: facts.KindModule, Name: "src/auth"},
		{Kind: facts.KindModule, Name: "src/db"},
	}
	testFiles := map[string]bool{
		"src/auth/auth_test.go":  true,
		"src/auth/login_test.go": true,
		"src/db/db_test.go":      true,
	}

	mapping := mapTestsToModules(modules, testFiles)

	if tests, ok := mapping["src/auth"]; !ok || len(tests) != 2 {
		t.Errorf("src/auth should have 2 test files, got %v", mapping["src/auth"])
	}
	if tests, ok := mapping["src/db"]; !ok || len(tests) != 1 {
		t.Errorf("src/db should have 1 test file, got %v", mapping["src/db"])
	}
}

// --- Unit: countSymbolsByModule ---

func TestCountSymbolsByModule(t *testing.T) {
	symbols := []facts.Fact{
		{Kind: facts.KindSymbol, Name: "Foo", File: "src/auth/auth.go"},
		{Kind: facts.KindSymbol, Name: "Bar", File: "src/auth/bar.go"},
		{Kind: facts.KindSymbol, Name: "Baz", File: "src/db/db.go"},
		// Test file symbols should be excluded.
		{Kind: facts.KindSymbol, Name: "TestFoo", File: "src/auth/auth_test.go"},
		// No file -> skipped.
		{Kind: facts.KindSymbol, Name: "Orphan"},
	}

	counts := countSymbolsByModule(symbols)
	if counts["src/auth"] != 2 {
		t.Errorf("src/auth symbols = %d, want 2", counts["src/auth"])
	}
	if counts["src/db"] != 1 {
		t.Errorf("src/db symbols = %d, want 1", counts["src/db"])
	}
	if _, ok := counts["src/auth_test"]; ok {
		t.Error("test file symbols should not be counted")
	}
}

// --- Edge: empty store ---

func TestExplain_EmptyStore(t *testing.T) {
	store := makeStore()
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights for empty store, got %d", len(insights))
	}
}

// --- Edge: all modules tested ---

func TestExplain_AllTested(t *testing.T) {
	store := makeStore(
		facts.Fact{Kind: facts.KindModule, Name: "src/a"},
		facts.Fact{Kind: facts.KindModule, Name: "src/b"},
		facts.Fact{Kind: facts.KindModule, Name: "src/a", File: "src/a/a_test.go"},
		facts.Fact{Kind: facts.KindModule, Name: "src/b", File: "src/b/b_test.go"},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	// Should have summary but no untested insight.
	for _, ins := range insights {
		if strings.Contains(ins.Title, "Untested") {
			t.Error("should not report untested modules when all are tested")
		}
	}

	// Summary should say 2 of 2.
	found := false
	for _, ins := range insights {
		if strings.Contains(ins.Title, "coverage") {
			found = true
			if !strings.Contains(ins.Description, "2 of 2") {
				t.Errorf("summary should say 2 of 2, got: %s", ins.Description)
			}
		}
	}
	if !found {
		t.Error("missing test coverage summary")
	}
}

// --- Edge: TypeScript spec files ---

func TestExplain_TypeScriptSpecs(t *testing.T) {
	store := makeStore(
		facts.Fact{Kind: facts.KindModule, Name: "src/components"},
		facts.Fact{Kind: facts.KindSymbol, Name: "Button", File: "src/components/Button.tsx"},
		facts.Fact{Kind: facts.KindModule, Name: "src/components", File: "src/components/Button.spec.tsx"},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	// No untested modules.
	for _, ins := range insights {
		if strings.Contains(ins.Title, "Untested") {
			t.Error("src/components has a spec file, should not be untested")
		}
	}
}

// --- Edge: implements Explainer interface ---

func TestExplainer_Interface(t *testing.T) {
	// Compile-time check that TestMapExplainer satisfies Explainer.
	var _ interface {
		Name() string
		Explain(context.Context, *facts.Store) ([]facts.Insight, error)
	} = New()
}

// --- Acceptance test: full Explain integration ---

func TestExplain_Integration(t *testing.T) {
	// Setup: 3 modules, 2 have tests, 1 untested.
	store := makeStore(
		// Modules
		facts.Fact{Kind: facts.KindModule, Name: "src/auth"},
		facts.Fact{Kind: facts.KindModule, Name: "src/db"},
		facts.Fact{Kind: facts.KindModule, Name: "src/cache"},

		// Test files — auth has 2 test files, db has 1, cache has 0
		facts.Fact{Kind: facts.KindModule, Name: "src/auth", File: "src/auth/auth_test.go"},
		facts.Fact{Kind: facts.KindModule, Name: "src/auth", File: "src/auth/login_test.go"},
		facts.Fact{Kind: facts.KindModule, Name: "src/db", File: "src/db/db_test.go"},

		// Symbols in modules (to measure density)
		facts.Fact{Kind: facts.KindSymbol, Name: "Authenticate", File: "src/auth/auth.go",
			Props: map[string]any{"symbol_kind": facts.SymbolFunc}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Login", File: "src/auth/login.go",
			Props: map[string]any{"symbol_kind": facts.SymbolFunc}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Query", File: "src/db/db.go",
			Props: map[string]any{"symbol_kind": facts.SymbolFunc}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Connect", File: "src/db/db.go",
			Props: map[string]any{"symbol_kind": facts.SymbolFunc}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Get", File: "src/cache/cache.go",
			Props: map[string]any{"symbol_kind": facts.SymbolFunc}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Set", File: "src/cache/cache.go",
			Props: map[string]any{"symbol_kind": facts.SymbolFunc}},

		// Import relations from test -> module (for import-based mapping)
		facts.Fact{Kind: facts.KindDependency, File: "src/auth/auth_test.go",
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "src/auth"}}},
		facts.Fact{Kind: facts.KindDependency, File: "src/db/db_test.go",
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "src/db"}}},
	)

	e := New()
	if e.Name() != "testmap" {
		t.Fatalf("Name() = %q, want %q", e.Name(), "testmap")
	}

	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	// We expect insights about:
	// 1. Untested modules (cache)
	// 2. Test coverage summary
	if len(insights) < 2 {
		t.Fatalf("expected at least 2 insights, got %d: %+v", len(insights), insights)
	}

	// Find untested module insight
	var untestedInsight *facts.Insight
	var summaryInsight *facts.Insight
	for i := range insights {
		if strings.Contains(insights[i].Title, "untested") || strings.Contains(insights[i].Title, "Untested") {
			untestedInsight = &insights[i]
		}
		if strings.Contains(insights[i].Title, "coverage") || strings.Contains(insights[i].Title, "Test coverage") {
			summaryInsight = &insights[i]
		}
	}

	if untestedInsight == nil {
		t.Error("missing insight about untested modules")
	} else {
		// Should mention cache as untested
		found := false
		for _, ev := range untestedInsight.Evidence {
			if strings.Contains(ev.Fact, "cache") || strings.Contains(ev.Detail, "cache") {
				found = true
			}
		}
		if !found {
			t.Error("untested insight should reference src/cache")
		}
		if len(untestedInsight.Actions) == 0 {
			t.Error("untested insight should suggest actions")
		}
	}

	if summaryInsight == nil {
		t.Error("missing test coverage summary insight")
	} else {
		// 2 of 3 modules tested = ~67%
		if !strings.Contains(summaryInsight.Description, "2") || !strings.Contains(summaryInsight.Description, "3") {
			t.Errorf("summary should mention 2 of 3 modules, got: %s", summaryInsight.Description)
		}
	}
}
