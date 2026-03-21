package rustextractor

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unsafe"

	"github.com/dejo1307/archmcp/internal/extractors/treesitter"
	"github.com/dejo1307/archmcp/internal/facts"

	sitter "github.com/tree-sitter/go-tree-sitter"
	rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
)

// RustExtractor extracts architectural facts from Rust source code using tree-sitter AST.
type RustExtractor struct{}

// New creates a new RustExtractor.
func New() *RustExtractor {
	return &RustExtractor{}
}

func (e *RustExtractor) Name() string {
	return "rust"
}

// Detect returns true if the repository looks like a Rust project (Cargo.toml present).
func (e *RustExtractor) Detect(repoPath string) (bool, error) {
	_, err := os.Stat(filepath.Join(repoPath, "Cargo.toml"))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// Extract parses Rust files and emits architectural facts.
func (e *RustExtractor) Extract(ctx context.Context, repoPath string, files []string) ([]facts.Fact, error) {
	var allFacts []facts.Fact

	modules := make(map[string]bool)

	for _, relFile := range files {
		select {
		case <-ctx.Done():
			return allFacts, ctx.Err()
		default:
		}

		if !isRustFile(relFile) {
			continue
		}

		absFile := filepath.Join(repoPath, relFile)
		src, err := os.ReadFile(absFile)
		if err != nil {
			log.Printf("[rust-extractor] error reading %s: %v", relFile, err)
			continue
		}

		fileFacts := extractFileAST(src, relFile)
		allFacts = append(allFacts, fileFacts...)

		dir := filepath.Dir(relFile)
		modules[dir] = true
	}

	for dir := range modules {
		allFacts = append(allFacts, facts.Fact{
			Kind: facts.KindModule,
			Name: dir,
			File: dir,
			Props: map[string]any{
				"language": "rust",
			},
		})
	}

	// Parse Cargo.toml for dependencies
	cargoPath := filepath.Join(repoPath, "Cargo.toml")
	if data, err := os.ReadFile(cargoPath); err == nil {
		allFacts = append(allFacts, parseCargoToml(data)...)
	}

	return allFacts, nil
}

// extractFileAST parses a single Rust file using tree-sitter and returns facts.
func extractFileAST(src []byte, relFile string) []facts.Fact {
	lang := sitter.NewLanguage(unsafe.Pointer(rust.Language()))
	tree, err := treesitter.Parse(src, lang)
	if err != nil {
		log.Printf("[rust-extractor] parse error for %s: %v", relFile, err)
		return nil
	}
	defer tree.Close()

	root := tree.RootNode()
	dir := filepath.Dir(relFile)

	var result []facts.Fact
	var pendingAttrs []attrInfo // attributes waiting to be applied to next item

	treesitter.WalkTopLevel(root, func(node *sitter.Node) {
		kind := node.Kind()

		// Collect attribute items (they appear as siblings before the item they decorate)
		if kind == "attribute_item" {
			ai := parseAttributeItem(node, src)
			if ai.isCfgTest {
				// Mark next item for cfg(test) skip — we clear pendingAttrs and skip the next item
				pendingAttrs = append(pendingAttrs, ai)
				return
			}
			pendingAttrs = append(pendingAttrs, ai)
			return
		}

		// Check if previous attributes include cfg(test) — skip this item entirely
		if hasCfgTest(pendingAttrs) {
			pendingAttrs = nil
			return
		}

		switch kind {
		case "use_declaration":
			result = append(result, extractUseDecl(node, src, relFile, dir)...)

		case "function_item":
			ff := extractFunctionItem(node, src, relFile, dir, false)
			applyPendingAttrsToFact(&ff, pendingAttrs)
			result = append(result, ff)

		case "struct_item":
			ff := extractStructItem(node, src, relFile, dir)
			applyDeriveRelations(&ff, pendingAttrs)
			applyNonDeriveAttrs(&ff, pendingAttrs)
			result = append(result, ff)

		case "enum_item":
			ff := extractEnumItem(node, src, relFile, dir)
			applyDeriveRelations(&ff, pendingAttrs)
			applyNonDeriveAttrs(&ff, pendingAttrs)
			result = append(result, ff)

		case "union_item":
			ff := extractUnionItem(node, src, relFile, dir)
			applyDeriveRelations(&ff, pendingAttrs)
			applyNonDeriveAttrs(&ff, pendingAttrs)
			result = append(result, ff)

		case "trait_item":
			ff := extractTraitItem(node, src, relFile, dir)
			applyNonDeriveAttrs(&ff, pendingAttrs)
			result = append(result, ff)

		case "impl_item":
			result = append(result, extractImplItem(node, src, relFile, dir)...)

		case "const_item":
			ff := extractConstItem(node, src, relFile, dir)
			if ff != nil {
				applyNonDeriveAttrs(ff, pendingAttrs)
				result = append(result, *ff)
			}

		case "static_item":
			ff := extractStaticItem(node, src, relFile, dir)
			applyNonDeriveAttrs(&ff, pendingAttrs)
			result = append(result, ff)

		case "type_item":
			ff := extractTypeItem(node, src, relFile, dir)
			applyNonDeriveAttrs(&ff, pendingAttrs)
			result = append(result, ff)

		case "macro_definition":
			ff := extractMacroDef(node, src, relFile, dir)
			result = append(result, ff)

		case "mod_item":
			ff := extractModItem(node, src, relFile, dir)
			result = append(result, ff)
		}

		pendingAttrs = nil
	})

	// Merge impl-trait-for relations onto existing struct/enum facts
	result = mergeImplRelations(result)

	return result
}

// --- Attribute handling ---

type attrInfo struct {
	isDeriveAttr bool
	isCfgTest    bool
	derives      []string // trait names from #[derive(X, Y)]
	rawText      string   // full text of the attribute content (e.g. "serde(rename_all = \"camelCase\")")
}

func parseAttributeItem(node *sitter.Node, src []byte) attrInfo {
	attr := treesitter.FindChildByKind(node, "attribute")
	if attr == nil {
		return attrInfo{}
	}

	attrText := treesitter.NodeText(attr, src)

	// Check for #[cfg(test)]
	nameNode := treesitter.FindChildByKind(attr, "identifier")
	if nameNode != nil && treesitter.NodeText(nameNode, src) == "cfg" {
		tokenTree := treesitter.FindChildByKind(attr, "token_tree")
		if tokenTree != nil {
			ttText := treesitter.NodeText(tokenTree, src)
			if ttText == "(test)" {
				return attrInfo{isCfgTest: true}
			}
		}
	}

	// Check for #[derive(...)]
	if nameNode != nil && treesitter.NodeText(nameNode, src) == "derive" {
		tokenTree := treesitter.FindChildByKind(attr, "token_tree")
		if tokenTree != nil {
			var derives []string
			for i := range tokenTree.ChildCount() {
				child := tokenTree.Child(i)
				if child.Kind() == "identifier" {
					derives = append(derives, treesitter.NodeText(child, src))
				}
			}
			return attrInfo{isDeriveAttr: true, derives: derives}
		}
	}

	// General attribute (non-derive, non-cfg)
	return attrInfo{rawText: attrText}
}

func hasCfgTest(attrs []attrInfo) bool {
	for _, a := range attrs {
		if a.isCfgTest {
			return true
		}
	}
	return false
}

func applyDeriveRelations(f *facts.Fact, attrs []attrInfo) {
	for _, a := range attrs {
		if a.isDeriveAttr {
			for _, trait := range a.derives {
				f.Relations = append(f.Relations, facts.Relation{
					Kind:   facts.RelImplements,
					Target: trait,
				})
			}
		}
	}
}

func applyNonDeriveAttrs(f *facts.Fact, attrs []attrInfo) {
	var nonDeriveAttrs []string
	for _, a := range attrs {
		if !a.isDeriveAttr && !a.isCfgTest && a.rawText != "" {
			// Skip doc and macro_export attributes
			if strings.HasPrefix(a.rawText, "doc") || a.rawText == "macro_export" {
				continue
			}
			nonDeriveAttrs = append(nonDeriveAttrs, a.rawText)
		}
	}
	if len(nonDeriveAttrs) > 0 {
		f.Props["attributes"] = nonDeriveAttrs
	}
}

func applyPendingAttrsToFact(f *facts.Fact, attrs []attrInfo) {
	applyNonDeriveAttrs(f, attrs)
}

// --- Extraction helpers ---

func hasVisibility(node *sitter.Node) bool {
	return treesitter.FindChildByKind(node, "visibility_modifier") != nil
}

func visibilityText(node *sitter.Node, src []byte) string {
	vis := treesitter.FindChildByKind(node, "visibility_modifier")
	if vis == nil {
		return ""
	}
	return treesitter.NodeText(vis, src)
}

func hasModifier(node *sitter.Node, src []byte, mod string) bool {
	mods := treesitter.FindChildByKind(node, "function_modifiers")
	if mods == nil {
		return false
	}
	return strings.Contains(treesitter.NodeText(mods, src), mod)
}

func nodeName(node *sitter.Node, src []byte) string {
	name := treesitter.FindChildByKind(node, "identifier")
	if name != nil {
		return treesitter.NodeText(name, src)
	}
	name = treesitter.FindChildByKind(node, "type_identifier")
	if name != nil {
		return treesitter.NodeText(name, src)
	}
	return ""
}

// --- use_declaration ---

func extractUseDecl(node *sitter.Node, src []byte, relFile, dir string) []facts.Fact {
	// Get the use path: can be scoped_identifier, identifier, use_list, etc.
	// We extract the full path text (excluding "use" keyword and ";")
	var importPath string
	for i := range node.ChildCount() {
		child := node.Child(i)
		k := child.Kind()
		if k == "use" || k == ";" || k == "visibility_modifier" {
			continue
		}
		importPath = treesitter.NodeText(child, src)
		break
	}
	if importPath == "" {
		return nil
	}

	// Determine if internal (crate::, self::, super::) or external
	source := "external"
	target := importPath
	if strings.HasPrefix(importPath, "crate::") || strings.HasPrefix(importPath, "self::") || strings.HasPrefix(importPath, "super::") {
		source = "internal"
	} else {
		// For external imports, use just the crate name (first segment)
		parts := strings.SplitN(importPath, "::", 2)
		target = parts[0]
	}

	return []facts.Fact{{
		Kind: facts.KindDependency,
		Name: dir + " -> " + target,
		File: relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"language": "rust",
			"source":   source,
		},
		Relations: []facts.Relation{
			{Kind: facts.RelImports, Target: target},
		},
	}}
}

