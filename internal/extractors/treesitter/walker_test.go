package treesitter

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
	typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// parseTS is a test helper that parses TypeScript source and returns the root node.
// The caller must close the returned tree.
func parseTS(t *testing.T, src string) (*sitter.Tree, *sitter.Node) {
	t.Helper()
	parser := sitter.NewParser()
	t.Cleanup(func() { parser.Close() })
	parser.SetLanguage(sitter.NewLanguage(typescript.LanguageTypescript()))

	tree := parser.Parse([]byte(src), nil)
	t.Cleanup(func() { tree.Close() })
	return tree, tree.RootNode()
}

// ---------------------------------------------------------------------------
// Parse
// ---------------------------------------------------------------------------

func TestParse_validSource(t *testing.T) {
	src := []byte(`const x = 1;`)
	lang := sitter.NewLanguage(typescript.LanguageTypescript())

	tree, err := Parse(src, lang)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	defer tree.Close()

	root := tree.RootNode()
	if root == nil {
		t.Fatal("Parse returned nil root node")
	}
	if root.ChildCount() == 0 {
		t.Fatal("expected at least one child node")
	}
}

func TestParse_emptySource(t *testing.T) {
	lang := sitter.NewLanguage(typescript.LanguageTypescript())

	tree, err := Parse([]byte{}, lang)
	if err != nil {
		t.Fatalf("Parse returned error for empty source: %v", err)
	}
	defer tree.Close()

	root := tree.RootNode()
	if root == nil {
		t.Fatal("Parse returned nil root for empty source")
	}
}

func TestParse_nilLanguage(t *testing.T) {
	_, err := Parse([]byte(`const x = 1;`), nil)
	if err == nil {
		t.Fatal("expected error for nil language")
	}
}

// ---------------------------------------------------------------------------
// NodeText
// ---------------------------------------------------------------------------

func TestNodeText(t *testing.T) {
	src := []byte(`const greeting = "hello";`)
	_, root := parseTS(t, string(src))

	// root > lexical_declaration > variable_declarator > identifier("greeting")
	decl := root.Child(0) // lexical_declaration
	if decl == nil {
		t.Fatal("expected lexical_declaration")
	}

	text := NodeText(decl, src)
	if text != `const greeting = "hello";` {
		t.Fatalf("NodeText got %q, want full declaration", text)
	}
}

// ---------------------------------------------------------------------------
// NodeKindIs
// ---------------------------------------------------------------------------

func TestNodeKindIs(t *testing.T) {
	_, root := parseTS(t, `const x = 1;`)

	if !NodeKindIs(root, "program") {
		t.Fatalf("expected root to be 'program', got %q", root.Kind())
	}
	if NodeKindIs(root, "function_declaration") {
		t.Fatal("root should not be 'function_declaration'")
	}
}

func TestNodeKindIs_nilNode(t *testing.T) {
	if NodeKindIs(nil, "program") {
		t.Fatal("NodeKindIs(nil, ...) should return false")
	}
}

// ---------------------------------------------------------------------------
// FindChildByKind
// ---------------------------------------------------------------------------

func TestFindChildByKind_found(t *testing.T) {
	src := `import { foo } from "bar";`
	_, root := parseTS(t, src)

	// root > import_statement
	importStmt := root.Child(0)
	if importStmt == nil || importStmt.Kind() != "import_statement" {
		t.Fatalf("expected import_statement, got %v", importStmt)
	}

	// import_statement should contain a "string" child for the source path
	strNode := FindChildByKind(importStmt, "string")
	if strNode == nil {
		t.Fatal("FindChildByKind did not find 'string' child")
	}
	got := NodeText(strNode, []byte(src))
	if got != `"bar"` {
		t.Fatalf("got %q, want %q", got, `"bar"`)
	}
}

func TestFindChildByKind_notFound(t *testing.T) {
	_, root := parseTS(t, `const x = 1;`)

	result := FindChildByKind(root, "class_declaration")
	if result != nil {
		t.Fatal("expected nil for non-existent kind")
	}
}

func TestFindChildByKind_nilNode(t *testing.T) {
	result := FindChildByKind(nil, "identifier")
	if result != nil {
		t.Fatal("expected nil for nil node")
	}
}

// ---------------------------------------------------------------------------
// FindChildrenByKind
// ---------------------------------------------------------------------------

func TestFindChildrenByKind(t *testing.T) {
	src := `
const a = 1;
const b = 2;
function foo() {}
const c = 3;
`
	_, root := parseTS(t, src)

	lexicals := FindChildrenByKind(root, "lexical_declaration")
	if len(lexicals) != 3 {
		t.Fatalf("expected 3 lexical_declarations, got %d", len(lexicals))
	}
}

func TestFindChildrenByKind_none(t *testing.T) {
	_, root := parseTS(t, `const x = 1;`)
	results := FindChildrenByKind(root, "class_declaration")
	if len(results) != 0 {
		t.Fatalf("expected 0, got %d", len(results))
	}
}

func TestFindChildrenByKind_nilNode(t *testing.T) {
	results := FindChildrenByKind(nil, "identifier")
	if len(results) != 0 {
		t.Fatal("expected empty slice for nil node")
	}
}

// ---------------------------------------------------------------------------
// FindDescendantByKind
// ---------------------------------------------------------------------------

func TestFindDescendantByKind(t *testing.T) {
	src := `export function greet(name: string) { return name; }`
	_, root := parseTS(t, src)

	// Should find the identifier "greet" deep inside: root > export_statement > function_declaration > identifier
	ident := FindDescendantByKind(root, "identifier")
	if ident == nil {
		t.Fatal("FindDescendantByKind did not find 'identifier'")
	}
	got := NodeText(ident, []byte(src))
	if got != "greet" {
		t.Fatalf("got %q, want %q", got, "greet")
	}
}

func TestFindDescendantByKind_notFound(t *testing.T) {
	_, root := parseTS(t, `const x = 1;`)

	result := FindDescendantByKind(root, "class_declaration")
	if result != nil {
		t.Fatal("expected nil for non-existent descendant kind")
	}
}

func TestFindDescendantByKind_nilNode(t *testing.T) {
	result := FindDescendantByKind(nil, "identifier")
	if result != nil {
		t.Fatal("expected nil for nil node")
	}
}

// ---------------------------------------------------------------------------
// WalkTopLevel
// ---------------------------------------------------------------------------

func TestWalkTopLevel(t *testing.T) {
	src := `
const a = 1;
function foo() { const inner = 2; }
class Bar {}
`
	_, root := parseTS(t, src)

	var kinds []string
	WalkTopLevel(root, func(node *sitter.Node) {
		kinds = append(kinds, node.Kind())
	})

	// Should visit top-level children only (not inner declarations)
	expected := []string{"lexical_declaration", "function_declaration", "class_declaration"}
	if len(kinds) != len(expected) {
		t.Fatalf("expected %d top-level nodes, got %d: %v", len(expected), len(kinds), kinds)
	}
	for i, want := range expected {
		if kinds[i] != want {
			t.Errorf("kinds[%d] = %q, want %q", i, kinds[i], want)
		}
	}
}

func TestWalkTopLevel_emptyProgram(t *testing.T) {
	_, root := parseTS(t, ``)

	var count int
	WalkTopLevel(root, func(node *sitter.Node) {
		count++
	})
	if count != 0 {
		t.Fatalf("expected 0 top-level nodes for empty program, got %d", count)
	}
}

func TestWalkTopLevel_nilNode(t *testing.T) {
	// Should not panic
	WalkTopLevel(nil, func(node *sitter.Node) {
		t.Fatal("callback should not be called for nil node")
	})
}
