package rustextractor

import (
	"bufio"
	"context"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dejo1307/archmcp/internal/facts"
)

// RustExtractor extracts architectural facts from Rust source code using line-based regex parsing.
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
		f, err := os.Open(absFile)
		if err != nil {
			log.Printf("[rust-extractor] error reading %s: %v", relFile, err)
			continue
		}

		fileFacts := extractFile(f, relFile)
		f.Close()
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

// --- Regex patterns ---

var (
	// use <path>;  or  use <path>::{...};  or  use <path> as ...;
	useRe = regexp.MustCompile(`^\s*(?:pub\s+)?use\s+([a-zA-Z_][\w:*]+)`)

	// fn declarations with optional pub/pub(crate)/async/unsafe/const/extern modifiers
	fnRe = regexp.MustCompile(
		`^\s*((?:pub(?:\([^)]*\))?\s+)?)((?:(?:async|unsafe|const|extern\s*"[^"]*")\s+)*)fn\s+(\w+)`)

	// struct declarations
	structRe = regexp.MustCompile(
		`^\s*((?:pub(?:\([^)]*\))?\s+)?)struct\s+(\w+)`)

	// enum declarations
	enumRe = regexp.MustCompile(
		`^\s*((?:pub(?:\([^)]*\))?\s+)?)enum\s+(\w+)`)

	// union declarations
	unionRe = regexp.MustCompile(
		`^\s*((?:pub(?:\([^)]*\))?\s+)?)union\s+(\w+)`)

	// trait declarations
	traitRe = regexp.MustCompile(
		`^\s*((?:pub(?:\([^)]*\))?\s+)?)((?:unsafe\s+)?)trait\s+(\w+)`)

	// impl [Trait for] Type
	implTraitForRe = regexp.MustCompile(
		`^\s*impl(?:\s*<[^>]*>)?\s+(\w+)\s+for\s+(\w+)`)
	implRe = regexp.MustCompile(
		`^\s*impl(?:\s*<[^>]*>)?\s+(\w+)`)

	// const NAME: TYPE = ...;
	constRe = regexp.MustCompile(
		`^\s*((?:pub(?:\([^)]*\))?\s+)?)const\s+(\w+)\s*:`)

	// static [mut] NAME: TYPE = ...;
	staticRe = regexp.MustCompile(
		`^\s*((?:pub(?:\([^)]*\))?\s+)?)static\s+(?:mut\s+)?(\w+)\s*:`)

	// type Alias = ...;
	typeAliasRe = regexp.MustCompile(
		`^\s*((?:pub(?:\([^)]*\))?\s+)?)type\s+(\w+)(?:\s*<[^>]*>)?\s*=`)

	// macro_rules! name
	macroRulesRe = regexp.MustCompile(
		`^\s*(?:#\[macro_export\]\s*)?macro_rules!\s+(\w+)`)

	// mod declarations: mod foo; or mod foo { ... }
	// Captures: (1) optional pub/pub(crate)/pub(super) prefix, (2) module name
	modDeclRe = regexp.MustCompile(
		`^\s*((?:pub(?:\([^)]*\))?\s+)?)mod\s+(\w+)\s*([;{])`)

	// #[derive(Trait1, Trait2, ...)]
	deriveRe = regexp.MustCompile(`#\[derive\(([^)]+)\)\]`)

	// #[cfg(test)]
	cfgTestRe = regexp.MustCompile(`#\[cfg\(test\)\]`)

	// General attribute: #[name] or #[name(...)] or #[path::name] or #[path::name(...)]
	// Excludes derive (handled separately) and cfg (handled separately).
	// Captures the full content between #[ and ]
	attrRe = regexp.MustCompile(`#\[([^\]]+)\]`)

	// Function/method call: optional qualifier (Type::, self., obj.) + name + "("
	// group 1: qualifier, group 2: function name, group 3: "!" if macro
	callRe = regexp.MustCompile(`(?:(\w+)(?:::|\.))?([\w]+)(!?)\s*\(`)
)