// --- function_item ---

func extractFunctionItem(node *sitter.Node, src []byte, relFile, dir string, isMethod bool) facts.Fact {
	name := nodeName(node, src)
	exported := hasVisibility(node)
	isAsync := hasModifier(node, src, "async")
	isUnsafe := hasModifier(node, src, "unsafe")

	symbolKind := facts.SymbolFunc
	if isMethod {
		symbolKind = facts.SymbolMethod
	}

	ff := facts.Fact{
		Kind: facts.KindSymbol,
		Name: dir + "." + name,
		File: relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": symbolKind,
			"exported":    exported,
			"language":    "rust",
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: dir},
		},
	}
	if isAsync {
		ff.Props["async"] = true
	}
	if isUnsafe {
		ff.Props["unsafe"] = true
	}

	// Extract calls from the function body
	block := treesitter.FindChildByKind(node, "block")
	if block != nil {
		calls := extractCallsFromNode(block, src)
		for _, call := range calls {
			ff.Relations = append(ff.Relations, facts.Relation{
				Kind:   facts.RelCalls,
				Target: call,
			})
		}
	}

	return ff
}

// --- struct_item ---

func extractStructItem(node *sitter.Node, src []byte, relFile, dir string) facts.Fact {
	name := treesitter.FindChildByKind(node, "type_identifier")
	nameStr := ""
	if name != nil {
		nameStr = treesitter.NodeText(name, src)
	}
	exported := hasVisibility(node)

	return facts.Fact{
		Kind: facts.KindSymbol,
		Name: dir + "." + nameStr,
		File: relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolStruct,
			"exported":    exported,
			"language":    "rust",
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: dir},
		},
	}
}

