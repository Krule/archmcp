package rustextractor

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dejo1307/archmcp/internal/facts"
)

// --- Module resolution ---

// resolveModRelativeImports reclassifies `use sibling_mod::X` as internal
// when `sibling_mod` matches a `mod sibling_mod;` declaration in the same file.
// Without this, `use utils::helper` (where `utils` is a local module) looks
// like an external crate import.
func resolveModRelativeImports(ff []facts.Fact) []facts.Fact {
	// Collect all mod declarations per directory
	// A mod declaration is a symbol with symbol_kind="module" and mod_style="external"
	modNames := make(map[string]map[string]bool) // dir → set of declared mod names
	for _, f := range ff {
		if f.Kind != facts.KindSymbol {
			continue
		}
		sk, _ := f.Props["symbol_kind"].(string)
		if sk != "module" {
			continue
		}
		dir := filepath.Dir(f.File)
		// Extract mod name from fact name "dir.modname"
		prefix := dir + "."
		if strings.HasPrefix(f.Name, prefix) {
			modName := f.Name[len(prefix):]
			if modNames[dir] == nil {
				modNames[dir] = make(map[string]bool)
			}
			modNames[dir][modName] = true
		}
	}

	// Now check dependency facts: if target matches a local mod, reclassify
	for i := range ff {
		f := &ff[i]
		if f.Kind != facts.KindDependency {
			continue
		}
		source, _ := f.Props["source"].(string)
		if source != "external" {
			continue
		}
		dir := filepath.Dir(f.File)
		mods := modNames[dir]
		if mods == nil {
			continue
		}

		for j := range f.Relations {
			rel := &f.Relations[j]
			if rel.Kind != facts.RelImports {
				continue
			}
			if mods[rel.Target] {
				f.Props["source"] = "internal"
			}
		}
	}

	return ff
}

// resolveCratePathImports rewrites crate:: import targets from Rust path form
// (crate::models::User) to module directory form (src/models).
// It finds the longest matching module directory for each crate:: path.
func resolveCratePathImports(ff []facts.Fact, modules map[string]bool) []facts.Fact {
	for i := range ff {
		f := &ff[i]
		if f.Kind != facts.KindDependency {
			continue
		}
		for j := range f.Relations {
			rel := &f.Relations[j]
			if rel.Kind != facts.RelImports {
				continue
			}
			if !strings.HasPrefix(rel.Target, "crate::") {
				continue
			}

			// crate::models::User → strip "crate::", split on "::"
			remainder := rel.Target[len("crate::"):]
			parts := strings.Split(remainder, "::")

			// Find the file's crate root directory. For a file at "src/handlers/user.rs",
			// walk up to find which parent is a module root containing lib.rs or main.rs.
			// Simplification: use the file's directory, then try "src" prefix.
			fileDir := filepath.Dir(f.File)

			// Find the crate source root by walking up from fileDir
			// looking for the topmost directory that's in the modules set
			crateRoot := findCrateRoot(fileDir, modules)

			// Build candidate paths from longest to shortest
			resolved := ""
			for k := len(parts); k >= 1; k-- {
				candidate := crateRoot + "/" + strings.Join(parts[:k], "/")
				if modules[candidate] {
					resolved = candidate
					break
				}
			}

			if resolved != "" {
				rel.Target = resolved
			}
		}
	}
	return ff
}

// findCrateRoot finds the source root directory for a file.
// For "src/handlers/user.rs" with modules {src, src/handlers}, returns "src".
// For "crates/mylib/src/lib.rs" with modules {crates/mylib/src}, returns "crates/mylib/src".
func findCrateRoot(dir string, modules map[string]bool) string {
	// Walk up parent directories to find the topmost module ancestor
	// that looks like a crate root (contains "src" or is the top-level "src")
	best := dir
	cur := dir
	for {
		parent := filepath.Dir(cur)
		if parent == cur || parent == "." {
			break
		}
		if modules[parent] {
			best = parent
		}
		cur = parent
	}
	return best
}

func isRustFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".rs")
}

// --- Workspace crate map ---

// crateMapEntry maps a crate name to its source directory relative to the repo root.
type crateMapEntry struct {
	name    string   // crate name from [package] name = "..."
	srcDir  string   // relative source dir, e.g. "src" or "crates/sourced-codegen/src"
	binDirs []string // relative dirs for [[bin]] targets, e.g. ["bin"]
}

