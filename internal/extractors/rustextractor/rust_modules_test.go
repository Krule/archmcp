package rustextractor

// Tests for module detection and resolution helpers.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dejo1307/archmcp/internal/facts"
)

// --- Acceptance Tests (Outer Loop) ---

// TestAcceptance_CrateAwareModuleNaming verifies that buildCrateMap
// correctly extracts crate names from Cargo.toml [package] sections
// and maps them to their source directories.
func TestAcceptance_CrateAwareModuleNaming(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "Cargo.toml", `[package]
name = "myapp"
version = "0.1.0"

[workspace]
members = ["crates/mylib"]
`)
	mkTestDir(t, dir, "crates/mylib")
	writeTestFile(t, dir, "crates/mylib/Cargo.toml", `[package]
name = "mylib"
version = "0.1.0"
`)

	cm := buildCrateMap(dir)

	entry, ok := cm["myapp"]
	if !ok {
		t.Fatal("expected crateMap to contain 'myapp'")
	}
	if entry.srcDir != "src" {
		t.Errorf("myapp srcDir = %q, want %q", entry.srcDir, "src")
	}

	entry, ok = cm["mylib"]
	if !ok {
		t.Fatal("expected crateMap to contain 'mylib'")
	}
	if entry.srcDir != "crates/mylib/src" {
		t.Errorf("mylib srcDir = %q, want %q", entry.srcDir, "crates/mylib/src")
	}
}

// TestAcceptance_CustomLibBinPaths verifies that buildCrateMap respects
// custom [lib] path and [[bin]] path settings in Cargo.toml.
func TestAcceptance_CustomLibBinPaths(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "Cargo.toml", `[package]
name = "custom-paths"
version = "0.1.0"

[lib]
path = "lib/core.rs"

[[bin]]
name = "cli"
path = "bin/main.rs"
`)

	cm := buildCrateMap(dir)

	entry, ok := cm["custom-paths"]
	if !ok {
		t.Fatal("expected crateMap to contain 'custom-paths'")
	}
	// When custom lib path is set, srcDir should be the directory of that path
	if entry.srcDir != "lib" {
		t.Errorf("srcDir = %q, want %q", entry.srcDir, "lib")
	}
	// Check that custom bin paths are tracked
	if len(entry.binDirs) == 0 {
		t.Fatal("expected binDirs to be non-empty")
	}
	if entry.binDirs[0] != "bin" {
		t.Errorf("binDirs[0] = %q, want %q", entry.binDirs[0], "bin")
	}
}

// TestAcceptance_WorkspaceGlobExpansion verifies that workspace member
// glob patterns like "crates/*" are expanded correctly.
func TestAcceptance_WorkspaceGlobExpansion(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "Cargo.toml", `[workspace]
members = ["crates/*"]
`)
	for _, name := range []string{"alpha", "beta"} {
		mkTestDir(t, dir, "crates/"+name)
		writeTestFile(t, dir, "crates/"+name+"/Cargo.toml", `[package]
name = "`+name+`"
version = "0.1.0"
`)
	}

	cm := buildCrateMap(dir)

	for _, name := range []string{"alpha", "beta"} {
		entry, ok := cm[name]
		if !ok {
			t.Errorf("expected crateMap to contain %q", name)
			continue
		}
		want := "crates/" + name + "/src"
		if entry.srcDir != want {
			t.Errorf("%s srcDir = %q, want %q", name, entry.srcDir, want)
		}
	}
}

// TestAcceptance_ModuleHierarchyFromModDecls verifies that mod declarations
// produce parent→child module relations.
func TestAcceptance_ModuleHierarchyFromModDecls(t *testing.T) {
	modFacts := buildModuleHierarchy(
		"src",
		[]string{"handlers", "models"},
		"src/lib.rs",
	)

	if len(modFacts) != 2 {
		t.Fatalf("got %d module facts, want 2", len(modFacts))
	}

	names := map[string]bool{}
	for _, mf := range modFacts {
		names[mf.Name] = true
		if mf.Kind != facts.KindModule {
			t.Errorf("fact kind = %q, want %q", mf.Kind, facts.KindModule)
		}
		found := false
		for _, rel := range mf.Relations {
			if rel.Kind == facts.RelDeclares && rel.Target == "src" {
				found = true
			}
		}
		if !found {
			t.Errorf("module %q missing declares→src relation", mf.Name)
		}
	}

	if !names["src/handlers"] {
		t.Error("missing module src/handlers")
	}
	if !names["src/models"] {
		t.Error("missing module src/models")
	}
}

// --- Unit Tests (Inner Loop) ---

