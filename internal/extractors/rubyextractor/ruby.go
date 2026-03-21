package rubyextractor

import (
	"bufio"
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
	ruby "github.com/tree-sitter/tree-sitter-ruby/bindings/go"
)

// RubyExtractor extracts architectural facts from Ruby source code using tree-sitter AST.
type RubyExtractor struct{}

// New creates a new RubyExtractor.
func New() *RubyExtractor {
	return &RubyExtractor{}
}

func (e *RubyExtractor) Name() string {
	return "ruby"
}

// Detect returns true if the repository looks like a Ruby project (has a Gemfile).
func (e *RubyExtractor) Detect(repoPath string) (bool, error) {
	if _, err := os.Stat(filepath.Join(repoPath, "Gemfile")); err == nil {
		return true, nil
	}
	return false, nil
}

// Extract parses Ruby files and emits architectural facts.
func (e *RubyExtractor) Extract(ctx context.Context, repoPath string, files []string) ([]facts.Fact, error) {
	var allFacts []facts.Fact

	isRails := detectRailsProject(repoPath)

	// Pass 1: parse packwerk packages.
	pkgInfo := parsePackwerk(repoPath)
	allFacts = append(allFacts, pkgInfo.facts...)

	modules := make(map[string]bool)

	// Pass 2: parse .rb files.
	for _, relFile := range files {
		select {
		case <-ctx.Done():
			return allFacts, ctx.Err()
		default:
		}

		if !isRubyFile(relFile) {
			continue
		}
		if isRails && isRouteFile(relFile) {
			continue
		}

		absFile := filepath.Join(repoPath, relFile)
		src, err := os.ReadFile(absFile)
		if err != nil {
			log.Printf("[ruby-extractor] error reading %s: %v", relFile, err)
			continue
		}

		exported := isPublicAPI(relFile, pkgInfo)
		fileFacts := extractFileAST(src, relFile, isRails, exported)

		storageFacts := extractStorageFacts(relFile, fileFacts)
		allFacts = append(allFacts, fileFacts...)
		allFacts = append(allFacts, storageFacts...)

		if len(storageFacts) > 0 {
			assocFacts := extractAssociationsFromFile(filepath.Join(repoPath, relFile), relFile)
			allFacts = append(allFacts, assocFacts...)
		}

		dir := filepath.Dir(relFile)
		modules[dir] = true
	}

	for dir := range modules {
		if pkgInfo.isPackage(dir) {
			continue
		}
		props := map[string]any{
			"language": "ruby",
		}
		if isRails {
			props["framework"] = "rails"
		}
		allFacts = append(allFacts, facts.Fact{
			Kind:  facts.KindModule,
			Name:  dir,
			File:  dir,
			Props: props,
		})
	}

	if isRails {
		routeFacts := extractAllRoutes(repoPath, files)
		allFacts = append(allFacts, routeFacts...)
	}

	return allFacts, nil
}

// --- Rails detection ---

