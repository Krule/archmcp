package rustextractor

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
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

	// Build workspace crate map for cross-crate resolution
	crateMap := buildCrateMap(repoPath)

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

		// Resolve cross-crate import targets using workspace crate map
		if len(crateMap) > 0 {
			fileFacts = resolveCrateImports(fileFacts, crateMap)
		}

		allFacts = append(allFacts, fileFacts...)

		dir := filepath.Dir(relFile)
		modules[dir] = true
	}

	// Cross-file call target resolution: resolve calls that the per-file pass
	// couldn't resolve because the target is defined in a different file within
	// the same module directory. This is critical for cohesion analysis.
	allFacts = resolveCallTargetsCrossFile(allFacts)

	// Resolve mod-relative use statements: `use sibling_mod::X` where
	// sibling_mod is declared via `mod sibling_mod;` in the same file.
	allFacts = resolveModRelativeImports(allFacts)

	// Resolve crate:: imports to module directory paths.
	allFacts = resolveCratePathImports(allFacts, modules)

	// Build module hierarchy from mod declarations (e.g., `mod handlers;`)
	// This creates parent→child module relationships for the architectural graph.
	modDeclsByDir := collectModDeclarations(allFacts)
	for dir, decls := range modDeclsByDir {
		hierarchyFacts := buildModuleHierarchy(dir, decls.names, decls.file)
		allFacts = append(allFacts, hierarchyFacts...)
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

	// Resolve call targets from raw AST form to fact-name form
	result = resolveCallTargets(result)

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

// mergeStringSlices merges two string slices, deduplicating values.
func mergeStringSlices(a, b []string) []string {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]bool, len(a))
	result := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// mergeBoundsMap merges two bounds maps (type param → trait bounds).
func mergeBoundsMap(a, b map[string][]string) map[string][]string {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	result := make(map[string][]string, len(a)+len(b))
	for k, v := range a {
		result[k] = v
	}
	for k, v := range b {
		if existing, ok := result[k]; ok {
			result[k] = append(existing, v...)
		} else {
			result[k] = v
		}
	}
	return result
}

// modDeclInfo holds mod declaration names and the declaring file for a directory.
type modDeclInfo struct {
	names []string
	file  string
}