// buildCrateMap discovers workspace member crates and builds a map from
// crate name → source directory. This enables resolving cross-crate imports
// like `use sourced::parser::ast` to the actual module path `src/parser`.
func buildCrateMap(repoPath string) map[string]crateMapEntry {
	result := make(map[string]crateMapEntry)

	rootCargoPath := filepath.Join(repoPath, "Cargo.toml")
	rootData, err := os.ReadFile(rootCargoPath)
	if err != nil {
		return result
	}

	rootContent := string(rootData)

	// Check if the root Cargo.toml has a [package] with a name
	if rootName := parsePackageName(rootContent); rootName != "" {
		entry := parseCrateEntry(rootName, "", rootContent)
		result[rootName] = entry
	}

	// Discover workspace members from [workspace] members = [...]
	members := parseWorkspaceMembers(rootContent)
	for _, pattern := range members {
		// Expand glob patterns like "crates/*"
		matches, err := filepath.Glob(filepath.Join(repoPath, pattern))
		if err != nil {
			continue
		}
		for _, memberDir := range matches {
			memberCargo := filepath.Join(memberDir, "Cargo.toml")
			data, err := os.ReadFile(memberCargo)
			if err != nil {
				continue
			}
			name := parsePackageName(string(data))
			if name == "" {
				continue
			}
			relDir, err := filepath.Rel(repoPath, memberDir)
			if err != nil {
				continue
			}
			entry := parseCrateEntry(name, relDir, string(data))
			result[name] = entry
		}
	}

	return result
}

// parseCrateEntry builds a crateMapEntry from a Cargo.toml's content.
// relDir is the crate's directory relative to the repo root ("" for root crate).
func parseCrateEntry(name, relDir, cargoContent string) crateMapEntry {
	entry := crateMapEntry{
		name:   name,
		srcDir: filepath.ToSlash(filepath.Join(relDir, "src")),
	}

	// Custom [lib] path overrides default "src"
	if libPath := parseLibPath(cargoContent); libPath != "" {
		entry.srcDir = filepath.ToSlash(filepath.Join(relDir, filepath.Dir(libPath)))
	}

	// Custom [[bin]] paths
	for _, bp := range parseBinPaths(cargoContent) {
		entry.binDirs = append(entry.binDirs, filepath.ToSlash(filepath.Join(relDir, filepath.Dir(bp))))
	}
	entry.binDirs = dedup(entry.binDirs)

	return entry
}

var (
	packageNameRe = regexp.MustCompile(`(?m)^\s*name\s*=\s*"([^"]+)"`)
	pathValueRe   = regexp.MustCompile(`(?m)^\s*path\s*=\s*"([^"]+)"`)
)

// parsePackageName extracts the package name from Cargo.toml content.
// Returns "" if no [package] name found.
func parsePackageName(content string) string {
	// Only look at lines after [package] and before the next section
	inPackage := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[package]" {
			inPackage = true
			continue
		}
		if inPackage && strings.HasPrefix(trimmed, "[") {
			break
		}
		if inPackage {
			if m := packageNameRe.FindStringSubmatch(trimmed); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

// parseWorkspaceMembers extracts member patterns from [workspace] members = [...].
// Handles both single-line and multi-line arrays.
func parseWorkspaceMembers(content string) []string {
	lines := strings.Split(content, "\n")
	inWorkspace := false
	inMembers := false
	var raw strings.Builder

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track [workspace] section
		if trimmed == "[workspace]" {
			inWorkspace = true
			continue
		}
		if inWorkspace && strings.HasPrefix(trimmed, "[") && trimmed != "[workspace]" {
			break
		}
		if !inWorkspace {
			continue
		}

		// Detect start of members = [...]
		if strings.HasPrefix(trimmed, "members") && strings.Contains(trimmed, "=") {
			idx := strings.Index(trimmed, "[")
			if idx == -1 {
				continue
			}
			rest := trimmed[idx+1:]
			if end := strings.Index(rest, "]"); end != -1 {
				// Single-line: members = ["a", "b"]
				raw.WriteString(rest[:end])
				break
			}
			// Multi-line start
			raw.WriteString(rest)
			inMembers = true
			continue
		}

		if inMembers {
			if end := strings.Index(trimmed, "]"); end != -1 {
				raw.WriteString(trimmed[:end])
				break
			}
			raw.WriteString(trimmed)
			raw.WriteByte(',') // ensure comma separation between lines
		}
	}

	if raw.Len() == 0 {
		return nil
	}

	var members []string
	for _, part := range strings.Split(raw.String(), ",") {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, "\"")
		if part != "" {
			members = append(members, part)
		}
	}
	return members
}