func detectRailsProject(repoPath string) bool {
	candidates := []string{
		filepath.Join(repoPath, "config", "application.rb"),
		filepath.Join(repoPath, "bin", "rails"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

var (
	qualifiedCallRe = regexp.MustCompile(`\b([A-Z]\w*(?:::[A-Z]\w*)*)\.([\w?!=]+)`)
	receiverCallRe  = regexp.MustCompile(`\b([a-z_]\w*)\.([\w?!=]+)\s*\(`)

	openAPISpecPathRe = regexp.MustCompile(`^\s*openapi_spec_path\s+['"]([^'"]+)['"]`)

	symbolListRe = regexp.MustCompile(`:(\w+)`)
)

// extractRubyCalls returns callee names found on a single source line.
func extractRubyCalls(line string) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, m := range qualifiedCallRe.FindAllStringSubmatch(line, -1) {
		add(m[1] + "." + m[2])
	}
	for _, m := range receiverCallRe.FindAllStringSubmatch(line, -1) {
		add(m[1] + "." + m[2])
	}
	return out
}

// scopeEntry tracks a class/module nesting level in the AST walk.
type scopeEntry struct {
	name string
	kind string // "class", "module", or "eigenclass"
}

// extractFileAST parses a single Ruby file using tree-sitter and returns facts.
func extractFileAST(src []byte, relFile string, isRails bool, exportedByPackwerk bool) []facts.Fact {
	lang := sitter.NewLanguage(unsafe.Pointer(ruby.Language()))
	tree, err := treesitter.Parse(src, lang)
	if err != nil {
		log.Printf("[ruby-extractor] parse error for %s: %v", relFile, err)
		return nil
	}
	defer tree.Close()

	root := tree.RootNode()
	dir := filepath.Dir(relFile)

	w := &astWalker{
		src:               src,
		relFile:           relFile,
		dir:               dir,
		isRails:           isRails,
		exportedByPackwerk: exportedByPackwerk,
		visibility:        "public",
	}

	w.walkBody(root)
	w.attachCalls()
	return w.result
}

// astWalker holds state for the recursive AST walk.
type astWalker struct {
	src                []byte
	relFile            string
	dir                string
	isRails            bool
	exportedByPackwerk bool

	scopeStack     []scopeEntry
	visibility     string
	isConcern      bool
	moduleFunction bool
	result         []facts.Fact
	callAccum      map[string][]string // method full name -> callees
}

// qualifiedName builds a fully-qualified Ruby name from the scope stack.
func (w *astWalker) qualifiedName(name string) string {
	var parts []string
	for _, entry := range w.scopeStack {
		if entry.kind == "eigenclass" || entry.name == "" {
			continue
		}
		parts = append(parts, entry.name)
	}
	if name != "" {
		parts = append(parts, name)
	}
	return strings.Join(parts, "::")
}

// inEigenclass returns true if the current scope is inside class << self.
func (w *astWalker) inEigenclass() bool {
	for _, e := range w.scopeStack {
		if e.kind == "eigenclass" {
			return true
		}
	}
	return false
}

// walkBody walks children of a program or body_statement node.
func (w *astWalker) walkBody(node *sitter.Node) {
	if node == nil {
		return
	}
	for i := range node.ChildCount() {
		child := node.Child(i)
		w.walkNode(child)
	}
}

// walkNode dispatches on node kind.
func (w *astWalker) walkNode(node *sitter.Node) {
	if node == nil {
		return
	}
	kind := node.Kind()

	switch kind {
	case "module":
		w.handleModule(node)
	case "class":
		w.handleClass(node)
	case "singleton_class":
		w.handleSingletonClass(node)
	case "method":
		w.handleMethod(node, false)
	case "singleton_method":
		w.handleMethod(node, true)
	case "assignment":
		w.handleAssignment(node)
	case "call":
		w.handleCall(node)
	case "identifier":
		// Bare identifier at body level: private, protected, public, module_function
		w.handleBareIdentifier(node)
	}
}

func (w *astWalker) nodeText(node *sitter.Node) string {
	return treesitter.NodeText(node, w.src)
}

func (w *astWalker) lineOf(node *sitter.Node) int {
	return int(node.StartPosition().Row) + 1
}

func (w *astWalker) handleModule(node *sitter.Node) {
	nameNode := treesitter.FindChildByKind(node, "constant")
	if nameNode == nil {
		return
	}
	name := w.nodeText(nameNode)
	qualName := w.qualifiedName(name)

	props := map[string]any{
		"symbol_kind": facts.SymbolInterface,
		"exported":    w.exportedByPackwerk,
		"language":    "ruby",
	}
	if w.isConcern {
		props["concern"] = true
		w.isConcern = false
	}
	if w.isRails {
		props["framework"] = "rails"
	}

	w.result = append(w.result, facts.Fact{
		Kind:  facts.KindSymbol,
		Name:  qualName,
		File:  w.relFile,
		Line:  w.lineOf(node),
		Props: props,
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: w.dir},
		},
	})

	savedVis := w.visibility
	savedMF := w.moduleFunction
	w.visibility = "public"
	w.moduleFunction = false
	w.scopeStack = append(w.scopeStack, scopeEntry{name: name, kind: "module"})

	body := treesitter.FindChildByKind(node, "body_statement")
	w.walkBody(body)

	w.scopeStack = w.scopeStack[:len(w.scopeStack)-1]
	w.visibility = savedVis
	w.moduleFunction = savedMF
}

