package rustextractor

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dejo1307/archmcp/internal/extractors/treesitter"
	"github.com/dejo1307/archmcp/internal/facts"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

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

// extractCallsFromNode walks a block node and extracts call_expression and
// macro_invocation targets. Returns deduplicated call targets in dotted form.
func extractCallsFromNode(block *sitter.Node, src []byte) []string {
	seen := make(map[string]bool)
	var calls []string

	walkDescendants(block, func(node *sitter.Node) {
		var target string
		switch node.Kind() {
		case "call_expression":
			target = extractCallTarget(node, src)
		case "macro_invocation":
			target = extractMacroTarget(node, src)
		default:
			return
		}

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
	return targetFromExpr(node.Child(0), src)
}

// targetFromExpr resolves a call-target expression node to a dotted string.
// Handles identifier, field_expression, scoped_identifier, and generic_function
// (turbofish). Returns "" for keywords or unrecognized nodes.
func targetFromExpr(expr *sitter.Node, src []byte) string {
	switch expr.Kind() {
	case "identifier":
		name := treesitter.NodeText(expr, src)
		if callKeywords[name] {
			return ""
		}
		return name

	case "field_expression":
		// Method call: obj.method() or self.method()
		obj := expr.Child(0)
		field := treesitter.FindChildByKind(expr, "field_identifier")
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
			objName = treesitter.NodeText(obj, src)
		}
		fieldName := treesitter.NodeText(field, src)
		if callKeywords[objName] || callKeywords[fieldName] {
			return ""
		}
		return objName + "." + fieldName

	case "scoped_identifier":
		parts := flattenScopedIdent(expr, src)
		if len(parts) == 0 {
			return ""
		}
		target := strings.Join(parts, ".")
		if callKeywords[parts[len(parts)-1]] {
			return ""
		}
		return target

	case "generic_function":
		// Turbofish: collect::<Vec<_>>(), parse::<i32>()
		// Unwrap to the inner expression before ::< type_arguments >.
		if expr.ChildCount() == 0 {
			return ""
		}
		return targetFromExpr(expr.Child(0), src)
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

// stdMacros is the set of standard library macros that should not appear as
// call targets.  User-defined macros (my_macro!, sqlx::query!) are kept.
var stdMacros = map[string]bool{
	// printing / formatting
	"println": true, "eprintln": true, "print": true, "eprint": true,
	"format": true, "format_args": true, "write": true, "writeln": true,
	// collection / allocation
	"vec": true, "array": true,
	// debugging / assertion
	"dbg": true, "todo": true, "unimplemented": true, "unreachable": true,
	"panic": true, "assert": true, "assert_eq": true, "assert_ne": true,
	"debug_assert": true, "debug_assert_eq": true, "debug_assert_ne": true,
	// compile-time / env
	"cfg": true, "env": true, "option_env": true,
	"include": true, "include_str": true, "include_bytes": true,
	"concat": true, "stringify": true,
	"line": true, "column": true, "file": true, "module_path": true,
	// test helpers
	"compile_error": true,
	// tracing / logging (common but still library macros – treat as std-like)
	"log": true,
	// match helpers
	"matches": true,
	// try (deprecated)
	"try": true,
}

// extractMacroTarget extracts a call target from a macro_invocation node.
// Returns the macro name for user-defined macros, "" for standard library macros.
// Scoped macros like sqlx::query! produce "sqlx.query".
func extractMacroTarget(node *sitter.Node, src []byte) string {
	if node.ChildCount() == 0 {
		return ""
	}

	nameNode := node.Child(0)
	switch nameNode.Kind() {
	case "identifier":
		name := treesitter.NodeText(nameNode, src)
		if stdMacros[name] {
			return ""
		}
		return name

	case "scoped_identifier":
		parts := flattenScopedIdent(nameNode, src)
		if len(parts) == 0 {
			return ""
		}
		last := parts[len(parts)-1]
		if stdMacros[last] {
			return ""
		}
		return strings.Join(parts, ".")
	}
	return ""
}

// walkDescendants visits all descendants of a node, calling fn for each.
func walkDescendants(node *sitter.Node, fn func(*sitter.Node)) {
	for i := range node.ChildCount() {
		child := node.Child(i)
		fn(child)
		walkDescendants(child, fn)
	}
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

// resolveCallTargets rewrites call relation targets from raw AST form
// (e.g. "callee", "Config.load", "self.baz") to fact-name form
// (e.g. "src.callee", "src.Config.load", "src.Foo.baz").
//
// This is critical for downstream analyses (cohesion, coupling, dead code)
// which match call targets against fact names.
func resolveCallTargets(ff []facts.Fact) []facts.Fact {
	// Build lookup: known fact names by dir, indexed for quick resolution.
	// Simple names: "callee" in dir "src" → "src.callee"
	// Qualified names: "Config.load" in dir "src" → "src.Config.load"
	type factKey struct {
		dir       string
		shortName string // "callee", "Config.load", etc.
	}
	known := make(map[factKey]string) // factKey → full fact name

	for _, f := range ff {
		if f.Kind != facts.KindSymbol {
			continue
		}
		dir := filepath.Dir(f.File)

		// Extract the suffix after "dir."
		prefix := dir + "."
		if strings.HasPrefix(f.Name, prefix) {
			shortName := f.Name[len(prefix):]
			known[factKey{dir, shortName}] = f.Name
		}
	}

	// Second pass: resolve call targets
	for i := range ff {
		f := &ff[i]
		if f.Kind != facts.KindSymbol {
			continue
		}
		dir := filepath.Dir(f.File)
		receiver, _ := f.Props["receiver"].(string)

		for j := range f.Relations {
			rel := &f.Relations[j]
			if rel.Kind != facts.RelCalls {
				continue
			}

			target := rel.Target

			// 1. Try direct lookup: target might already be "Config.load" → "src.Config.load"
			if resolved, ok := known[factKey{dir, target}]; ok {
				rel.Target = resolved
				continue
			}

			// 2. Handle "self.method" → "Receiver.method"
			if strings.HasPrefix(target, "self.") && receiver != "" {
				methodName := target[len("self."):]
				candidate := receiver + "." + methodName
				if resolved, ok := known[factKey{dir, candidate}]; ok {
					rel.Target = resolved
					continue
				}
			}

			// 3. Unresolved — leave as-is (external call, unknown variable method, etc.)
		}
	}

	return ff
}

// resolveCallTargetsCrossFile is a second resolution pass that operates on
// ALL facts from ALL files. It resolves call targets that the per-file pass
// couldn't resolve because the callee is in a different file within the same module.
func resolveCallTargetsCrossFile(ff []facts.Fact) []facts.Fact {
	type factKey struct {
		dir       string
		shortName string
	}
	known := make(map[factKey]string)

	for _, f := range ff {
		if f.Kind != facts.KindSymbol {
			continue
		}
		dir := filepath.Dir(f.File)
		prefix := dir + "."
		if strings.HasPrefix(f.Name, prefix) {
			shortName := f.Name[len(prefix):]
			known[factKey{dir, shortName}] = f.Name
		}
	}

	for i := range ff {
		f := &ff[i]
		if f.Kind != facts.KindSymbol {
			continue
		}
		dir := filepath.Dir(f.File)
		receiver, _ := f.Props["receiver"].(string)

		for j := range f.Relations {
			rel := &f.Relations[j]
			if rel.Kind != facts.RelCalls {
				continue
			}
			// Skip already-resolved targets
			if strings.Contains(rel.Target, "/") || strings.HasPrefix(rel.Target, dir+".") {
				continue
			}

			target := rel.Target

			if resolved, ok := known[factKey{dir, target}]; ok {
				rel.Target = resolved
				continue
			}

			if strings.HasPrefix(target, "self.") && receiver != "" {
				candidate := receiver + "." + target[len("self."):]
				if resolved, ok := known[factKey{dir, candidate}]; ok {
					rel.Target = resolved
					continue
				}
			}
		}
	}

	return ff
}