// --- enum_item ---

func extractEnumItem(node *sitter.Node, src []byte, relFile, dir string) facts.Fact {
	name := treesitter.FindChildByKind(node, "type_identifier")
	nameStr := ""
	if name != nil {
		nameStr = treesitter.NodeText(name, src)
	}
	exported := hasVisibility(node)

	return facts.Fact{
		Kind: facts.KindSymbol,
		Name: dir + "." + nameStr,
		File: relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolType,
			"exported":    exported,
			"language":    "rust",
			"enum":        true,
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: dir},
		},
	}
}

// --- union_item ---

func extractUnionItem(node *sitter.Node, src []byte, relFile, dir string) facts.Fact {
	name := treesitter.FindChildByKind(node, "type_identifier")
	nameStr := ""
	if name != nil {
		nameStr = treesitter.NodeText(name, src)
	}
	exported := hasVisibility(node)

	return facts.Fact{
		Kind: facts.KindSymbol,
		Name: dir + "." + nameStr,
		File: relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolStruct,
			"exported":    exported,
			"language":    "rust",
			"union":       true,
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: dir},
		},
	}
}

// --- trait_item ---

func extractTraitItem(node *sitter.Node, src []byte, relFile, dir string) facts.Fact {
	name := treesitter.FindChildByKind(node, "type_identifier")
	nameStr := ""
	if name != nil {
		nameStr = treesitter.NodeText(name, src)
	}
	exported := hasVisibility(node)

	return facts.Fact{
		Kind: facts.KindSymbol,
		Name: dir + "." + nameStr,
		File: relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolInterface,
			"exported":    exported,
			"language":    "rust",
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: dir},
		},
	}
}