// TestUnit_ParseLibPath verifies parsing [lib] path from Cargo.toml.
func TestUnit_ParseLibPath(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "standard lib path",
			content: `[package]
name = "mylib"

[lib]
path = "lib/core.rs"
`,
			want: "lib/core.rs",
		},
		{
			name: "no lib section",
			content: `[package]
name = "mylib"
`,
			want: "",
		},
		{
			name: "lib section without path",
			content: `[package]
name = "mylib"

[lib]
name = "mylib"
`,
			want: "",
		},
		{
			name: "lib section after dependencies",
			content: `[package]
name = "mylib"

[dependencies]
serde = "1"

[lib]
path = "custom/lib.rs"
`,
			want: "custom/lib.rs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLibPath(tt.content)
			if got != tt.want {
				t.Errorf("parseLibPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestUnit_ParseBinPaths verifies parsing [[bin]] paths from Cargo.toml.
func TestUnit_ParseBinPaths(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name: "two bins",
			content: `[package]
name = "myapp"

[[bin]]
name = "cli"
path = "bin/main.rs"

[[bin]]
name = "server"
path = "bin/server.rs"
`,
			want: []string{"bin/main.rs", "bin/server.rs"},
		},
		{
			name: "no bins",
			content: `[package]
name = "myapp"
`,
			want: nil,
		},
		{
			name:    "single bin",
			content: "[[bin]]\nname = \"tool\"\npath = \"src/bin/tool.rs\"\n",
			want:    []string{"src/bin/tool.rs"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBinPaths(tt.content)
			if len(got) != len(tt.want) {
				t.Fatalf("parseBinPaths() returned %d, want %d: %v", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestUnit_ParseWorkspaceMembers_Multiline verifies multi-line member arrays.
func TestUnit_ParseWorkspaceMembers_Multiline(t *testing.T) {
	content := `[workspace]
members = [
    "crates/alpha",
    "crates/beta",
    "crates/gamma",
]
`
	got := parseWorkspaceMembers(content)
	want := []string{"crates/alpha", "crates/beta", "crates/gamma"}
	if len(got) != len(want) {
		t.Fatalf("got %d members, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("member[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestUnit_ParseWorkspaceMembers_SingleLine ensures single-line still works.
func TestUnit_ParseWorkspaceMembers_SingleLine(t *testing.T) {
	content := `[workspace]
members = ["foo", "bar"]
`
	got := parseWorkspaceMembers(content)
	want := []string{"foo", "bar"}
	if len(got) != len(want) {
		t.Fatalf("got %d members, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("member[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestUnit_BuildModuleHierarchy_Empty handles no mod declarations.
func TestUnit_BuildModuleHierarchy_Empty(t *testing.T) {
	mf := buildModuleHierarchy("src", nil, "src/lib.rs")
	if len(mf) != 0 {
		t.Errorf("expected 0 facts, got %d", len(mf))
	}
}

// TestUnit_BuildModuleHierarchy_SingleMod handles one mod declaration.
func TestUnit_BuildModuleHierarchy_SingleMod(t *testing.T) {
	mf := buildModuleHierarchy("src", []string{"utils"}, "src/main.rs")
	if len(mf) != 1 {
		t.Fatalf("got %d facts, want 1", len(mf))
	}
	if mf[0].Name != "src/utils" {
		t.Errorf("name = %q, want %q", mf[0].Name, "src/utils")
	}
	if mf[0].Kind != facts.KindModule {
		t.Errorf("kind = %q, want %q", mf[0].Kind, facts.KindModule)
	}
	// Check props
	if lang, _ := mf[0].Props["language"].(string); lang != "rust" {
		t.Errorf("language = %q, want %q", lang, "rust")
	}
	if parent, _ := mf[0].Props["parent_module"].(string); parent != "src" {
		t.Errorf("parent_module = %q, want %q", parent, "src")
	}
}

// TestUnit_ParsePackageName ensures existing parser still works.
func TestUnit_ParsePackageName(t *testing.T) {
	tests := []struct {
		content string
		want    string
	}{
		{`[package]\nname = "foo"`, ""},        // escaped newlines aren't real newlines
		{"[package]\nname = \"foo\"", "foo"},   // real newlines
		{"[dependencies]\nname = \"bar\"", ""}, // wrong section
		{"", ""},
	}
	for _, tt := range tests {
		got := parsePackageName(tt.content)
		if got != tt.want {
			t.Errorf("parsePackageName(%q) = %q, want %q", tt.content, got, tt.want)
		}
	}
}

// --- Test helpers ---

func writeTestFile(t *testing.T, base, rel, content string) {
	t.Helper()
	full := filepath.Join(base, rel)
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkTestDir(t *testing.T, base, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(base, rel), 0o755); err != nil {
		t.Fatal(err)
	}
}