// parseLibPath extracts the path from [lib] section in Cargo.toml.
// Returns "" if no custom lib path is set.
func parseLibPath(content string) string {
	return parseSectionPath(content, "[lib]")
}

// parseBinPaths extracts all paths from [[bin]] sections in Cargo.toml.
func parseBinPaths(content string) []string {
	var paths []string
	lines := strings.Split(content, "\n")
	inBin := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[[bin]]" {
			inBin = true
			continue
		}
		if inBin && strings.HasPrefix(trimmed, "[") {
			inBin = false
			// Could be another [[bin]] section
			if trimmed == "[[bin]]" {
				inBin = true
			}
			continue
		}
		if inBin {
			if m := pathValueRe.FindStringSubmatch(trimmed); m != nil {
				paths = append(paths, m[1])
			}
		}
	}
	return paths
}

// parseSectionPath extracts `path = "..."` from a specific TOML section.
func parseSectionPath(content, section string) string {
	lines := strings.Split(content, "\n")
	inSection := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == section {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(trimmed, "[") {
			break
		}
		if inSection {
			if m := pathValueRe.FindStringSubmatch(trimmed); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

// buildModuleHierarchy creates module facts from `mod foo;` declarations.
// parentDir is the directory containing the declaring file.
// modNames are the declared module names (e.g., from `mod handlers;`).
// declaringFile is the file that contains the mod declarations.
func buildModuleHierarchy(parentDir string, modNames []string, declaringFile string) []facts.Fact {
	var result []facts.Fact
	for _, name := range modNames {
		childPath := parentDir + "/" + name
		result = append(result, facts.Fact{
			Kind: facts.KindModule,
			Name: childPath,
			File: declaringFile,
			Props: map[string]any{
				"language":      "rust",
				"parent_module": parentDir,
			},
			Relations: []facts.Relation{
				{Kind: facts.RelDeclares, Target: parentDir},
			},
		})
	}
	return result
}

// dedup returns a slice with duplicate strings removed, preserving order.
func dedup(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(ss))
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// resolveCrateImports rewrites dependency fact targets for cross-crate imports.
// For example, `use sourced::parser::ast` with crateMap["sourced"] = {srcDir: "src"}
// becomes a dependency with target "src/parser" (matching the module directory structure).
func resolveCrateImports(ff []facts.Fact, crateMap map[string]crateMapEntry) []facts.Fact {
	for i := range ff {
		f := &ff[i]
		if f.Kind != facts.KindDependency {
			continue
		}

		for j := range f.Relations {
			rel := &f.Relations[j]
			if rel.Kind != facts.RelImports {
				continue
			}

			// Check if the import target's first segment matches a known crate
			target := rel.Target
			// For external imports, target is already just the crate name (e.g. "sourced")
			// For internal imports (crate::, self::, super::), skip — already handled
			source, _ := f.Props["source"].(string)
			if source == "internal" {
				continue
			}

			entry, ok := crateMap[target]
			if !ok {
				continue
			}

			// Now we need the full import path from the original use statement
			// The dependency fact Name is "dir -> target", extract the original path
			// from the fact. We need to look at the original import path.
			// Unfortunately extractUseDecl strips the path to just the crate name for externals.
			// We need to preserve the full path. Let's check if we stored it.
			// For now, resolve "sourced" → "src" (the crate's source dir)
			// This at least creates a module→module edge.

			// If the import has sub-path info, resolve deeper.
			// The fact name contains "dir -> target", and target is the crate name.
			// We stored the full path in a prop if available.
			if fullPath, ok := f.Props["import_path"].(string); ok {
				// fullPath is like "sourced::parser" — resolve to "src/parser"
				parts := strings.SplitN(fullPath, "::", 2)
				if len(parts) == 2 {
					subPath := strings.ReplaceAll(parts[1], "::", "/")
					rel.Target = entry.srcDir + "/" + subPath
				} else {
					rel.Target = entry.srcDir
				}
			} else {
				rel.Target = entry.srcDir
			}

			// Mark as internal since it's a workspace dependency
			f.Props["source"] = "internal"
		}
	}

	return ff
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