// --- impl_item ---

func extractImplItem(node *sitter.Node, src []byte, relFile, dir string) []facts.Fact {
	var result []facts.Fact

	// Determine if this is "impl Type" or "impl Trait for Type"
	// In the AST: impl_item has type_identifier children.
	// For "impl Trait for Type": impl, type_identifier(Trait), for, type_identifier(Type), declaration_list
	// For "impl Type": impl, type_identifier(Type), declaration_list
	typeIdents := treesitter.FindChildrenByKind(node, "type_identifier")

	var typeName, traitName string
	hasFork := treesitter.FindChildByKind(node, "for") != nil

	if hasFork && len(typeIdents) >= 2 {
		// impl Trait for Type
		traitName = treesitter.NodeText(typeIdents[0], src)
		typeName = treesitter.NodeText(typeIdents[1], src)
	} else if len(typeIdents) >= 1 {
		// impl Type (possibly with generics)
		// The type_identifier could be the first or only one
		typeName = treesitter.NodeText(typeIdents[0], src)
	}

	// If it's "impl Trait for Type", emit impl relation
	if traitName != "" {
		result = append(result, implRelation{
			typeName:  typeName,
			traitName: traitName,
			dir:       dir,
		}.asFact(relFile, int(node.StartPosition().Row)+1))
	}

	// Extract methods from declaration_list
	declList := treesitter.FindChildByKind(node, "declaration_list")
	if declList == nil {
		return result
	}

	for i := range declList.ChildCount() {
		child := declList.Child(i)
		if child.Kind() != "function_item" {
			continue
		}

		methodName := nodeName(child, src)
		exported := hasVisibility(child)
		isAsync := hasModifier(child, src, "async")
		isUnsafe := hasModifier(child, src, "unsafe")

		ff := facts.Fact{
			Kind: facts.KindSymbol,
			Name: dir + "." + typeName + "." + methodName,
			File: relFile,
			Line: int(child.StartPosition().Row) + 1,
			Props: map[string]any{
				"symbol_kind": facts.SymbolMethod,
				"exported":    exported,
				"language":    "rust",
				"receiver":    typeName,
			},
			Relations: []facts.Relation{
				{Kind: facts.RelDeclares, Target: dir},
			},
		}
		if traitName != "" {
			ff.Props["trait"] = traitName
		}
		if isAsync {
			ff.Props["async"] = true
		}
		if isUnsafe {
			ff.Props["unsafe"] = true
		}

		// Extract calls from method body
		block := treesitter.FindChildByKind(child, "block")
		if block != nil {
			calls := extractCallsFromNode(block, src)
			for _, call := range calls {
				ff.Relations = append(ff.Relations, facts.Relation{
					Kind:   facts.RelCalls,
					Target: call,
				})
			}
		}

		result = append(result, ff)
	}

	return result
}