// collectModDeclarations scans extracted facts for external mod declarations
// (symbol_kind="module", mod_style="external") and groups them by directory.
func collectModDeclarations(ff []facts.Fact) map[string]modDeclInfo {
	result := make(map[string]modDeclInfo)
	for _, f := range ff {
		if f.Kind != facts.KindSymbol {
			continue
		}
		sk, _ := f.Props["symbol_kind"].(string)
		ms, _ := f.Props["mod_style"].(string)
		if sk != "module" || ms != "external" {
			continue
		}
		dir := filepath.Dir(f.File)
		// Extract mod name from fact name "dir.modname"
		prefix := dir + "."
		if !strings.HasPrefix(f.Name, prefix) {
			continue
		}
		modName := f.Name[len(prefix):]

		info := result[dir]
		info.names = append(info.names, modName)
		info.file = f.File
		result[dir] = info
	}
	return result
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

	props := map[string]any{
		"language": "rust",
		"source":   source,
	}
	// Store the full import path so cross-crate resolution can resolve sub-modules
	if source == "external" && strings.Contains(importPath, "::") {
		props["import_path"] = importPath
	}

	return []facts.Fact{{
		Kind:  facts.KindDependency,
		Name:  dir + " -> " + target,
		File:  relFile,
		Line:  int(node.StartPosition().Row) + 1,
		Props: props,
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

	// Wire generics: type params, lifetimes, bounds, where clause, return type
	typeParams, lifetimes, bounds := extractTypeParams(node, src)
	if len(typeParams) > 0 {
		ff.Props["type_params"] = typeParams
	}
	if len(lifetimes) > 0 {
		ff.Props["lifetimes"] = lifetimes
	}
	if len(bounds) > 0 {
		ff.Props["bounds"] = bounds
	}
	if wc := extractWhereClause(node, src); wc != "" {
		ff.Props["where_clause"] = wc
	}
	if rt := extractReturnType(node, src); rt != "" {
		ff.Props["return_type"] = rt
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

	ff := facts.Fact{
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

	// Wire generics: type params, lifetimes, bounds, where clause
	typeParams, lifetimes, bounds := extractTypeParams(node, src)
	if len(typeParams) > 0 {
		ff.Props["type_params"] = typeParams
	}
	if len(lifetimes) > 0 {
		ff.Props["lifetimes"] = lifetimes
	}
	if len(bounds) > 0 {
		ff.Props["bounds"] = bounds
	}
	if wc := extractWhereClause(node, src); wc != "" {
		ff.Props["where_clause"] = wc
	}

	return ff
}

// --- enum_item ---

func extractEnumItem(node *sitter.Node, src []byte, relFile, dir string) facts.Fact {
	name := treesitter.FindChildByKind(node, "type_identifier")
	nameStr := ""
	if name != nil {
		nameStr = treesitter.NodeText(name, src)
	}
	exported := hasVisibility(node)

	ff := facts.Fact{
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

	// Wire generics: type params, lifetimes, bounds, where clause
	typeParams, lifetimes, bounds := extractTypeParams(node, src)
	if len(typeParams) > 0 {
		ff.Props["type_params"] = typeParams
	}
	if len(lifetimes) > 0 {
		ff.Props["lifetimes"] = lifetimes
	}
	if len(bounds) > 0 {
		ff.Props["bounds"] = bounds
	}
	if wc := extractWhereClause(node, src); wc != "" {
		ff.Props["where_clause"] = wc
	}

	return ff
}

// --- union_item ---

func extractUnionItem(node *sitter.Node, src []byte, relFile, dir string) facts.Fact {
	name := treesitter.FindChildByKind(node, "type_identifier")
	nameStr := ""
	if name != nil {
		nameStr = treesitter.NodeText(name, src)
	}
	exported := hasVisibility(node)

	ff := facts.Fact{
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

	// Wire generics: type params, lifetimes, bounds, where clause
	typeParams, lifetimes, bounds := extractTypeParams(node, src)
	if len(typeParams) > 0 {
		ff.Props["type_params"] = typeParams
	}
	if len(lifetimes) > 0 {
		ff.Props["lifetimes"] = lifetimes
	}
	if len(bounds) > 0 {
		ff.Props["bounds"] = bounds
	}
	if wc := extractWhereClause(node, src); wc != "" {
		ff.Props["where_clause"] = wc
	}

	return ff
}

// --- trait_item ---

func extractTraitItem(node *sitter.Node, src []byte, relFile, dir string) facts.Fact {
	name := treesitter.FindChildByKind(node, "type_identifier")
	nameStr := ""
	if name != nil {
		nameStr = treesitter.NodeText(name, src)
	}
	exported := hasVisibility(node)

	ff := facts.Fact{
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

	// Wire generics: type params, lifetimes, bounds, where clause
	typeParams, lifetimes, bounds := extractTypeParams(node, src)
	if len(typeParams) > 0 {
		ff.Props["type_params"] = typeParams
	}
	if len(lifetimes) > 0 {
		ff.Props["lifetimes"] = lifetimes
	}
	if len(bounds) > 0 {
		ff.Props["bounds"] = bounds
	}
	if wc := extractWhereClause(node, src); wc != "" {
		ff.Props["where_clause"] = wc
	}

	return ff
}

// extractImplTypeNames extracts type names from an impl_item node.
// Handles both bare types (impl Foo) and generic types (impl<'a> Foo<'a>).
// Returns names in order of appearance (for "impl Trait for Type": [Trait, Type]).
func extractImplTypeNames(node *sitter.Node, src []byte) []string {
	var names []string
	seenKeywords := map[string]bool{"impl": true, "for": true}

	for i := range node.ChildCount() {
		child := node.Child(i)
		kind := child.Kind()

		if kind == "type_identifier" {
			text := treesitter.NodeText(child, src)
			if !seenKeywords[text] {
				names = append(names, text)
			}
		} else if kind == "generic_type" {
			// generic_type contains: type_identifier, type_arguments
			inner := treesitter.FindChildByKind(child, "type_identifier")
			if inner != nil {
				names = append(names, treesitter.NodeText(inner, src))
			}
		} else if kind == "scoped_type_identifier" {
			// e.g. path::Type — extract just the last type_identifier
			inner := treesitter.FindChildByKind(child, "type_identifier")
			if inner != nil {
				names = append(names, treesitter.NodeText(inner, src))
			}
		}
	}

	return names
}

// --- impl_item ---

func extractImplItem(node *sitter.Node, src []byte, relFile, dir string) []facts.Fact {
	var result []facts.Fact

	// Determine if this is "impl Type" or "impl Trait for Type"
	// In the AST: impl_item has type_identifier children.
	// For "impl Trait for Type": impl, type_identifier(Trait), for, type_identifier(Type), declaration_list
	// For "impl Type": impl, type_identifier(Type), declaration_list
	//
	// When generics are present (impl<'a> Foo<'a>), the type is wrapped in
	// a generic_type node: generic_type { type_identifier("Foo"), type_arguments }
	// We need to extract the type_identifier from generic_type nodes too.
	typeNames := extractImplTypeNames(node, src)

	var typeName, traitName string
	hasFork := treesitter.FindChildByKind(node, "for") != nil

	if hasFork && len(typeNames) >= 2 {
		// impl Trait for Type
		traitName = typeNames[0]
		typeName = typeNames[1]
	} else if len(typeNames) >= 1 {
		// impl Type (possibly with generics)
		typeName = typeNames[0]
	}

	// Extract impl-level generics (type params, lifetimes, where clause)
	implTypeParams, implLifetimes, implBounds := extractTypeParams(node, src)
	implWhereClause := extractWhereClause(node, src)

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

		// Wire generics: merge impl-level and method-level type params
		methodTypeParams, methodLifetimes, methodBounds := extractTypeParams(child, src)
		allTypeParams := mergeStringSlices(implTypeParams, methodTypeParams)
		allLifetimes := mergeStringSlices(implLifetimes, methodLifetimes)
		allBounds := mergeBoundsMap(implBounds, methodBounds)
		if len(allTypeParams) > 0 {
			ff.Props["type_params"] = allTypeParams
		}
		if len(allLifetimes) > 0 {
			ff.Props["lifetimes"] = allLifetimes
		}
		if len(allBounds) > 0 {
			ff.Props["bounds"] = allBounds
		}
		// Method where clause takes precedence; fall back to impl-level
		if wc := extractWhereClause(child, src); wc != "" {
			ff.Props["where_clause"] = wc
		} else if implWhereClause != "" {
			ff.Props["where_clause"] = implWhereClause
		}
		if rt := extractReturnType(child, src); rt != "" {
			ff.Props["return_type"] = rt
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

	ff := facts.Fact{
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

	// Wire generics: type params, lifetimes, bounds, where clause
	typeParams, lifetimes, bounds := extractTypeParams(node, src)
	if len(typeParams) > 0 {
		ff.Props["type_params"] = typeParams
	}
	if len(lifetimes) > 0 {
		ff.Props["lifetimes"] = lifetimes
	}
	if len(bounds) > 0 {
		ff.Props["bounds"] = bounds
	}
	if wc := extractWhereClause(node, src); wc != "" {
		ff.Props["where_clause"] = wc
	}

	return ff
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