func (w *astWalker) handleClass(node *sitter.Node) {
	nameNode := treesitter.FindChildByKind(node, "constant")
	if nameNode == nil {
		return
	}
	name := w.nodeText(nameNode)
	qualName := w.qualifiedName(name)

	var superclass string
	superNode := treesitter.FindChildByKind(node, "superclass")
	if superNode != nil {
		// superclass node contains: < ConstantOrScopeResolution
		for j := range superNode.ChildCount() {
			ch := superNode.Child(j)
			if ch.Kind() == "constant" || ch.Kind() == "scope_resolution" {
				superclass = w.nodeText(ch)
				break
			}
		}
	}

	exported := w.visibility == "public" && w.exportedByPackwerk

	props := map[string]any{
		"symbol_kind": facts.SymbolClass,
		"exported":    exported,
		"language":    "ruby",
	}
	if w.isRails {
		props["framework"] = "rails"
	}
	if superclass != "" {
		props["superclass"] = superclass
	}

	rels := []facts.Relation{
		{Kind: facts.RelDeclares, Target: w.dir},
	}
	if superclass != "" {
		rels = append(rels, facts.Relation{
			Kind:   facts.RelImplements,
			Target: superclass,
		})
	}

	w.result = append(w.result, facts.Fact{
		Kind:      facts.KindSymbol,
		Name:      qualName,
		File:      w.relFile,
		Line:      w.lineOf(node),
		Props:     props,
		Relations: rels,
	})

	// Check if this is an inline class (has body_statement or not).
	body := treesitter.FindChildByKind(node, "body_statement")
	if body != nil {
		savedVis := w.visibility
		savedMF := w.moduleFunction
		w.visibility = "public"
		w.moduleFunction = false
		w.scopeStack = append(w.scopeStack, scopeEntry{name: name, kind: "class"})

		w.walkBody(body)

		w.scopeStack = w.scopeStack[:len(w.scopeStack)-1]
		w.visibility = savedVis
		w.moduleFunction = savedMF
	}
}

func (w *astWalker) handleSingletonClass(node *sitter.Node) {
	w.scopeStack = append(w.scopeStack, scopeEntry{name: "", kind: "eigenclass"})

	body := treesitter.FindChildByKind(node, "body_statement")
	w.walkBody(body)

	w.scopeStack = w.scopeStack[:len(w.scopeStack)-1]
}

func (w *astWalker) handleMethod(node *sitter.Node, isSingleton bool) {
	nameNode := treesitter.FindChildByKind(node, "identifier")
	if nameNode == nil {
		return
	}
	methodName := w.nodeText(nameNode)

	isSelf := isSingleton || w.inEigenclass() || w.moduleFunction

	scopeName := w.qualifiedName("")
	var fullName string
	if scopeName != "" {
		if isSelf {
			fullName = scopeName + "." + methodName
		} else {
			fullName = scopeName + "#" + methodName
		}
	} else {
		fullName = w.dir + "." + methodName
	}

	symbolKind := facts.SymbolMethod
	if isSelf {
		symbolKind = facts.SymbolFunc
	}

	exported := w.visibility == "public" && w.exportedByPackwerk

	props := map[string]any{
		"symbol_kind": symbolKind,
		"exported":    exported,
		"language":    "ruby",
	}
	if w.isRails {
		props["framework"] = "rails"
	}

	rels := []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}}

	// Check if this is an endless method (has = child followed by expression).
	isEndless := false
	for j := range node.ChildCount() {
		if node.Child(j).Kind() == "=" {
			isEndless = true
			break
		}
	}

	if isEndless {
		// For endless methods, extract calls from the expression part.
		// The expression is the last meaningful child after "=".
		line := w.nodeText(node)
		defLineCalls := extractRubyCalls(line)
		seen := make(map[string]bool)
		for _, callee := range defLineCalls {
			if !seen[callee] {
				seen[callee] = true
				rels = append(rels, facts.Relation{Kind: facts.RelCalls, Target: callee})
			}
		}
	}

	w.result = append(w.result, facts.Fact{
		Kind:      facts.KindSymbol,
		Name:      fullName,
		File:      w.relFile,
		Line:      w.lineOf(node),
		Props:     props,
		Relations: rels,
	})

	// For non-endless methods with a body, accumulate calls.
	if !isEndless {
		body := treesitter.FindChildByKind(node, "body_statement")
		if body != nil {
			w.accumulateCalls(fullName, body)
		}
	}
}