// --- const_item ---

func extractConstItem(node *sitter.Node, src []byte, relFile, dir string) *facts.Fact {
	name := nodeName(node, src)
	if name == "_" {
		return nil
	}
	exported := hasVisibility(node)

	f := facts.Fact{
		Kind: facts.KindSymbol,
		Name: dir + "." + name,
		File: relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolConstant,
			"exported":    exported,
			"language":    "rust",
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: dir},
		},
	}
	return &f
}

// --- static_item ---

func extractStaticItem(node *sitter.Node, src []byte, relFile, dir string) facts.Fact {
	name := nodeName(node, src)
	exported := hasVisibility(node)

	return facts.Fact{
		Kind: facts.KindSymbol,
		Name: dir + "." + name,
		File: relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolVariable,
			"exported":    exported,
			"language":    "rust",
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: dir},
		},
	}
}

// --- type_item ---

func extractTypeItem(node *sitter.Node, src []byte, relFile, dir string) facts.Fact {
	name := treesitter.FindChildByKind(node, "type_identifier")
	nameStr := ""
	if name != nil {
		nameStr = treesitter.NodeText(name, src)
	}
	exported := hasVisibility(node)

	return facts.Fact{
		Kind: facts.KindSymbol,
		Name: dir + "." + nameStr,
		File: relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolType,
			"exported":    exported,
			"language":    "rust",
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: dir},
		},
	}
}

// --- macro_definition ---

func extractMacroDef(node *sitter.Node, src []byte, relFile, dir string) facts.Fact {
	name := nodeName(node, src)

	return facts.Fact{
		Kind: facts.KindSymbol,
		Name: dir + "." + name,
		File: relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolFunc,
			"exported":    false,
			"language":    "rust",
			"macro":       true,
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: dir},
		},
	}
}

// --- mod_item ---

func extractModItem(node *sitter.Node, src []byte, relFile, dir string) facts.Fact {
	name := nodeName(node, src)
	exported := hasVisibility(node)

	// Determine mod style: external (has ";") vs inline (has declaration_list)
	modStyle := "external"
	if treesitter.FindChildByKind(node, "declaration_list") != nil {
		modStyle = "inline"
	}

	props := map[string]any{
		"symbol_kind": "module",
		"exported":    exported,
		"language":    "rust",
		"mod_style":   modStyle,
	}

	// Capture specific visibility for pub(crate), pub(super), etc.
	visText := visibilityText(node, src)
	if strings.HasPrefix(visText, "pub(") {
		props["visibility"] = visText
	}

	return facts.Fact{
		Kind:  facts.KindSymbol,
		Name:  dir + "." + name,
		File:  relFile,
		Line:  int(node.StartPosition().Row) + 1,
		Props: props,
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: dir},
		},
	}
}

// --- Call extraction from AST ---

// Keywords that look like function calls but aren't.
var callKeywords = map[string]bool{
	"if": true, "while": true, "for": true, "match": true,
	"return": true, "let": true, "else": true, "unsafe": true,
	"async": true, "await": true, "move": true, "loop": true,
	"fn": true, "pub": true, "use": true, "mod": true,
	"struct": true, "enum": true, "trait": true, "impl": true,
	"type": true, "const": true, "static": true, "where": true,
	"as": true, "in": true, "ref": true, "mut": true,
	"Some": true, "None": true, "Ok": true, "Err": true,
	"Box": true, "Vec": true, "Arc": true, "Rc": true,
}

