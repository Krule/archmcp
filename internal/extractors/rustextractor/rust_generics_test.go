package rustextractor

import (
	"testing"
	"unsafe"

	"github.com/dejo1307/archmcp/internal/extractors/treesitter"
	sitter "github.com/tree-sitter/go-tree-sitter"
	rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
)

// parseRustNode parses Rust source and returns the first top-level node.
func parseRustNode(t *testing.T, src string) (*sitter.Tree, *sitter.Node) {
	t.Helper()
	lang := sitter.NewLanguage(unsafe.Pointer(rust.Language()))
	tree, err := treesitter.Parse([]byte(src), lang)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	root := tree.RootNode()
	if root.ChildCount() == 0 {
		t.Fatal("no top-level nodes")
	}
	return tree, root.Child(0)
}

// --- extractTypeParams tests ---

func TestExtractTypeParams_NilNode(t *testing.T) {
	typeParams, lifetimes, bounds := extractTypeParams(nil, nil)
	if typeParams != nil {
		t.Errorf("expected nil typeParams, got %v", typeParams)
	}
	if lifetimes != nil {
		t.Errorf("expected nil lifetimes, got %v", lifetimes)
	}
	if bounds != nil {
		t.Errorf("expected nil bounds, got %v", bounds)
	}
}

func TestExtractTypeParams_SimpleTypeParams(t *testing.T) {
	src := `fn simple<T, U>(x: T) -> U { x }`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	typeParams, lifetimes, bounds := extractTypeParams(node, []byte(src))

	if len(typeParams) != 2 {
		t.Fatalf("expected 2 type params, got %d: %v", len(typeParams), typeParams)
	}
	if typeParams[0] != "T" || typeParams[1] != "U" {
		t.Errorf("type params = %v, want [T, U]", typeParams)
	}
	if len(lifetimes) != 0 {
		t.Errorf("expected 0 lifetimes, got %v", lifetimes)
	}
	if len(bounds) != 0 {
		t.Errorf("expected 0 bounds, got %v", bounds)
	}
}

func TestExtractTypeParams_WithBounds(t *testing.T) {
	src := `fn with_bounds<T: Clone + Debug, U: Send>(x: T) -> U { x }`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	typeParams, lifetimes, bounds := extractTypeParams(node, []byte(src))

	if len(typeParams) != 2 {
		t.Fatalf("expected 2 type params, got %d: %v", len(typeParams), typeParams)
	}
	if typeParams[0] != "T" || typeParams[1] != "U" {
		t.Errorf("type params = %v, want [T, U]", typeParams)
	}
	if len(lifetimes) != 0 {
		t.Errorf("expected 0 lifetimes, got %v", lifetimes)
	}
	if len(bounds) != 2 {
		t.Fatalf("expected bounds for 2 params, got %d", len(bounds))
	}
	tBounds := bounds["T"]
	if len(tBounds) != 2 || tBounds[0] != "Clone" || tBounds[1] != "Debug" {
		t.Errorf("T bounds = %v, want [Clone, Debug]", tBounds)
	}
	uBounds := bounds["U"]
	if len(uBounds) != 1 || uBounds[0] != "Send" {
		t.Errorf("U bounds = %v, want [Send]", uBounds)
	}
}

func TestExtractTypeParams_Lifetimes(t *testing.T) {
	src := `fn with_lifetime<'a, 'b>(x: &'a str) -> &'b str { x }`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	typeParams, lifetimes, bounds := extractTypeParams(node, []byte(src))

	if len(typeParams) != 0 {
		t.Errorf("expected 0 type params, got %v", typeParams)
	}
	if len(lifetimes) != 2 {
		t.Fatalf("expected 2 lifetimes, got %d: %v", len(lifetimes), lifetimes)
	}
	if lifetimes[0] != "'a" || lifetimes[1] != "'b" {
		t.Errorf("lifetimes = %v, want ['a, 'b]", lifetimes)
	}
	if len(bounds) != 0 {
		t.Errorf("expected 0 bounds, got %v", bounds)
	}
}