func (w *astWalker) handleAssignment(node *sitter.Node) {
	// Constant assignment: CONST = value
	lhs := node.Child(0)
	if lhs == nil || lhs.Kind() != "constant" {
		return
	}
	constName := w.nodeText(lhs)
	// Must be ALL_CAPS to be treated as a constant (not a class/module name).
	if !isAllCaps(constName) {
		return
	}

	scopeName := w.qualifiedName("")
	var fullName string
	if scopeName != "" {
		fullName = scopeName + "::" + constName
	} else {
		fullName = w.dir + "." + constName
	}

	w.result = append(w.result, facts.Fact{
		Kind: facts.KindSymbol,
		Name: fullName,
		File: w.relFile,
		Line: w.lineOf(node),
		Props: map[string]any{
			"symbol_kind": facts.SymbolConstant,
			"exported":    w.visibility == "public" && w.exportedByPackwerk,
			"language":    "ruby",
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: w.dir},
		},
	})
}

func (w *astWalker) handleCall(node *sitter.Node) {
	// call node: identifier + argument_list
	// Handles: require, require_relative, include, extend, prepend,
	//          attr_reader/writer/accessor, openapi_spec_path
	fnNode := node.Child(0)
	if fnNode == nil || fnNode.Kind() != "identifier" {
		return
	}
	fnName := w.nodeText(fnNode)

	switch fnName {
	case "require", "require_relative":
		w.handleRequire(node, fnName)
	case "include", "extend", "prepend":
		w.handleMixin(node, fnName)
	case "attr_reader", "attr_writer", "attr_accessor":
		w.handleAttr(node, fnName)
	case "openapi_spec_path":
		w.handleOpenapiSpecPath(node)
	}
}

func (w *astWalker) handleBareIdentifier(node *sitter.Node) {
	text := w.nodeText(node)
	switch text {
	case "private":
		w.visibility = "private"
	case "protected":
		w.visibility = "protected"
	case "public":
		w.visibility = "public"
	case "module_function":
		w.moduleFunction = true
	}
}

func (w *astWalker) handleRequire(node *sitter.Node, fnName string) {
	args := treesitter.FindChildByKind(node, "argument_list")
	if args == nil {
		return
	}
	strNode := treesitter.FindDescendantByKind(args, "string_content")
	if strNode == nil {
		return
	}
	importPath := w.nodeText(strNode)

	props := map[string]any{
		"language": "ruby",
	}
	if fnName == "require_relative" {
		props["require_relative"] = true
	}

	w.result = append(w.result, facts.Fact{
		Kind:  facts.KindDependency,
		Name:  w.dir + " -> " + importPath,
		File:  w.relFile,
		Line:  w.lineOf(node),
		Props: props,
		Relations: []facts.Relation{
			{Kind: facts.RelImports, Target: importPath},
		},
	})
}

func (w *astWalker) handleMixin(node *sitter.Node, fnName string) {
	args := treesitter.FindChildByKind(node, "argument_list")
	if args == nil {
		return
	}

	// The argument can be a constant or scope_resolution.
	var mixinName string
	for j := range args.ChildCount() {
		arg := args.Child(j)
		if arg.Kind() == "constant" || arg.Kind() == "scope_resolution" {
			mixinName = w.nodeText(arg)
			break
		}
	}
	if mixinName == "" {
		return
	}

	// Detect ActiveSupport::Concern.
	if fnName == "extend" && mixinName == "ActiveSupport::Concern" {
		w.isConcern = true
		return
	}

	scopeName := w.qualifiedName("")
	if scopeName == "" {
		scopeName = w.dir
	}

	w.result = append(w.result, facts.Fact{
		Kind: facts.KindDependency,
		Name: scopeName + " -> " + mixinName,
		File: w.relFile,
		Line: w.lineOf(node),
		Props: map[string]any{
			"language":   "ruby",
			"mixin_kind": fnName,
		},
		Relations: []facts.Relation{
			{Kind: facts.RelImplements, Target: mixinName},
		},
	})
}