// extractCallsFromNode walks a block node and extracts call_expression targets.
// Returns deduplicated call targets in dotted form matching the regex-based extractCalls output.
func extractCallsFromNode(block *sitter.Node, src []byte) []string {
	seen := make(map[string]bool)
	var calls []string

	walkDescendants(block, func(node *sitter.Node) {
		if node.Kind() != "call_expression" {
			return
		}

		target := extractCallTarget(node, src)
		if target == "" {
			return
		}

		if !seen[target] {
			seen[target] = true
			calls = append(calls, target)
		}
	})

	return calls
}

// extractCallTarget extracts the call target string from a call_expression node.
// Returns "" if the target should be skipped (keyword, etc.)
func extractCallTarget(node *sitter.Node, src []byte) string {
	if node.ChildCount() == 0 {
		return ""
	}

	fn := node.Child(0) // The function being called
	switch fn.Kind() {
	case "identifier":
		// Simple function call: callee()
		name := treesitter.NodeText(fn, src)
		if callKeywords[name] {
			return ""
		}
		return name

	case "field_expression":
		// Method call: obj.method() or self.method()
		// field_expression has: <expr> . <field_identifier>
		obj := fn.Child(0)
		field := treesitter.FindChildByKind(fn, "field_identifier")
		if obj == nil || field == nil {
			return ""
		}
		objName := ""
		switch obj.Kind() {
		case "identifier":
			objName = treesitter.NodeText(obj, src)
		case "self":
			objName = "self"
		default:
			// Complex expression like foo.bar.baz() — use the source text of the receiver
			objName = treesitter.NodeText(obj, src)
		}
		fieldName := treesitter.NodeText(field, src)
		if callKeywords[objName] || callKeywords[fieldName] {
			return ""
		}
		return objName + "." + fieldName

	case "scoped_identifier":
		// Qualified call: Type::method() or module::function()
		// scoped_identifier has: <identifier|scoped_identifier> :: <identifier>
		parts := flattenScopedIdent(fn, src)
		if len(parts) == 0 {
			return ""
		}
		// Use dotted form to match regex behavior: "Config::load" -> "Config.load"
		target := strings.Join(parts, ".")
		// Check if last part is a keyword
		last := parts[len(parts)-1]
		if callKeywords[last] {
			return ""
		}
		return target
	}

	return ""
}

// flattenScopedIdent flattens a scoped_identifier tree into string parts.
// e.g. std::io::stdout -> ["std", "io", "stdout"]
// But for call targets we only want the last two segments to match regex behavior.
func flattenScopedIdent(node *sitter.Node, src []byte) []string {
	if node == nil {
		return nil
	}
	switch node.Kind() {
	case "identifier":
		return []string{treesitter.NodeText(node, src)}
	case "crate":
		return []string{"crate"}
	case "self":
		return []string{"self"}
	case "super":
		return []string{"super"}
	case "scoped_identifier":
		var parts []string
		for i := range node.ChildCount() {
			child := node.Child(i)
			if child.Kind() == "::" {
				continue
			}
			parts = append(parts, flattenScopedIdent(child, src)...)
		}
		// For call targets, regex only captures the last two segments.
		// e.g. "std::io::stdout()" regex matches as "io.stdout"
		// But "Config::load()" matches as "Config.load"
		// The regex pattern is: (?:(\w+)(?:::|\.))?([\w]+)(!?)\s*\(
		// This means it only captures ONE qualifier and one name.
		if len(parts) > 2 {
			parts = parts[len(parts)-2:]
		}
		return parts
	}
	return []string{treesitter.NodeText(node, src)}
}