func TestExtractTypeParams_Mixed(t *testing.T) {
	src := `impl<'a, T: Clone> Foo<'a, T> { fn bar(&self) {} }`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	typeParams, lifetimes, bounds := extractTypeParams(node, []byte(src))

	if len(typeParams) != 1 || typeParams[0] != "T" {
		t.Errorf("type params = %v, want [T]", typeParams)
	}
	if len(lifetimes) != 1 || lifetimes[0] != "'a" {
		t.Errorf("lifetimes = %v, want ['a]", lifetimes)
	}
	if len(bounds) != 1 {
		t.Fatalf("expected bounds for 1 param, got %d", len(bounds))
	}
	if b := bounds["T"]; len(b) != 1 || b[0] != "Clone" {
		t.Errorf("T bounds = %v, want [Clone]", bounds["T"])
	}
}

func TestExtractTypeParams_StructGenerics(t *testing.T) {
	src := `struct Pair<T, U> { a: T, b: U }`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	typeParams, lifetimes, _ := extractTypeParams(node, []byte(src))

	if len(typeParams) != 2 || typeParams[0] != "T" || typeParams[1] != "U" {
		t.Errorf("type params = %v, want [T, U]", typeParams)
	}
	if len(lifetimes) != 0 {
		t.Errorf("expected 0 lifetimes, got %v", lifetimes)
	}
}

func TestExtractTypeParams_ComplexBound(t *testing.T) {
	// T: Iterator<Item = u32> — the bound is a generic_type, should extract "Iterator<Item = u32>"
	src := `fn constrained<T: Iterator<Item = u32>>(x: T) {}`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	typeParams, _, bounds := extractTypeParams(node, []byte(src))

	if len(typeParams) != 1 || typeParams[0] != "T" {
		t.Errorf("type params = %v, want [T]", typeParams)
	}
	tBounds := bounds["T"]
	if len(tBounds) != 1 || tBounds[0] != "Iterator<Item = u32>" {
		t.Errorf("T bounds = %v, want [Iterator<Item = u32>]", tBounds)
	}
}

func TestExtractTypeParams_NoGenerics(t *testing.T) {
	src := `fn plain(x: u32) -> u32 { x }`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	typeParams, lifetimes, bounds := extractTypeParams(node, []byte(src))

	if typeParams != nil {
		t.Errorf("expected nil typeParams, got %v", typeParams)
	}
	if lifetimes != nil {
		t.Errorf("expected nil lifetimes, got %v", lifetimes)
	}
	if bounds != nil {
		t.Errorf("expected nil bounds, got %v", bounds)
	}
}

// --- extractWhereClause tests ---