// extractFile parses a single Rust file and returns facts.
func extractFile(f *os.File, relFile string) []facts.Fact {
	var result []facts.Fact
	dir := filepath.Dir(relFile)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	type implCtx struct {
		typeName  string // the type being impl'd
		traitName string // non-empty if "impl Trait for Type"
		depth     int    // braceDepth when the impl block opened
	}

	type fnCtx struct {
		factIdx int // index into result slice for the function/method fact
		depth   int // braceDepth when the fn was declared (body is at depth+1)
	}

	var (
		lineNum        int
		braceDepth     int
		pendingDerives []string // derives waiting to be applied to next struct/enum
		pendingAttrs   []string // attribute macros waiting to be applied to next item
		cfgTestDepth   int      // -1 = not in cfg(test), otherwise the braceDepth when we entered
		cfgTestPending bool     // next item is #[cfg(test)]
		inBlockComment bool
		currentImpl    *implCtx // non-nil when inside an impl block
		currentFn      *fnCtx   // non-nil when inside a function/method body
	)
	cfgTestDepth = -1

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip block comments
		if inBlockComment {
			if strings.Contains(line, "*/") {
				inBlockComment = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "/*") {
			if !strings.Contains(line, "*/") {
				inBlockComment = true
			}
			continue
		}

		// Skip line comments
		if strings.HasPrefix(trimmed, "//") {
			continue
		}

		// Check for #[cfg(test)] attribute
		if cfgTestRe.MatchString(line) {
			cfgTestPending = true
			continue
		}

		// Track brace depth
		openBraces := strings.Count(line, "{")
		closeBraces := strings.Count(line, "}")

		// If cfgTestPending and we see a brace opening, enter cfg(test) skip mode
		if cfgTestPending && openBraces > 0 {
			cfgTestDepth = braceDepth
			braceDepth += openBraces - closeBraces
			cfgTestPending = false
			continue
		}
		cfgTestPending = false

		// Effective depth: braceDepth BEFORE processing this line's braces
		effectiveDepth := braceDepth

		braceDepth += openBraces - closeBraces

		// If we're in a cfg(test) block, skip until we exit
		if cfgTestDepth >= 0 {
			if braceDepth <= cfgTestDepth {
				cfgTestDepth = -1
			}
			continue
		}

		// Clear impl context when we exit its brace scope
		if currentImpl != nil && braceDepth <= currentImpl.depth {
			currentImpl = nil
		}

		// Clear fn context when we exit its brace scope
		if currentFn != nil && braceDepth <= currentFn.depth {
			currentFn = nil
		}

		// Extract calls from function/method bodies
		if currentFn != nil && effectiveDepth > currentFn.depth {
			calls := extractCalls(line)
			for _, call := range calls {
				result[currentFn.factIdx].Relations = append(
					result[currentFn.factIdx].Relations,
					facts.Relation{Kind: facts.RelCalls, Target: call},
				)
			}
		}

		// Inside an impl block at depth 1: extract methods
		if currentImpl != nil && effectiveDepth == currentImpl.depth+1 {
			if m := fnRe.FindStringSubmatch(line); m != nil {
				pubMod := m[1]
				modifiers := m[2]
				name := m[3]
				exported := strings.Contains(pubMod, "pub")

				ff := facts.Fact{
					Kind: facts.KindSymbol,
					Name: dir + "." + currentImpl.typeName + "." + name,
					File: relFile,
					Line: lineNum,
					Props: map[string]any{
						"symbol_kind": facts.SymbolMethod,
						"exported":    exported,
						"language":    "rust",
						"receiver":    currentImpl.typeName,
					},
					Relations: []facts.Relation{
						{Kind: facts.RelDeclares, Target: dir},
					},
				}
				if currentImpl.traitName != "" {
					ff.Props["trait"] = currentImpl.traitName
				}
				if strings.Contains(modifiers, "async") {
					ff.Props["async"] = true
				}
				if strings.Contains(modifiers, "unsafe") {
					ff.Props["unsafe"] = true
				}
				result = append(result, ff)
				// Track this method for call extraction
				if openBraces > 0 {
					currentFn = &fnCtx{factIdx: len(result) - 1, depth: effectiveDepth}
				}
				continue
			}
		}

		// Only extract top-level items (effectiveDepth == 0) beyond this point
		if effectiveDepth > 0 {
			continue
		}

		// Collect #[derive(...)] attributes
		if m := deriveRe.FindStringSubmatch(line); m != nil {
			traits := parseDeriveLine(m[1])
			pendingDerives = append(pendingDerives, traits...)
			// If the line only has the derive attribute and nothing else, continue
			if !structRe.MatchString(line) && !enumRe.MatchString(line) {
				continue
			}
		}

		// Collect general attribute macros (non-derive, non-cfg)
		if attrs := extractAttributes(trimmed); len(attrs) > 0 {
			pendingAttrs = append(pendingAttrs, attrs...)
			// If the line is only attributes (no item declaration), continue
			if !fnRe.MatchString(line) && !structRe.MatchString(line) &&
				!enumRe.MatchString(line) && !unionRe.MatchString(line) &&
				!constRe.MatchString(line) && !staticRe.MatchString(line) &&
				!typeAliasRe.MatchString(line) && !traitRe.MatchString(line) {
				continue
			}
		}

		// macro_rules!
		if m := macroRulesRe.FindStringSubmatch(line); m != nil {
			name := m[1]
			result = append(result, facts.Fact{
				Kind: facts.KindSymbol,
				Name: dir + "." + name,
				File: relFile,
				Line: lineNum,
				Props: map[string]any{
					"symbol_kind": facts.SymbolFunc,
					"exported":    false,
					"language":    "rust",
					"macro":       true,
				},
				Relations: []facts.Relation{
					{Kind: facts.RelDeclares, Target: dir},
				},
			})
			pendingDerives = nil
			pendingAttrs = nil
			continue
		}

		// mod declarations: mod foo; or mod foo { ... }
		if m := modDeclRe.FindStringSubmatch(line); m != nil {
			pubMod := m[1]
			name := m[2]
			terminator := m[3] // ";" for external, "{" for inline
			exported := strings.Contains(pubMod, "pub")

			modStyle := "external"
			if terminator == "{" {
				modStyle = "inline"
			}

			props := map[string]any{
				"symbol_kind": "module",
				"exported":    exported,
				"language":    "rust",
				"mod_style":   modStyle,
			}

			// Capture specific visibility for pub(crate), pub(super), etc.
			trimmedPub := strings.TrimSpace(pubMod)
			if strings.HasPrefix(trimmedPub, "pub(") {
				props["visibility"] = trimmedPub
			}

			result = append(result, facts.Fact{
				Kind:  facts.KindSymbol,
				Name:  dir + "." + name,
				File:  relFile,
				Line:  lineNum,
				Props: props,
				Relations: []facts.Relation{
					{Kind: facts.RelDeclares, Target: dir},
				},
			})
			pendingDerives = nil
			pendingAttrs = nil
			continue
		}

		// use statements
		if m := useRe.FindStringSubmatch(line); m != nil {
			importPath := m[1]

			// Determine if internal (crate::, self::, super::) or external
			source := "external"
			target := importPath
			if strings.HasPrefix(importPath, "crate::") || strings.HasPrefix(importPath, "self::") || strings.HasPrefix(importPath, "super::") {
				source = "internal"
				// Keep the full path as target for internal imports
			} else {
				// For external imports, use just the crate name (first segment)
				parts := strings.SplitN(importPath, "::", 2)
				target = parts[0]
			}

			result = append(result, facts.Fact{
				Kind: facts.KindDependency,
				Name: dir + " -> " + target,
				File: relFile,
				Line: lineNum,
				Props: map[string]any{
					"language": "rust",
					"source":   source,
				},
				Relations: []facts.Relation{
					{Kind: facts.RelImports, Target: target},
				},
			})
			continue
		}

		// impl Trait for Type — set context and emit relation
		if m := implTraitForRe.FindStringSubmatch(line); m != nil {
			traitName := m[1]
			typeName := m[2]
			result = append(result, implRelation{
				typeName:  typeName,
				traitName: traitName,
				dir:       dir,
			}.asFact(relFile, lineNum))
			if openBraces > 0 {
				currentImpl = &implCtx{
					typeName:  typeName,
					traitName: traitName,
					depth:     effectiveDepth,
				}
			}
			pendingDerives = nil
			pendingAttrs = nil
			continue
		}

		// impl Type — set context (must come after implTraitForRe check)
		if m := implRe.FindStringSubmatch(line); m != nil {
			typeName := m[1]
			if openBraces > 0 {
				currentImpl = &implCtx{
					typeName:  typeName,
					traitName: "",
					depth:     effectiveDepth,
				}
			}
			pendingDerives = nil
			pendingAttrs = nil
			continue
		}

		// trait declarations
		if m := traitRe.FindStringSubmatch(line); m != nil {
			pubMod := m[1]
			name := m[3]
			exported := strings.Contains(pubMod, "pub")

			f := facts.Fact{
				Kind: facts.KindSymbol,
				Name: dir + "." + name,
				File: relFile,
				Line: lineNum,
				Props: map[string]any{
					"symbol_kind": facts.SymbolInterface,
					"exported":    exported,
					"language":    "rust",
				},
				Relations: []facts.Relation{
					{Kind: facts.RelDeclares, Target: dir},
				},
			}
			applyPendingAttrs(&f, pendingAttrs)
			result = append(result, f)
			pendingDerives = nil
			pendingAttrs = nil
			continue
		}

		// struct declarations
		if m := structRe.FindStringSubmatch(line); m != nil {
			pubMod := m[1]
			name := m[2]
			exported := strings.Contains(pubMod, "pub")

			f := facts.Fact{
				Kind: facts.KindSymbol,
				Name: dir + "." + name,
				File: relFile,
				Line: lineNum,
				Props: map[string]any{
					"symbol_kind": facts.SymbolStruct,
					"exported":    exported,
					"language":    "rust",
				},
				Relations: []facts.Relation{
					{Kind: facts.RelDeclares, Target: dir},
				},
			}
			// Apply pending derives
			for _, trait := range pendingDerives {
				f.Relations = append(f.Relations, facts.Relation{
					Kind:   facts.RelImplements,
					Target: trait,
				})
			}
			applyPendingAttrs(&f, pendingAttrs)
			result = append(result, f)
			pendingDerives = nil
			pendingAttrs = nil
			continue
		}

		// enum declarations
		if m := enumRe.FindStringSubmatch(line); m != nil {
			pubMod := m[1]
			name := m[2]
			exported := strings.Contains(pubMod, "pub")

			f := facts.Fact{
				Kind: facts.KindSymbol,
				Name: dir + "." + name,
				File: relFile,
				Line: lineNum,
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
			for _, trait := range pendingDerives {
				f.Relations = append(f.Relations, facts.Relation{
					Kind:   facts.RelImplements,
					Target: trait,
				})
			}
			applyPendingAttrs(&f, pendingAttrs)
			result = append(result, f)
			pendingDerives = nil
			pendingAttrs = nil
			continue
		}

		// union declarations
		if m := unionRe.FindStringSubmatch(line); m != nil {
			pubMod := m[1]
			name := m[2]
			exported := strings.Contains(pubMod, "pub")

			f := facts.Fact{
				Kind: facts.KindSymbol,
				Name: dir + "." + name,
				File: relFile,
				Line: lineNum,
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
			for _, trait := range pendingDerives {
				f.Relations = append(f.Relations, facts.Relation{
					Kind:   facts.RelImplements,
					Target: trait,
				})
			}
			applyPendingAttrs(&f, pendingAttrs)
			result = append(result, f)
			pendingDerives = nil
			pendingAttrs = nil
			continue
		}

		// fn declarations (top-level only)
		if m := fnRe.FindStringSubmatch(line); m != nil {
			pubMod := m[1]
			modifiers := m[2]
			name := m[3]
			exported := strings.Contains(pubMod, "pub")

			ff := facts.Fact{
				Kind: facts.KindSymbol,
				Name: dir + "." + name,
				File: relFile,
				Line: lineNum,
				Props: map[string]any{
					"symbol_kind": facts.SymbolFunc,
					"exported":    exported,
					"language":    "rust",
				},
				Relations: []facts.Relation{
					{Kind: facts.RelDeclares, Target: dir},
				},
			}
			if strings.Contains(modifiers, "async") {
				ff.Props["async"] = true
			}
			if strings.Contains(modifiers, "unsafe") {
				ff.Props["unsafe"] = true
			}
			applyPendingAttrs(&ff, pendingAttrs)
			result = append(result, ff)
			// Track this function for call extraction
			if openBraces > 0 {
				currentFn = &fnCtx{factIdx: len(result) - 1, depth: effectiveDepth}
			}
			pendingDerives = nil
			pendingAttrs = nil
			continue
		}

		// const declarations
		if m := constRe.FindStringSubmatch(line); m != nil {
			pubMod := m[1]
			name := m[2]
			if name == "_" {
				pendingAttrs = nil
				continue
			}
			exported := strings.Contains(pubMod, "pub")

			f := facts.Fact{
				Kind: facts.KindSymbol,
				Name: dir + "." + name,
				File: relFile,
				Line: lineNum,
				Props: map[string]any{
					"symbol_kind": facts.SymbolConstant,
					"exported":    exported,
					"language":    "rust",
				},
				Relations: []facts.Relation{
					{Kind: facts.RelDeclares, Target: dir},
				},
			}
			applyPendingAttrs(&f, pendingAttrs)
			result = append(result, f)
			pendingDerives = nil
			pendingAttrs = nil
			continue
		}

		// static declarations
		if m := staticRe.FindStringSubmatch(line); m != nil {
			pubMod := m[1]
			name := m[2]
			exported := strings.Contains(pubMod, "pub")

			f := facts.Fact{
				Kind: facts.KindSymbol,
				Name: dir + "." + name,
				File: relFile,
				Line: lineNum,
				Props: map[string]any{
					"symbol_kind": facts.SymbolVariable,
					"exported":    exported,
					"language":    "rust",
				},
				Relations: []facts.Relation{
					{Kind: facts.RelDeclares, Target: dir},
				},
			}
			applyPendingAttrs(&f, pendingAttrs)
			result = append(result, f)
			pendingDerives = nil
			pendingAttrs = nil
			continue
		}

		// type alias declarations
		if m := typeAliasRe.FindStringSubmatch(line); m != nil {
			pubMod := m[1]
			name := m[2]
			exported := strings.Contains(pubMod, "pub")

			f := facts.Fact{
				Kind: facts.KindSymbol,
				Name: dir + "." + name,
				File: relFile,
				Line: lineNum,
				Props: map[string]any{
					"symbol_kind": facts.SymbolType,
					"exported":    exported,
					"language":    "rust",
				},
				Relations: []facts.Relation{
					{Kind: facts.RelDeclares, Target: dir},
				},
			}
			applyPendingAttrs(&f, pendingAttrs)
			result = append(result, f)
			pendingDerives = nil
			pendingAttrs = nil
			continue
		}
	}

	// Merge impl-trait-for relations onto existing struct/enum facts
	result = mergeImplRelations(result)

	return result
}

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

// extractAttributes extracts non-derive, non-cfg attribute macros from a line.
// Returns the inner content of each #[...] attribute (e.g. "tokio::main", "serde(rename_all = \"camelCase\")").
func extractAttributes(line string) []string {
	matches := attrRe.FindAllStringSubmatch(line, -1)
	var attrs []string
	for _, m := range matches {
		content := strings.TrimSpace(m[1])
		// Skip derive (handled separately) and cfg (handled separately)
		if strings.HasPrefix(content, "derive(") || strings.HasPrefix(content, "cfg(") {
			continue
		}
		// Skip doc attributes (they're comments, not proc macros)
		if strings.HasPrefix(content, "doc") {
			continue
		}
		// Skip macro_export (handled with macro_rules!)
		if content == "macro_export" {
			continue
		}
		attrs = append(attrs, content)
	}
	return attrs
}

// applyPendingAttrs sets the "attributes" property on a fact if there are pending attributes.
func applyPendingAttrs(f *facts.Fact, attrs []string) {
	if len(attrs) > 0 {
		// Copy to avoid sharing the slice
		cp := make([]string, len(attrs))
		copy(cp, attrs)
		f.Props["attributes"] = cp
	}
}

// parseDeriveLine splits "Debug, Clone, PartialEq" into individual trait names.
func parseDeriveLine(s string) []string {
	var result []string
	for _, part := range strings.Split(s, ",") {
		t := strings.TrimSpace(part)
		if t != "" {
			result = append(result, t)
		}
	}
	return result
}

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