// walkDescendants visits all descendants of a node, calling fn for each.
func walkDescendants(node *sitter.Node, fn func(*sitter.Node)) {
	for i := range node.ChildCount() {
		child := node.Child(i)
		fn(child)
		walkDescendants(child, fn)
	}
}

// --- extractFile wrapper for backward compatibility with tests ---
// The tests call extractFile(f *os.File, relFile string), so we keep this wrapper.

func extractFile(f *os.File, relFile string) []facts.Fact {
	src, err := io.ReadAll(f)
	if err != nil {
		log.Printf("[rust-extractor] error reading %s: %v", relFile, err)
		return nil
	}
	return extractFileAST(src, relFile)
}

// --- Regex-based call extraction (preserved for direct unit test calls) ---

var (
	// Function/method call: optional qualifier (Type::, self., obj.) + name + "("
	// group 1: qualifier, group 2: function name, group 3: "!" if macro
	callRe = regexp.MustCompile(`(?:(\w+)(?:::|\.))?([\w]+)(!?)\s*\(`)
)

// extractCalls extracts function/method call targets from a single line of Rust code.
// Returns slice of call targets in dotted form: "func", "qualifier.func".
func extractCalls(line string) []string {
	matches := callRe.FindAllStringSubmatch(line, -1)
	var calls []string
	seen := make(map[string]bool)

	for _, m := range matches {
		qualifier := m[1]
		name := m[2]
		bang := m[3]

		// Skip macro invocations (ending with !)
		if bang == "!" {
			continue
		}

		// Skip keywords
		if callKeywords[name] {
			continue
		}
		// Skip if the qualifier is a keyword
		if qualifier != "" && callKeywords[qualifier] {
			continue
		}

		var target string
		if qualifier != "" {
			target = qualifier + "." + name
		} else {
			target = name
		}

		if !seen[target] {
			seen[target] = true
			calls = append(calls, target)
		}
	}

	return calls
}

// --- impl relation merging ---

// implRelation is a temporary holder for "impl Trait for Type" relations.
// It generates a synthetic fact that gets merged onto the real type fact.
type implRelation struct {
	typeName  string
	traitName string
	dir       string
}

func (ir implRelation) asFact(relFile string, line int) facts.Fact {
	return facts.Fact{
		Kind: "impl_relation", // synthetic, will be merged
		Name: ir.dir + "." + ir.typeName + "+impl+" + ir.traitName,
		File: relFile,
		Line: line,
		Props: map[string]any{
			"type_name":  ir.typeName,
			"trait_name": ir.traitName,
			"dir":        ir.dir,
		},
	}
}

// mergeImplRelations takes the fact list, finds synthetic impl_relation facts,
// and adds implements relations to the corresponding type facts.
func mergeImplRelations(ff []facts.Fact) []facts.Fact {
	// Collect impl relations
	type implInfo struct {
		traitName string
		dir       string
	}
	implMap := make(map[string][]implInfo) // typeName -> []implInfo

	var cleaned []facts.Fact
	for _, f := range ff {
		if f.Kind == "impl_relation" {
			typeName := f.Props["type_name"].(string)
			traitName := f.Props["trait_name"].(string)
			dir := f.Props["dir"].(string)
			implMap[typeName] = append(implMap[typeName], implInfo{traitName, dir})
		} else {
			cleaned = append(cleaned, f)
		}
	}

	// Apply impl relations to existing facts
	for i := range cleaned {
		f := &cleaned[i]
		if f.Kind != facts.KindSymbol {
			continue
		}
		// Extract the simple name from "dir.Name"
		parts := strings.SplitN(f.Name, ".", 2)
		if len(parts) != 2 {
			continue
		}
		simpleName := parts[1]
		dir := parts[0]

		if impls, ok := implMap[simpleName]; ok {
			for _, impl := range impls {
				if impl.dir == dir {
					f.Relations = append(f.Relations, facts.Relation{
						Kind:   facts.RelImplements,
						Target: impl.traitName,
					})
				}
			}
		}
	}

	return cleaned
}

func isRustFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".rs")
}

// --- Cargo.toml dependency parsing ---