func TestExtractWhereClause_NilNode(t *testing.T) {
	result := extractWhereClause(nil, nil)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestExtractWhereClause_NoWhereClause(t *testing.T) {
	src := `fn plain(x: u32) -> u32 { x }`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	result := extractWhereClause(node, []byte(src))
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestExtractWhereClause_Simple(t *testing.T) {
	src := `fn with_where<T>(x: T) -> T where T: Clone + Debug { x }`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	result := extractWhereClause(node, []byte(src))
	if result != "where T: Clone + Debug" {
		t.Errorf("where clause = %q, want %q", result, "where T: Clone + Debug")
	}
}

func TestExtractWhereClause_Multiple(t *testing.T) {
	src := `fn multi<T, U>(x: T, y: U) where T: Clone, U: Debug { }`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	result := extractWhereClause(node, []byte(src))
	if result != "where T: Clone, U: Debug" {
		t.Errorf("where clause = %q, want %q", result, "where T: Clone, U: Debug")
	}
}

// --- extractReturnType tests ---

func TestExtractReturnType_NilNode(t *testing.T) {
	result := extractReturnType(nil, nil)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestExtractReturnType_NoReturn(t *testing.T) {
	src := `fn no_return() {}`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	result := extractReturnType(node, []byte(src))
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestExtractReturnType_SimpleType(t *testing.T) {
	src := `fn simple<T, U>(x: T) -> U { x }`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	result := extractReturnType(node, []byte(src))
	if result != "U" {
		t.Errorf("return type = %q, want %q", result, "U")
	}
}

func TestExtractReturnType_GenericType(t *testing.T) {
	src := `fn returns_result() -> Result<String, Error> { Ok("".into()) }`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	result := extractReturnType(node, []byte(src))
	if result != "Result<String, Error>" {
		t.Errorf("return type = %q, want %q", result, "Result<String, Error>")
	}
}

func TestExtractReturnType_ReferenceType(t *testing.T) {
	src := `fn with_lifetime<'a, 'b>(x: &'a str) -> &'b str { x }`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	result := extractReturnType(node, []byte(src))
	if result != "&'b str" {
		t.Errorf("return type = %q, want %q", result, "&'b str")
	}
}

func TestExtractReturnType_PrimitiveType(t *testing.T) {
	src := `fn add(a: i32, b: i32) -> i32 { a + b }`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	result := extractReturnType(node, []byte(src))
	if result != "i32" {
		t.Errorf("return type = %q, want %q", result, "i32")
	}
}

func TestExtractReturnType_ImplTrait(t *testing.T) {
	src := `fn test() -> impl Iterator<Item = u32> { todo!() }`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	result := extractReturnType(node, []byte(src))
	if result != "impl Iterator<Item = u32>" {
		t.Errorf("return type = %q, want %q", result, "impl Iterator<Item = u32>")
	}
}

func TestExtractTypeParams_LifetimeBound(t *testing.T) {
	// T: 'a + Clone — the bound includes a lifetime
	src := `fn test<T: 'a + Clone>(x: T) {}`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	typeParams, _, bounds := extractTypeParams(node, []byte(src))

	if len(typeParams) != 1 || typeParams[0] != "T" {
		t.Errorf("type params = %v, want [T]", typeParams)
	}
	tBounds := bounds["T"]
	if len(tBounds) != 2 || tBounds[0] != "'a" || tBounds[1] != "Clone" {
		t.Errorf("T bounds = %v, want ['a, Clone]", tBounds)
	}
}

func TestExtractTypeParams_FnBound(t *testing.T) {
	// T: Fn(u32) -> bool — function_type bound
	src := `fn test<T: Fn(u32) -> bool>(x: T) {}`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	typeParams, _, bounds := extractTypeParams(node, []byte(src))

	if len(typeParams) != 1 || typeParams[0] != "T" {
		t.Errorf("type params = %v, want [T]", typeParams)
	}
	tBounds := bounds["T"]
	if len(tBounds) != 1 || tBounds[0] != "Fn(u32) -> bool" {
		t.Errorf("T bounds = %v, want [Fn(u32) -> bool]", tBounds)
	}
}

func TestExtractTypeParams_ConstGeneric(t *testing.T) {
	src := `fn test<const N: usize>() {}`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	typeParams, _, _ := extractTypeParams(node, []byte(src))

	if len(typeParams) != 1 || typeParams[0] != "N" {
		t.Errorf("type params = %v, want [N]", typeParams)
	}
}

func TestExtractTypeParams_StructLifetimeAndBound(t *testing.T) {
	src := `struct Wrapper<'a, T: 'a>(&'a T);`
	tree, node := parseRustNode(t, src)
	defer tree.Close()

	typeParams, lifetimes, bounds := extractTypeParams(node, []byte(src))

	if len(lifetimes) != 1 || lifetimes[0] != "'a" {
		t.Errorf("lifetimes = %v, want ['a]", lifetimes)
	}
	if len(typeParams) != 1 || typeParams[0] != "T" {
		t.Errorf("type params = %v, want [T]", typeParams)
	}
	tBounds := bounds["T"]
	if len(tBounds) != 1 || tBounds[0] != "'a" {
		t.Errorf("T bounds = %v, want ['a]", tBounds)
	}
}