func (w *astWalker) handleAttr(node *sitter.Node, fnName string) {
	attrKind := strings.TrimPrefix(fnName, "attr_")
	args := treesitter.FindChildByKind(node, "argument_list")
	if args == nil {
		return
	}

	scopeName := w.qualifiedName("")
	if scopeName == "" {
		scopeName = w.dir
	}

	// Each symbol argument: :name
	for j := range args.ChildCount() {
		arg := args.Child(j)
		if arg.Kind() != "simple_symbol" {
			continue
		}
		// simple_symbol text is ":name", extract the name part.
		symText := w.nodeText(arg)
		attrName := strings.TrimPrefix(symText, ":")

		w.result = append(w.result, facts.Fact{
			Kind: facts.KindSymbol,
			Name: scopeName + "#" + attrName,
			File: w.relFile,
			Line: w.lineOf(node),
			Props: map[string]any{
				"symbol_kind": facts.SymbolVariable,
				"exported":    w.visibility == "public" && w.exportedByPackwerk,
				"language":    "ruby",
				"attr_kind":   attrKind,
			},
			Relations: []facts.Relation{
				{Kind: facts.RelDeclares, Target: w.dir},
			},
		})
	}
}

func (w *astWalker) handleOpenapiSpecPath(node *sitter.Node) {
	args := treesitter.FindChildByKind(node, "argument_list")
	if args == nil {
		return
	}
	strNode := treesitter.FindDescendantByKind(args, "string_content")
	if strNode == nil {
		return
	}
	specFile := w.nodeText(strNode)

	scopeName := w.qualifiedName("")
	if scopeName == "" {
		scopeName = w.dir
	}

	w.result = append(w.result, facts.Fact{
		Kind: facts.KindDependency,
		Name: scopeName + " -> " + specFile,
		File: w.relFile,
		Line: w.lineOf(node),
		Props: map[string]any{
			"language":  "ruby",
			"type":      "openapi_spec",
			"spec_file": specFile,
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDependsOn, Target: specFile},
		},
	})
}

// accumulateCalls walks a method body and accumulates call targets.
func (w *astWalker) accumulateCalls(methodName string, body *sitter.Node) {
	if w.callAccum == nil {
		w.callAccum = make(map[string][]string)
	}
	walkAllDescendants(body, func(n *sitter.Node) {
		if n.Kind() != "call" {
			return
		}
		// Only extract calls with a receiver (dot calls).
		line := w.nodeText(n)
		calls := extractRubyCalls(line)
		w.callAccum[methodName] = append(w.callAccum[methodName], calls...)
	})
}

// attachCalls attaches accumulated RelCalls edges to each method/function fact.
func (w *astWalker) attachCalls() {
	if w.callAccum == nil {
		return
	}
	seen := make(map[string]map[string]bool)
	for i, f := range w.result {
		sk, _ := f.Props["symbol_kind"].(string)
		if f.Kind != facts.KindSymbol ||
			(sk != facts.SymbolMethod && sk != facts.SymbolFunc) {
			continue
		}
		calls, ok := w.callAccum[f.Name]
		if !ok {
			continue
		}
		if seen[f.Name] == nil {
			seen[f.Name] = make(map[string]bool)
		}
		for _, callee := range calls {
			if seen[f.Name][callee] {
				continue
			}
			seen[f.Name][callee] = true
			w.result[i].Relations = append(w.result[i].Relations,
				facts.Relation{Kind: facts.RelCalls, Target: callee})
		}
	}
}