var (
	// Matches TOML section headers like [dependencies], [dev-dependencies], [build-dependencies]
	tomlSectionRe = regexp.MustCompile(`^\s*\[([^\]]+)\]\s*$`)

	// Matches simple deps: name = "version"
	simpleDepRe = regexp.MustCompile(`^\s*([a-zA-Z_][\w-]*)\s*=\s*"([^"]*)"`)

	// Matches inline table deps: name = { ... }
	inlineTableDepRe = regexp.MustCompile(`^\s*([a-zA-Z_][\w-]*)\s*=\s*\{([^}]*)\}`)

	// For parsing inline table fields
	inlineVersionRe   = regexp.MustCompile(`version\s*=\s*"([^"]*)"`)
	inlinePathRe      = regexp.MustCompile(`path\s*=\s*"([^"]*)"`)
	inlineWorkspaceRe = regexp.MustCompile(`workspace\s*=\s*true`)
)

// cargoDepInfo holds parsed info about a single Cargo.toml dependency.
type cargoDepInfo struct {
	Name  string
	Props map[string]any
}

// parseCargoToml parses [dependencies], [dev-dependencies], and [build-dependencies]
// from a Cargo.toml file and returns dependency facts.
func parseCargoToml(data []byte) []facts.Fact {
	var result []facts.Fact

	lines := strings.Split(string(data), "\n")
	var currentScope string // "", "normal", "dev", "build"

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip empty lines and comments
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Check for section header
		if m := tomlSectionRe.FindStringSubmatch(trimmed); m != nil {
			section := strings.TrimSpace(m[1])
			switch section {
			case "dependencies":
				currentScope = "normal"
			case "dev-dependencies":
				currentScope = "dev"
			case "build-dependencies":
				currentScope = "build"
			default:
				currentScope = ""
			}
			continue
		}

		// Only parse lines inside a dependency section
		if currentScope == "" {
			continue
		}

		// Try inline table first: name = { version = "1.0", ... }
		if m := inlineTableDepRe.FindStringSubmatch(trimmed); m != nil {
			crateName := m[1]
			tableContent := m[2]
			dep := parseInlineTableDep(crateName, tableContent, currentScope)
			result = append(result, dep)
			continue
		}

		// Try simple string: name = "version"
		if m := simpleDepRe.FindStringSubmatch(trimmed); m != nil {
			crateName := m[1]
			version := m[2]
			result = append(result, makeDepFact(crateName, version, "external", currentScope, nil))
			continue
		}
	}

	return result
}

// parseInlineTableDep parses the content of an inline table like: version = "1", features = ["full"]
func parseInlineTableDep(name, tableContent, scope string) facts.Fact {
	props := map[string]any{}

	if m := inlineVersionRe.FindStringSubmatch(tableContent); m != nil {
		props["version"] = m[1]
	}
	if m := inlinePathRe.FindStringSubmatch(tableContent); m != nil {
		props["path"] = m[1]
	}
	if inlineWorkspaceRe.MatchString(tableContent) {
		props["workspace"] = true
	}

	// Determine source
	source := "external"
	if _, hasPath := props["path"]; hasPath {
		source = "internal"
	}

	return makeDepFact(name, props["version"], source, scope, props)
}

// makeDepFact creates a facts.Fact for a Cargo.toml dependency.
func makeDepFact(name string, version any, source, scope string, extraProps map[string]any) facts.Fact {
	props := map[string]any{
		"language":  "rust",
		"source":    source,
		"dep_scope": scope,
	}

	// Set version if available
	if v, ok := version.(string); ok && v != "" {
		props["version"] = v
	}

	// Merge extra props
	for k, v := range extraProps {
		if k == "version" {
			continue // already handled
		}
		props[k] = v
	}

	return facts.Fact{
		Kind:  facts.KindDependency,
		Name:  name,
		File:  "Cargo.toml",
		Props: props,
		Relations: []facts.Relation{
			{Kind: facts.RelDependsOn, Target: name},
		},
	}
}