// walkAllDescendants visits all descendants of a node.
func walkAllDescendants(node *sitter.Node, fn func(*sitter.Node)) {
	if node == nil {
		return
	}
	for i := range node.ChildCount() {
		child := node.Child(i)
		fn(child)
		walkAllDescendants(child, fn)
	}
}

// isAllCaps returns true if s is all uppercase letters, digits, and underscores
// and starts with an uppercase letter with at least 2 chars.
func isAllCaps(s string) bool {
	if len(s) < 2 {
		return false
	}
	for _, ch := range s {
		if !((ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return false
		}
	}
	return true
}

// extractFile provides backward compatibility with existing tests.
// Tests call extractFile(f *os.File, relFile, isRails, exported).
func extractFile(f *os.File, relFile string, isRails bool, exportedByPackwerk bool) []facts.Fact {
	src, err := io.ReadAll(f)
	if err != nil {
		log.Printf("[ruby-extractor] error reading %s: %v", relFile, err)
		return nil
	}
	return extractFileAST(src, relFile, isRails, exportedByPackwerk)
}

// isRubyFile returns true if the file has a .rb extension.
func isRubyFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".rb")
}

// isPublicAPI checks if a file is within a packwerk package's app/public/ directory.
func isPublicAPI(relFile string, pkg *packwerkInfo) bool {
	if pkg == nil || len(pkg.packages) == 0 {
		return true
	}

	ownerPkg := pkg.ownerPackage(relFile)
	if ownerPkg == "" {
		return true
	}

	pkgCfg, ok := pkg.packages[ownerPkg]
	if !ok || !pkgCfg.enforcePrivacy {
		return true
	}

	publicDir := filepath.Join(ownerPkg, "app", "public")
	return strings.HasPrefix(relFile, publicDir+"/") || strings.HasPrefix(relFile, publicDir+"\\")
}

// extractAssociationsFromFile re-reads a file to extract ActiveRecord associations and scopes.
func extractAssociationsFromFile(absPath string, relFile string) []facts.Fact {
	f, err := os.Open(absPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	// Find model classes in the file to determine context.
	modelClasses := make(map[string]bool)
	classRe := regexp.MustCompile(`^\s*class\s+([\w:]+)(?:\s*<\s*([\w:]+))?`)
	for _, line := range lines {
		if m := classRe.FindStringSubmatch(line); m != nil {
			superclass := m[2]
			if isARBaseClass(superclass) {
				modelClasses[m[1]] = true
			}
		}
	}

	if len(modelClasses) == 0 {
		return nil
	}

	var result []facts.Fact
	for lineNum, line := range lines {
		// Association declarations.
		if m := associationRe.FindStringSubmatch(line); m != nil {
			assocKind := m[1]
			assocName := m[2]

			targetModel := assocName
			if assocKind == "has_many" || assocKind == "has_and_belongs_to_many" {
				targetModel = singularize(assocName)
			}
			targetModel = snakeToCamel(targetModel)

			result = append(result, facts.Fact{
				Kind: facts.KindDependency,
				Name: relFile + ":" + assocKind + " :" + assocName,
				File: relFile,
				Line: lineNum + 1,
				Props: map[string]any{
					"language":         "ruby",
					"association_kind": assocKind,
				},
				Relations: []facts.Relation{
					{Kind: facts.RelDependsOn, Target: targetModel},
				},
			})
		}

		// Scope declarations on models.
		if m := scopeRe.FindStringSubmatch(line); m != nil {
			result = append(result, facts.Fact{
				Kind: facts.KindSymbol,
				Name: "scope:" + m[1],
				File: relFile,
				Line: lineNum + 1,
				Props: map[string]any{
					"symbol_kind": facts.SymbolFunc,
					"language":    "ruby",
					"scope":       true,
				},
			})
		}

		// Explicit table name: self.table_name = 'foo'
		if m := tableNameRe.FindStringSubmatch(line); m != nil {
			result = append(result, facts.Fact{
				Kind: facts.KindStorage,
				Name: m[1],
				File: relFile,
				Line: lineNum + 1,
				Props: map[string]any{
					"storage_kind": "table",
					"language":     "ruby",
					"framework":    "rails",
					"explicit":     true,
				},
			})
		}
	}

	return result
}
