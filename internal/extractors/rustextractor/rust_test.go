package rustextractor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dejo1307/archmcp/internal/facts"
)

// --- helpers ---

// extractFromString writes Rust source to a temp file and runs extractFile.
func extractFromString(t *testing.T, src string) []facts.Fact {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.rs")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	return extractFile(f, "src/test.rs")
}

func findFact(ff []facts.Fact, name string) (facts.Fact, bool) {
	for _, f := range ff {
		if f.Name == name {
			return f, true
		}
	}
	return facts.Fact{}, false
}

func findFactByKind(ff []facts.Fact, kind string) []facts.Fact {
	var result []facts.Fact
	for _, f := range ff {
		if f.Kind == kind {
			result = append(result, f)
		}
	}
	return result
}

func hasRelation(f facts.Fact, relKind, target string) bool {
	for _, r := range f.Relations {
		if r.Kind == relKind && r.Target == target {
			return true
		}
	}
	return false
}

// --- Unit tests ---

func TestDetect_CargoToml(t *testing.T) {
	dir := t.TempDir()
	ext := New()

	// No Cargo.toml -> false
	detected, err := ext.Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if detected {
		t.Error("Detect() = true for empty dir, want false")
	}

	// With Cargo.toml -> true
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	detected, err = ext.Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !detected {
		t.Error("Detect() = false with Cargo.toml, want true")
	}
}

func TestDetect_CargoWorkspace(t *testing.T) {
	dir := t.TempDir()
	ext := New()

	// Cargo.toml with [workspace] -> true
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[workspace]\nmembers = [\"crate-a\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	detected, err := ext.Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !detected {
		t.Error("Detect() = false with workspace Cargo.toml, want true")
	}
}

// --- Unit tests for extractFile ---

func TestExtractFile_PubFn(t *testing.T) {
	ff := extractFromString(t, `pub fn serve(port: u16) {
    println!("serving on {}", port);
}

fn helper() {}
`)
	f, ok := findFact(ff, "src.serve")
	if !ok {
		t.Fatal("expected fact for src.serve")
	}
	if f.Props["symbol_kind"] != facts.SymbolFunc {
		t.Errorf("symbol_kind = %v, want function", f.Props["symbol_kind"])
	}
	if f.Props["exported"] != true {
		t.Errorf("exported = %v, want true", f.Props["exported"])
	}

	h, ok := findFact(ff, "src.helper")
	if !ok {
		t.Fatal("expected fact for src.helper")
	}
	if h.Props["exported"] != false {
		t.Errorf("helper exported = %v, want false", h.Props["exported"])
	}
}

func TestExtractFile_AsyncFn(t *testing.T) {
	ff := extractFromString(t, `pub async fn fetch_data() -> Result<String, Error> {
    Ok("data".into())
}
`)
	f, ok := findFact(ff, "src.fetch_data")
	if !ok {
		t.Fatal("expected fact for src.fetch_data")
	}
	if f.Props["async"] != true {
		t.Errorf("async = %v, want true", f.Props["async"])
	}
}

func TestExtractFile_UnsafeFn(t *testing.T) {
	ff := extractFromString(t, `pub unsafe fn dangerous() {}
`)
	f, ok := findFact(ff, "src.dangerous")
	if !ok {
		t.Fatal("expected fact for src.dangerous")
	}
	if f.Props["unsafe"] != true {
		t.Errorf("unsafe = %v, want true", f.Props["unsafe"])
	}
}

func TestExtractFile_Struct(t *testing.T) {
	ff := extractFromString(t, `pub struct Config {
    pub host: String,
    port: u16,
}
`)
	f, ok := findFact(ff, "src.Config")
	if !ok {
		t.Fatal("expected fact for src.Config")
	}
	if f.Props["symbol_kind"] != facts.SymbolStruct {
		t.Errorf("symbol_kind = %v, want struct", f.Props["symbol_kind"])
	}
	if f.Props["exported"] != true {
		t.Errorf("exported = %v, want true", f.Props["exported"])
	}
}

func TestExtractFile_Enum(t *testing.T) {
	ff := extractFromString(t, `pub enum Color {
    Red,
    Green,
    Blue,
}
`)
	f, ok := findFact(ff, "src.Color")
	if !ok {
		t.Fatal("expected fact for src.Color")
	}
	if f.Props["symbol_kind"] != facts.SymbolType {
		t.Errorf("symbol_kind = %v, want type", f.Props["symbol_kind"])
	}
	if f.Props["enum"] != true {
		t.Errorf("enum = %v, want true", f.Props["enum"])
	}
}

func TestExtractFile_Trait(t *testing.T) {
	ff := extractFromString(t, `pub trait Handler {
    fn handle(&self, req: Request) -> Response;
}
`)
	f, ok := findFact(ff, "src.Handler")
	if !ok {
		t.Fatal("expected fact for src.Handler")
	}
	if f.Props["symbol_kind"] != facts.SymbolInterface {
		t.Errorf("symbol_kind = %v, want interface", f.Props["symbol_kind"])
	}
	if f.Props["exported"] != true {
		t.Errorf("exported = %v, want true", f.Props["exported"])
	}
}

func TestExtractFile_Union(t *testing.T) {
	ff := extractFromString(t, `pub union MyUnion {
    f1: u32,
    f2: f32,
}
`)
	f, ok := findFact(ff, "src.MyUnion")
	if !ok {
		t.Fatal("expected fact for src.MyUnion")
	}
	if f.Props["symbol_kind"] != facts.SymbolStruct {
		t.Errorf("symbol_kind = %v, want struct", f.Props["symbol_kind"])
	}
	if f.Props["union"] != true {
		t.Errorf("union = %v, want true", f.Props["union"])
	}
}

func TestExtractFile_AttributeMacroOnFn(t *testing.T) {
	ff := extractFromString(t, `#[tokio::main]
async fn main() {}

#[test]
fn it_works() {}

#[inline(always)]
pub fn hot() {}
`)
	mainFn, ok := findFact(ff, "src.main")
	if !ok {
		t.Fatal("expected fact for src.main")
	}
	mainAttrs, _ := mainFn.Props["attributes"].([]string)
	if !containsStr(mainAttrs, "tokio::main") {
		t.Errorf("main missing tokio::main attribute, got %v", mainAttrs)
	}

	itWorks, ok := findFact(ff, "src.it_works")
	if !ok {
		t.Fatal("expected fact for src.it_works")
	}
	itWorksAttrs, _ := itWorks.Props["attributes"].([]string)
	if !containsStr(itWorksAttrs, "test") {
		t.Errorf("it_works missing test attribute, got %v", itWorksAttrs)
	}

	hot, ok := findFact(ff, "src.hot")
	if !ok {
		t.Fatal("expected fact for src.hot")
	}
	hotAttrs, _ := hot.Props["attributes"].([]string)
	if !containsStr(hotAttrs, "inline(always)") {
		t.Errorf("hot missing inline(always) attribute, got %v", hotAttrs)
	}
}

func TestExtractFile_AttributeMacroOnStruct(t *testing.T) {
	ff := extractFromString(t, `#[derive(Debug)]
#[serde(rename_all = "camelCase")]
pub struct Config {
    pub host: String,
}
`)
	config, ok := findFact(ff, "src.Config")
	if !ok {
		t.Fatal("expected fact for src.Config")
	}
	// Should have derive relation
	if !hasRelation(config, facts.RelImplements, "Debug") {
		t.Error("Config missing implements relation for Debug")
	}
	// Should have serde attribute
	attrs, _ := config.Props["attributes"].([]string)
	if !containsStr(attrs, "serde(rename_all = \"camelCase\")") {
		t.Errorf("Config missing serde attribute, got %v", attrs)
	}
}

func TestExtractFile_MultipleAttributes(t *testing.T) {
	ff := extractFromString(t, `#[allow(dead_code)]
#[deprecated(since = "1.0", note = "use new_fn")]
fn old_fn() {}
`)
	oldFn, ok := findFact(ff, "src.old_fn")
	if !ok {
		t.Fatal("expected fact for src.old_fn")
	}
	attrs, _ := oldFn.Props["attributes"].([]string)
	if !containsStr(attrs, "allow(dead_code)") {
		t.Errorf("old_fn missing allow(dead_code), got %v", attrs)
	}
	if !containsStr(attrs, "deprecated(since = \"1.0\", note = \"use new_fn\")") {
		t.Errorf("old_fn missing deprecated, got %v", attrs)
	}
}

func TestExtractFile_UnionWithDerive(t *testing.T) {
	ff := extractFromString(t, `#[derive(Copy, Clone)]
pub union Bits {
    f: f32,
    i: i32,
}
`)
	f, ok := findFact(ff, "src.Bits")
	if !ok {
		t.Fatal("expected fact for src.Bits")
	}
	for _, trait := range []string{"Copy", "Clone"} {
		if !hasRelation(f, facts.RelImplements, trait) {
			t.Errorf("Bits missing implements relation for %s", trait)
		}
	}
}

func TestExtractFile_ImplBlock(t *testing.T) {
	ff := extractFromString(t, `pub struct Server {}

impl Server {
    pub fn start(&self) {}
}
`)
	// The struct should still be extracted
	f, ok := findFact(ff, "src.Server")
	if !ok {
		t.Fatal("expected fact for src.Server")
	}
	if f.Props["symbol_kind"] != facts.SymbolStruct {
		t.Errorf("symbol_kind = %v, want struct", f.Props["symbol_kind"])
	}

	// Method inside impl block should be extracted
	m, ok := findFact(ff, "src.Server.start")
	if !ok {
		t.Fatal("expected method fact for src.Server.start")
	}
	if m.Props["symbol_kind"] != facts.SymbolMethod {
		t.Errorf("symbol_kind = %v, want method", m.Props["symbol_kind"])
	}
	if m.Props["receiver"] != "Server" {
		t.Errorf("receiver = %v, want Server", m.Props["receiver"])
	}
	if m.Props["exported"] != true {
		t.Errorf("exported = %v, want true", m.Props["exported"])
	}
}

func TestExtractFile_ImplTraitForType(t *testing.T) {
	ff := extractFromString(t, `pub struct Dog {}

pub trait Animal {
    fn speak(&self);
}

impl Animal for Dog {
    fn speak(&self) {}
}
`)
	dog, ok := findFact(ff, "src.Dog")
	if !ok {
		t.Fatal("expected fact for src.Dog")
	}
	if !hasRelation(dog, facts.RelImplements, "Animal") {
		t.Error("Dog missing implements relation for Animal")
	}

	// The method inside "impl Animal for Dog" should be extracted
	speak, ok := findFact(ff, "src.Dog.speak")
	if !ok {
		t.Fatal("expected method fact for src.Dog.speak")
	}
	if speak.Props["symbol_kind"] != facts.SymbolMethod {
		t.Errorf("speak symbol_kind = %v, want method", speak.Props["symbol_kind"])
	}
	if speak.Props["receiver"] != "Dog" {
		t.Errorf("speak receiver = %v, want Dog", speak.Props["receiver"])
	}
	if speak.Props["trait"] != "Animal" {
		t.Errorf("speak trait = %v, want Animal", speak.Props["trait"])
	}
}

func TestExtractFile_ImplAsyncMethod(t *testing.T) {
	ff := extractFromString(t, `pub struct Client {}

impl Client {
    pub async fn fetch(&self) -> String {
        "data".into()
    }
}
`)
	m, ok := findFact(ff, "src.Client.fetch")
	if !ok {
		t.Fatal("expected method fact for src.Client.fetch")
	}
	if m.Props["symbol_kind"] != facts.SymbolMethod {
		t.Errorf("symbol_kind = %v, want method", m.Props["symbol_kind"])
	}
	if m.Props["async"] != true {
		t.Errorf("async = %v, want true", m.Props["async"])
	}
	if m.Props["receiver"] != "Client" {
		t.Errorf("receiver = %v, want Client", m.Props["receiver"])
	}
}

func TestExtractFile_ImplPrivateMethod(t *testing.T) {
	ff := extractFromString(t, `pub struct Db {}

impl Db {
    pub fn query(&self) {}
    fn internal_check(&self) {}
}
`)
	pub, ok := findFact(ff, "src.Db.query")
	if !ok {
		t.Fatal("expected method fact for src.Db.query")
	}
	if pub.Props["exported"] != true {
		t.Errorf("query exported = %v, want true", pub.Props["exported"])
	}

	priv, ok := findFact(ff, "src.Db.internal_check")
	if !ok {
		t.Fatal("expected method fact for src.Db.internal_check")
	}
	if priv.Props["exported"] != false {
		t.Errorf("internal_check exported = %v, want false", priv.Props["exported"])
	}
}

func TestExtractFile_ImplUnsafeMethod(t *testing.T) {
	ff := extractFromString(t, `pub struct RawPtr {}

impl RawPtr {
    pub unsafe fn deref(&self) -> u8 {
        0
    }
}
`)
	m, ok := findFact(ff, "src.RawPtr.deref")
	if !ok {
		t.Fatal("expected method fact for src.RawPtr.deref")
	}
	if m.Props["unsafe"] != true {
		t.Errorf("unsafe = %v, want true", m.Props["unsafe"])
	}
}

func TestExtractFile_DeriveAttributes(t *testing.T) {
	ff := extractFromString(t, `#[derive(Debug, Clone, PartialEq)]
pub struct Point {
    x: f64,
    y: f64,
}
`)
	f, ok := findFact(ff, "src.Point")
	if !ok {
		t.Fatal("expected fact for src.Point")
	}
	for _, trait := range []string{"Debug", "Clone", "PartialEq"} {
		if !hasRelation(f, facts.RelImplements, trait) {
			t.Errorf("Point missing implements relation for %s", trait)
		}
	}
}

func TestExtractFile_UseStatements(t *testing.T) {
	ff := extractFromString(t, `use std::collections::HashMap;
use crate::models::User;
use serde::Serialize;
use self::helpers::format;
use super::config::Settings;
`)
	deps := findFactByKind(ff, facts.KindDependency)
	if len(deps) == 0 {
		t.Fatal("expected dependency facts")
	}

	// External: std, serde
	foundStd := false
	foundSerde := false
	// Internal: crate::, self::, super::
	foundCrate := false
	foundSelf := false
	foundSuper := false
	for _, d := range deps {
		for _, r := range d.Relations {
			if r.Kind != facts.RelImports {
				continue
			}
			switch {
			case r.Target == "std":
				foundStd = true
			case r.Target == "serde":
				foundSerde = true
			case r.Target == "crate::models::User" || r.Target == "models/User":
				foundCrate = true
			case r.Target == "self::helpers::format" || r.Target == "helpers/format":
				foundSelf = true
			case r.Target == "super::config::Settings" || r.Target == "config/Settings":
				foundSuper = true
			}
		}
	}
	if !foundStd {
		t.Error("expected import for std")
	}
	if !foundSerde {
		t.Error("expected import for serde")
	}
	if !foundCrate {
		t.Error("expected internal import for crate::models::User")
	}
	if !foundSelf {
		t.Error("expected internal import for self::helpers::format")
	}
	if !foundSuper {
		t.Error("expected internal import for super::config::Settings")
	}
}

func TestExtractFile_ConstAndStatic(t *testing.T) {
	ff := extractFromString(t, `pub const MAX: usize = 100;
const INTERNAL: &str = "x";
pub static COUNTER: AtomicUsize = AtomicUsize::new(0);
static mut BUFFER: [u8; 1024] = [0; 1024];
`)
	max, ok := findFact(ff, "src.MAX")
	if !ok {
		t.Fatal("expected fact for src.MAX")
	}
	if max.Props["symbol_kind"] != facts.SymbolConstant {
		t.Errorf("MAX symbol_kind = %v, want constant", max.Props["symbol_kind"])
	}
	if max.Props["exported"] != true {
		t.Errorf("MAX exported = %v, want true", max.Props["exported"])
	}

	internal, ok := findFact(ff, "src.INTERNAL")
	if !ok {
		t.Fatal("expected fact for src.INTERNAL")
	}
	if internal.Props["exported"] != false {
		t.Errorf("INTERNAL exported = %v, want false", internal.Props["exported"])
	}

	counter, ok := findFact(ff, "src.COUNTER")
	if !ok {
		t.Fatal("expected fact for src.COUNTER")
	}
	if counter.Props["symbol_kind"] != facts.SymbolVariable {
		t.Errorf("COUNTER symbol_kind = %v, want variable", counter.Props["symbol_kind"])
	}

	buffer, ok := findFact(ff, "src.BUFFER")
	if !ok {
		t.Fatal("expected fact for src.BUFFER")
	}
	if buffer.Props["symbol_kind"] != facts.SymbolVariable {
		t.Errorf("BUFFER symbol_kind = %v, want variable", buffer.Props["symbol_kind"])
	}
}

func TestExtractFile_TypeAlias(t *testing.T) {
	ff := extractFromString(t, `pub type Result<T> = std::result::Result<T, MyError>;
type BoxFuture<T> = Box<dyn Future<Output = T>>;
`)
	r, ok := findFact(ff, "src.Result")
	if !ok {
		t.Fatal("expected fact for src.Result")
	}
	if r.Props["symbol_kind"] != facts.SymbolType {
		t.Errorf("Result symbol_kind = %v, want type", r.Props["symbol_kind"])
	}
	if r.Props["exported"] != true {
		t.Errorf("Result exported = %v, want true", r.Props["exported"])
	}

	bf, ok := findFact(ff, "src.BoxFuture")
	if !ok {
		t.Fatal("expected fact for src.BoxFuture")
	}
	if bf.Props["exported"] != false {
		t.Errorf("BoxFuture exported = %v, want false", bf.Props["exported"])
	}
}

func TestExtractFile_MacroRules(t *testing.T) {
	ff := extractFromString(t, `macro_rules! vec_of_strings {
    ($($x:expr),*) => {
        vec![$($x.to_string()),*]
    };
}
`)
	f, ok := findFact(ff, "src.vec_of_strings")
	if !ok {
		t.Fatal("expected fact for src.vec_of_strings")
	}
	if f.Props["symbol_kind"] != facts.SymbolFunc {
		t.Errorf("symbol_kind = %v, want function", f.Props["symbol_kind"])
	}
	if f.Props["macro"] != true {
		t.Errorf("macro = %v, want true", f.Props["macro"])
	}
}

func TestExtractFile_CfgTestSkipped(t *testing.T) {
	ff := extractFromString(t, `pub struct RealStruct {}

#[cfg(test)]
mod tests {
    use super::*;

    struct MockStruct {}

    fn helper() {}

    #[test]
    fn it_works() {}
}
`)
	// RealStruct should be extracted
	if _, ok := findFact(ff, "src.RealStruct"); !ok {
		t.Error("expected fact for src.RealStruct")
	}
	// MockStruct from #[cfg(test)] should NOT be extracted
	if _, ok := findFact(ff, "src.MockStruct"); ok {
		t.Error("MockStruct from #[cfg(test)] block should not be extracted")
	}
	if _, ok := findFact(ff, "src.helper"); ok {
		t.Error("helper from #[cfg(test)] block should not be extracted")
	}
}

func TestExtractFile_NestedBraces(t *testing.T) {
	// Only top-level items should be extracted
	ff := extractFromString(t, `pub fn outer() {
    struct Inner {}
    fn nested() {}
}

pub struct TopLevel {}
`)
	if _, ok := findFact(ff, "src.outer"); !ok {
		t.Error("expected top-level fn outer")
	}
	if _, ok := findFact(ff, "src.TopLevel"); !ok {
		t.Error("expected top-level struct TopLevel")
	}
	if _, ok := findFact(ff, "src.Inner"); ok {
		t.Error("nested struct Inner should not be extracted")
	}
	if _, ok := findFact(ff, "src.nested"); ok {
		t.Error("nested fn should not be extracted")
	}
}

func TestExtractFile_ImplNestedFnNotExtracted(t *testing.T) {
	// fn inside fn inside impl should NOT be extracted (depth > impl+1)
	ff := extractFromString(t, `pub struct Svc {}

impl Svc {
    pub fn run(&self) {
        fn local_helper() {}
    }
}
`)
	if _, ok := findFact(ff, "src.Svc.run"); !ok {
		t.Fatal("expected method src.Svc.run")
	}
	if _, ok := findFact(ff, "src.Svc.local_helper"); ok {
		t.Error("nested fn inside impl method should not be extracted")
	}
	if _, ok := findFact(ff, "src.local_helper"); ok {
		t.Error("nested fn inside impl method should not be extracted as top-level")
	}
}

// --- Acceptance test: impl block method extraction ---
// This tests that methods inside impl blocks are extracted as method symbols.

func TestAcceptance_ImplBlockMethods(t *testing.T) {
	ff := extractFromString(t, `pub struct Server {
    port: u16,
}

impl Server {
    pub fn new(port: u16) -> Self {
        Self { port }
    }

    pub async fn start(&self) {
        println!("starting on {}", self.port);
    }

    fn private_helper(&self) {}
}

pub trait Handler {
    fn handle(&self);
    fn cleanup(&mut self);
}

impl Handler for Server {
    fn handle(&self) {
        println!("handling");
    }

    fn cleanup(&mut self) {}
}
`)

	// 1. Simple impl methods should be extracted
	newFn, ok := findFact(ff, "src.Server.new")
	if !ok {
		t.Fatal("expected method src.Server.new")
	}
	if newFn.Props["symbol_kind"] != facts.SymbolMethod {
		t.Errorf("Server.new symbol_kind = %v, want method", newFn.Props["symbol_kind"])
	}
	if newFn.Props["receiver"] != "Server" {
		t.Errorf("Server.new receiver = %v, want Server", newFn.Props["receiver"])
	}
	if newFn.Props["exported"] != true {
		t.Errorf("Server.new exported = %v, want true", newFn.Props["exported"])
	}

	// 2. Async methods
	startFn, ok := findFact(ff, "src.Server.start")
	if !ok {
		t.Fatal("expected method src.Server.start")
	}
	if startFn.Props["symbol_kind"] != facts.SymbolMethod {
		t.Errorf("Server.start symbol_kind = %v, want method", startFn.Props["symbol_kind"])
	}
	if startFn.Props["async"] != true {
		t.Errorf("Server.start async = %v, want true", startFn.Props["async"])
	}
	if startFn.Props["receiver"] != "Server" {
		t.Errorf("Server.start receiver = %v, want Server", startFn.Props["receiver"])
	}

	// 3. Private methods
	helperFn, ok := findFact(ff, "src.Server.private_helper")
	if !ok {
		t.Fatal("expected method src.Server.private_helper")
	}
	if helperFn.Props["exported"] != false {
		t.Errorf("Server.private_helper exported = %v, want false", helperFn.Props["exported"])
	}

	// 4. Trait impl methods should have trait info
	handleFn, ok := findFact(ff, "src.Server.handle")
	if !ok {
		t.Fatal("expected method src.Server.handle")
	}
	if handleFn.Props["symbol_kind"] != facts.SymbolMethod {
		t.Errorf("Server.handle symbol_kind = %v, want method", handleFn.Props["symbol_kind"])
	}
	if handleFn.Props["receiver"] != "Server" {
		t.Errorf("Server.handle receiver = %v, want Server", handleFn.Props["receiver"])
	}
	if handleFn.Props["trait"] != "Handler" {
		t.Errorf("Server.handle trait = %v, want Handler", handleFn.Props["trait"])
	}

	cleanupFn, ok := findFact(ff, "src.Server.cleanup")
	if !ok {
		t.Fatal("expected method src.Server.cleanup")
	}
	if cleanupFn.Props["trait"] != "Handler" {
		t.Errorf("Server.cleanup trait = %v, want Handler", cleanupFn.Props["trait"])
	}

	// 5. Server struct should still be extracted
	srv, ok := findFact(ff, "src.Server")
	if !ok {
		t.Fatal("expected struct src.Server")
	}
	if srv.Props["symbol_kind"] != facts.SymbolStruct {
		t.Errorf("Server symbol_kind = %v, want struct", srv.Props["symbol_kind"])
	}

	// 6. Handler trait should still be extracted
	handler, ok := findFact(ff, "src.Handler")
	if !ok {
		t.Fatal("expected trait src.Handler")
	}
	if handler.Props["symbol_kind"] != facts.SymbolInterface {
		t.Errorf("Handler symbol_kind = %v, want interface", handler.Props["symbol_kind"])
	}
}

// --- Acceptance test ---
// This exercises the full extractor public interface against a realistic Rust project.

func TestAcceptance_RustExtractor_FullProject(t *testing.T) {
	// Build a minimal Rust project on disk
	dir := t.TempDir()

	// Cargo.toml
	cargoToml := `[package]
name = "myapp"
version = "0.1.0"
edition = "2021"

[dependencies]
serde = { version = "1", features = ["derive"] }
tokio = { version = "1", features = ["full"] }
axum = "0.7"
`
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(cargoToml), 0o644); err != nil {
		t.Fatal(err)
	}

	// src/main.rs
	if err := os.MkdirAll(filepath.Join(dir, "src", "handlers"), 0o755); err != nil {
		t.Fatal(err)
	}

	mainRs := `use crate::handlers::user;
use axum::Router;

mod handlers;

pub const MAX_CONNECTIONS: usize = 100;
static APP_NAME: &str = "myapp";

#[tokio::main]
async fn main() {
    let app = Router::new();
    axum::serve(app).await.unwrap();
}

fn setup_logging() {
    println!("logging");
}
`
	if err := os.WriteFile(filepath.Join(dir, "src", "main.rs"), []byte(mainRs), 0o644); err != nil {
		t.Fatal(err)
	}

	// src/handlers/mod.rs
	handlersModRs := `pub mod user;
`
	if err := os.WriteFile(filepath.Join(dir, "src", "handlers", "mod.rs"), []byte(handlersModRs), 0o644); err != nil {
		t.Fatal(err)
	}

	// src/handlers/user.rs
	userRs := `use serde::{Deserialize, Serialize};

pub trait Repository {
    fn find_by_id(&self, id: u64) -> Option<User>;
    fn save(&mut self, user: &User);
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct User {
    pub id: u64,
    pub name: String,
    email: String,
}

pub enum UserRole {
    Admin,
    Member,
    Guest,
}

pub struct UserService {
    db: Box<dyn Repository>,
}

impl UserService {
    pub fn new(db: Box<dyn Repository>) -> Self {
        Self { db }
    }

    pub fn get_user(&self, id: u64) -> Option<User> {
        self.db.find_by_id(id)
    }
}

impl Repository for UserService {
    fn find_by_id(&self, id: u64) -> Option<User> {
        None
    }

    fn save(&mut self, user: &User) {}
}

type UserId = u64;

macro_rules! log_action {
    ($msg:expr) => {
        println!("{}", $msg);
    };
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_user_creation() {
        // This entire block should be skipped by the extractor
        struct TestRepo;
        impl Repository for TestRepo {
            fn find_by_id(&self, _id: u64) -> Option<User> { None }
            fn save(&mut self, _user: &User) {}
        }
    }
}
`
	if err := os.WriteFile(filepath.Join(dir, "src", "handlers", "user.rs"), []byte(userRs), 0o644); err != nil {
		t.Fatal(err)
	}

	// --- Run the extractor ---
	ext := New()

	if ext.Name() != "rust" {
		t.Fatalf("Name() = %q, want %q", ext.Name(), "rust")
	}

	detected, err := ext.Detect(dir)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !detected {
		t.Fatal("Detect() = false, want true (Cargo.toml present)")
	}

	files := []string{
		"src/main.rs",
		"src/handlers/mod.rs",
		"src/handlers/user.rs",
	}

	allFacts, err := ext.Extract(context.Background(), dir, files)
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}

	// --- Verify modules ---
	modules := findFactByKind(allFacts, facts.KindModule)
	if len(modules) == 0 {
		t.Fatal("expected module facts")
	}
	// Should have modules for src and src/handlers at minimum
	foundSrc := false
	foundHandlers := false
	for _, m := range modules {
		if m.Name == "src" {
			foundSrc = true
		}
		if m.Name == "src/handlers" {
			foundHandlers = true
		}
	}
	if !foundSrc {
		t.Error("expected module fact for 'src'")
	}
	if !foundHandlers {
		t.Error("expected module fact for 'src/handlers'")
	}

	// --- Verify symbols from main.rs ---
	// pub fn main (async)
	mainFn, ok := findFact(allFacts, "src.main")
	if !ok {
		t.Fatal("expected symbol for src.main")
	}
	if mainFn.Props["symbol_kind"] != facts.SymbolFunc {
		t.Errorf("main symbol_kind = %v, want function", mainFn.Props["symbol_kind"])
	}
	if mainFn.Props["async"] != true {
		t.Errorf("main async = %v, want true", mainFn.Props["async"])
	}

	// fn setup_logging (not pub -> not exported)
	setupFn, ok := findFact(allFacts, "src.setup_logging")
	if !ok {
		t.Fatal("expected symbol for src.setup_logging")
	}
	if setupFn.Props["exported"] != false {
		t.Errorf("setup_logging exported = %v, want false", setupFn.Props["exported"])
	}

	// const MAX_CONNECTIONS
	maxConn, ok := findFact(allFacts, "src.MAX_CONNECTIONS")
	if !ok {
		t.Fatal("expected symbol for src.MAX_CONNECTIONS")
	}
	if maxConn.Props["symbol_kind"] != facts.SymbolConstant {
		t.Errorf("MAX_CONNECTIONS symbol_kind = %v, want constant", maxConn.Props["symbol_kind"])
	}
	if maxConn.Props["exported"] != true {
		t.Errorf("MAX_CONNECTIONS exported = %v, want true", maxConn.Props["exported"])
	}

	// static APP_NAME
	appName, ok := findFact(allFacts, "src.APP_NAME")
	if !ok {
		t.Fatal("expected symbol for src.APP_NAME")
	}
	if appName.Props["symbol_kind"] != facts.SymbolVariable {
		t.Errorf("APP_NAME symbol_kind = %v, want variable", appName.Props["symbol_kind"])
	}

	// --- Verify symbols from user.rs ---
	// trait Repository
	repo, ok := findFact(allFacts, "src/handlers.Repository")
	if !ok {
		t.Fatal("expected symbol for src/handlers.Repository")
	}
	if repo.Props["symbol_kind"] != facts.SymbolInterface {
		t.Errorf("Repository symbol_kind = %v, want interface", repo.Props["symbol_kind"])
	}
	if repo.Props["exported"] != true {
		t.Errorf("Repository exported = %v, want true", repo.Props["exported"])
	}

	// struct User (pub, with derive)
	user, ok := findFact(allFacts, "src/handlers.User")
	if !ok {
		t.Fatal("expected symbol for src/handlers.User")
	}
	if user.Props["symbol_kind"] != facts.SymbolStruct {
		t.Errorf("User symbol_kind = %v, want struct", user.Props["symbol_kind"])
	}
	if user.Props["exported"] != true {
		t.Errorf("User exported = %v, want true", user.Props["exported"])
	}
	// derive traits should create implements relations
	for _, trait := range []string{"Debug", "Clone", "Serialize", "Deserialize"} {
		if !hasRelation(user, facts.RelImplements, trait) {
			t.Errorf("User missing implements relation for %s", trait)
		}
	}

	// enum UserRole
	role, ok := findFact(allFacts, "src/handlers.UserRole")
	if !ok {
		t.Fatal("expected symbol for src/handlers.UserRole")
	}
	if role.Props["symbol_kind"] != facts.SymbolType {
		t.Errorf("UserRole symbol_kind = %v, want type (enum)", role.Props["symbol_kind"])
	}
	if role.Props["enum"] != true {
		t.Errorf("UserRole enum = %v, want true", role.Props["enum"])
	}

	// struct UserService
	svc, ok := findFact(allFacts, "src/handlers.UserService")
	if !ok {
		t.Fatal("expected symbol for src/handlers.UserService")
	}
	if svc.Props["symbol_kind"] != facts.SymbolStruct {
		t.Errorf("UserService symbol_kind = %v, want struct", svc.Props["symbol_kind"])
	}

	// impl Repository for UserService -> implements relation
	if !hasRelation(svc, facts.RelImplements, "Repository") {
		t.Error("UserService missing implements relation for Repository")
	}

	// --- Verify impl block methods from user.rs ---
	// impl UserService { pub fn new(...), pub fn get_user(...) }
	newMethod, ok := findFact(allFacts, "src/handlers.UserService.new")
	if !ok {
		t.Fatal("expected method for src/handlers.UserService.new")
	}
	if newMethod.Props["symbol_kind"] != facts.SymbolMethod {
		t.Errorf("UserService.new symbol_kind = %v, want method", newMethod.Props["symbol_kind"])
	}
	if newMethod.Props["receiver"] != "UserService" {
		t.Errorf("UserService.new receiver = %v, want UserService", newMethod.Props["receiver"])
	}
	if newMethod.Props["exported"] != true {
		t.Errorf("UserService.new exported = %v, want true", newMethod.Props["exported"])
	}

	getUser, ok := findFact(allFacts, "src/handlers.UserService.get_user")
	if !ok {
		t.Fatal("expected method for src/handlers.UserService.get_user")
	}
	if getUser.Props["symbol_kind"] != facts.SymbolMethod {
		t.Errorf("UserService.get_user symbol_kind = %v, want method", getUser.Props["symbol_kind"])
	}

	// impl Repository for UserService { fn find_by_id, fn save }
	findById, ok := findFact(allFacts, "src/handlers.UserService.find_by_id")
	if !ok {
		t.Fatal("expected method for src/handlers.UserService.find_by_id")
	}
	if findById.Props["trait"] != "Repository" {
		t.Errorf("UserService.find_by_id trait = %v, want Repository", findById.Props["trait"])
	}
	if findById.Props["receiver"] != "UserService" {
		t.Errorf("UserService.find_by_id receiver = %v, want UserService", findById.Props["receiver"])
	}

	saveMethod, ok := findFact(allFacts, "src/handlers.UserService.save")
	if !ok {
		t.Fatal("expected method for src/handlers.UserService.save")
	}
	if saveMethod.Props["trait"] != "Repository" {
		t.Errorf("UserService.save trait = %v, want Repository", saveMethod.Props["trait"])
	}

	// type alias UserId
	userId, ok := findFact(allFacts, "src/handlers.UserId")
	if !ok {
		t.Fatal("expected symbol for src/handlers.UserId")
	}
	if userId.Props["symbol_kind"] != facts.SymbolType {
		t.Errorf("UserId symbol_kind = %v, want type", userId.Props["symbol_kind"])
	}

	// macro_rules! log_action
	logMacro, ok := findFact(allFacts, "src/handlers.log_action")
	if !ok {
		t.Fatal("expected symbol for src/handlers.log_action")
	}
	if logMacro.Props["macro"] != true {
		t.Errorf("log_action macro = %v, want true", logMacro.Props["macro"])
	}

	// --- Verify imports ---
	deps := findFactByKind(allFacts, facts.KindDependency)
	if len(deps) == 0 {
		t.Fatal("expected dependency facts")
	}
	// Check for internal import: use crate::handlers::user
	foundCrateImport := false
	// Check for external import: use serde::{Deserialize, Serialize}
	foundSerdeImport := false
	// Check for external import: use axum::Router
	foundAxumImport := false
	for _, d := range deps {
		for _, r := range d.Relations {
			if r.Kind == facts.RelImports {
				switch {
				case r.Target == "crate::handlers::user" || r.Target == "handlers/user" || r.Target == "src/handlers":
					foundCrateImport = true
				case r.Target == "serde":
					foundSerdeImport = true
				case r.Target == "axum":
					foundAxumImport = true
				}
			}
		}
	}
	if !foundCrateImport {
		t.Error("expected internal import for crate::handlers::user")
	}
	if !foundSerdeImport {
		t.Error("expected external import for serde")
	}
	if !foundAxumImport {
		t.Error("expected external import for axum")
	}

	// --- Verify #[cfg(test)] blocks are SKIPPED ---
	// TestRepo struct from the test module should NOT be extracted
	if _, ok := findFact(allFacts, "src/handlers.TestRepo"); ok {
		t.Error("TestRepo from #[cfg(test)] block should not be extracted")
	}

	// --- Verify all facts have language: "rust" ---
	for _, f := range allFacts {
		if f.Props != nil {
			if lang, ok := f.Props["language"]; ok {
				if lang != "rust" {
					t.Errorf("fact %q has language %v, want rust", f.Name, lang)
				}
			}
		}
	}
}

// --- Unit tests for mod declarations ---

func TestExtractFile_ModExternalDeclaration(t *testing.T) {
	ff := extractFromString(t, `mod foo;
pub mod bar;
pub(crate) mod baz;
`)
	// mod foo; → private external module
	foo, ok := findFact(ff, "src.foo")
	if !ok {
		t.Fatal("expected symbol fact for src.foo")
	}
	if foo.Props["symbol_kind"] != "module" {
		t.Errorf("foo symbol_kind = %v, want module", foo.Props["symbol_kind"])
	}
	if foo.Props["exported"] != false {
		t.Errorf("foo exported = %v, want false", foo.Props["exported"])
	}
	if foo.Props["mod_style"] != "external" {
		t.Errorf("foo mod_style = %v, want external", foo.Props["mod_style"])
	}

	// pub mod bar; → public external module
	bar, ok := findFact(ff, "src.bar")
	if !ok {
		t.Fatal("expected symbol fact for src.bar")
	}
	if bar.Props["exported"] != true {
		t.Errorf("bar exported = %v, want true", bar.Props["exported"])
	}

	// pub(crate) mod baz; → pub(crate) visibility
	baz, ok := findFact(ff, "src.baz")
	if !ok {
		t.Fatal("expected symbol fact for src.baz")
	}
	if baz.Props["exported"] != true {
		t.Errorf("baz exported = %v, want true", baz.Props["exported"])
	}
	if baz.Props["visibility"] != "pub(crate)" {
		t.Errorf("baz visibility = %v, want pub(crate)", baz.Props["visibility"])
	}
}

func TestExtractFile_ModInlineDeclaration(t *testing.T) {
	ff := extractFromString(t, `mod helpers {
    pub fn do_thing() {}
}

pub mod public_inline {
    fn private_fn() {}
}
`)
	// mod helpers { ... } → private inline module
	helpers, ok := findFact(ff, "src.helpers")
	if !ok {
		t.Fatal("expected symbol fact for src.helpers")
	}
	if helpers.Props["symbol_kind"] != "module" {
		t.Errorf("helpers symbol_kind = %v, want module", helpers.Props["symbol_kind"])
	}
	if helpers.Props["exported"] != false {
		t.Errorf("helpers exported = %v, want false", helpers.Props["exported"])
	}
	if helpers.Props["mod_style"] != "inline" {
		t.Errorf("helpers mod_style = %v, want inline", helpers.Props["mod_style"])
	}

	// pub mod public_inline { ... } → public inline module
	pubInline, ok := findFact(ff, "src.public_inline")
	if !ok {
		t.Fatal("expected symbol fact for src.public_inline")
	}
	if pubInline.Props["exported"] != true {
		t.Errorf("public_inline exported = %v, want true", pubInline.Props["exported"])
	}
	if pubInline.Props["mod_style"] != "inline" {
		t.Errorf("public_inline mod_style = %v, want inline", pubInline.Props["mod_style"])
	}
}

func TestExtractFile_CfgTestModNotTracked(t *testing.T) {
	ff := extractFromString(t, `pub struct Real {}

#[cfg(test)]
mod tests {
    fn test_fn() {}
}
`)
	// Real struct should be there
	if _, ok := findFact(ff, "src.Real"); !ok {
		t.Fatal("expected fact for src.Real")
	}
	// cfg(test) mod tests should NOT be a module symbol
	if _, ok := findFact(ff, "src.tests"); ok {
		t.Error("cfg(test) mod tests should not be extracted as a module symbol")
	}
	// items inside cfg(test) should also not be extracted
	if _, ok := findFact(ff, "src.test_fn"); ok {
		t.Error("test_fn inside cfg(test) should not be extracted")
	}
}

// --- Acceptance test: mod declarations ---

func TestAcceptance_ModDeclarations(t *testing.T) {
	ff := extractFromString(t, `pub mod handlers;
mod utils;
pub(crate) mod config;

mod inline_mod {
    pub fn inner_fn() {}
}

#[cfg(test)]
mod tests {
    fn test_helper() {}
}
`)

	// 1. `pub mod handlers;` — external module, exported
	handlers, ok := findFact(ff, "src.handlers")
	if !ok {
		t.Fatal("expected symbol fact for src.handlers (pub mod handlers;)")
	}
	if handlers.Kind != facts.KindSymbol {
		t.Errorf("handlers kind = %v, want symbol", handlers.Kind)
	}
	if handlers.Props["symbol_kind"] != "module" {
		t.Errorf("handlers symbol_kind = %v, want module", handlers.Props["symbol_kind"])
	}
	if handlers.Props["exported"] != true {
		t.Errorf("handlers exported = %v, want true", handlers.Props["exported"])
	}
	if handlers.Props["mod_style"] != "external" {
		t.Errorf("handlers mod_style = %v, want external", handlers.Props["mod_style"])
	}
	if !hasRelation(handlers, facts.RelDeclares, "src") {
		t.Error("handlers missing declares relation to src")
	}

	// 2. `mod utils;` — external module, not exported
	utils, ok := findFact(ff, "src.utils")
	if !ok {
		t.Fatal("expected symbol fact for src.utils (mod utils;)")
	}
	if utils.Props["symbol_kind"] != "module" {
		t.Errorf("utils symbol_kind = %v, want module", utils.Props["symbol_kind"])
	}
	if utils.Props["exported"] != false {
		t.Errorf("utils exported = %v, want false", utils.Props["exported"])
	}
	if utils.Props["mod_style"] != "external" {
		t.Errorf("utils mod_style = %v, want external", utils.Props["mod_style"])
	}

	// 3. `pub(crate) mod config;` — external module, exported (pub(crate) counts as exported)
	config, ok := findFact(ff, "src.config")
	if !ok {
		t.Fatal("expected symbol fact for src.config (pub(crate) mod config;)")
	}
	if config.Props["symbol_kind"] != "module" {
		t.Errorf("config symbol_kind = %v, want module", config.Props["symbol_kind"])
	}
	if config.Props["exported"] != true {
		t.Errorf("config exported = %v, want true", config.Props["exported"])
	}
	if config.Props["visibility"] != "pub(crate)" {
		t.Errorf("config visibility = %v, want pub(crate)", config.Props["visibility"])
	}

	// 4. `mod inline_mod { ... }` — inline module, not exported
	inlineMod, ok := findFact(ff, "src.inline_mod")
	if !ok {
		t.Fatal("expected symbol fact for src.inline_mod (mod inline_mod { ... })")
	}
	if inlineMod.Props["symbol_kind"] != "module" {
		t.Errorf("inline_mod symbol_kind = %v, want module", inlineMod.Props["symbol_kind"])
	}
	if inlineMod.Props["exported"] != false {
		t.Errorf("inline_mod exported = %v, want false", inlineMod.Props["exported"])
	}
	if inlineMod.Props["mod_style"] != "inline" {
		t.Errorf("inline_mod mod_style = %v, want inline", inlineMod.Props["mod_style"])
	}

	// 5. `#[cfg(test)] mod tests` — should NOT generate a module fact
	if _, ok := findFact(ff, "src.tests"); ok {
		t.Error("cfg(test) mod tests should not generate a module symbol fact")
	}

	// 6. fn inside #[cfg(test)] should still be skipped
	if _, ok := findFact(ff, "src.test_helper"); ok {
		t.Error("test_helper from cfg(test) block should not be extracted")
	}
}

// --- Unit tests for parseCargoToml ---

func TestParseCargoToml_SimpleStringDep(t *testing.T) {
	content := `[package]
name = "myapp"

[dependencies]
serde = "1.0"
`
	deps := parseCargoToml([]byte(content))
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(deps))
	}
	if deps[0].Name != "serde" {
		t.Errorf("name = %q, want serde", deps[0].Name)
	}
	if deps[0].Props["version"] != "1.0" {
		t.Errorf("version = %v, want 1.0", deps[0].Props["version"])
	}
	if deps[0].Props["source"] != "external" {
		t.Errorf("source = %v, want external", deps[0].Props["source"])
	}
	if deps[0].Props["dep_scope"] != "normal" {
		t.Errorf("dep_scope = %v, want normal", deps[0].Props["dep_scope"])
	}
}

func TestParseCargoToml_InlineTableDep(t *testing.T) {
	content := `[dependencies]
tokio = { version = "1", features = ["full"] }
`
	deps := parseCargoToml([]byte(content))
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(deps))
	}
	if deps[0].Props["version"] != "1" {
		t.Errorf("version = %v, want 1", deps[0].Props["version"])
	}
	if deps[0].Props["source"] != "external" {
		t.Errorf("source = %v, want external", deps[0].Props["source"])
	}
}

func TestParseCargoToml_PathDep(t *testing.T) {
	content := `[dependencies]
my_lib = { path = "../my_lib" }
`
	deps := parseCargoToml([]byte(content))
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(deps))
	}
	if deps[0].Props["source"] != "internal" {
		t.Errorf("source = %v, want internal", deps[0].Props["source"])
	}
	if deps[0].Props["path"] != "../my_lib" {
		t.Errorf("path = %v, want ../my_lib", deps[0].Props["path"])
	}
}

func TestParseCargoToml_WorkspaceDep(t *testing.T) {
	content := `[dependencies]
shared = { workspace = true }
`
	deps := parseCargoToml([]byte(content))
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(deps))
	}
	if deps[0].Props["workspace"] != true {
		t.Errorf("workspace = %v, want true", deps[0].Props["workspace"])
	}
	if deps[0].Props["source"] != "external" {
		t.Errorf("source = %v, want external", deps[0].Props["source"])
	}
}

func TestParseCargoToml_DevDependencies(t *testing.T) {
	content := `[dev-dependencies]
mockall = "0.11"
`
	deps := parseCargoToml([]byte(content))
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(deps))
	}
	if deps[0].Props["dep_scope"] != "dev" {
		t.Errorf("dep_scope = %v, want dev", deps[0].Props["dep_scope"])
	}
}

func TestParseCargoToml_BuildDependencies(t *testing.T) {
	content := `[build-dependencies]
cc = "1.0"
`
	deps := parseCargoToml([]byte(content))
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(deps))
	}
	if deps[0].Props["dep_scope"] != "build" {
		t.Errorf("dep_scope = %v, want build", deps[0].Props["dep_scope"])
	}
}

func TestParseCargoToml_MultipleSections(t *testing.T) {
	content := `[package]
name = "myapp"

[dependencies]
serde = "1.0"
tokio = { version = "1" }

[dev-dependencies]
mockall = "0.11"

[build-dependencies]
cc = "1.0"
`
	deps := parseCargoToml([]byte(content))
	if len(deps) != 4 {
		t.Fatalf("expected 4 deps, got %d: %v", len(deps), deps)
	}

	// Verify scopes
	scopes := make(map[string]string)
	for _, d := range deps {
		scopes[d.Name] = d.Props["dep_scope"].(string)
	}
	if scopes["serde"] != "normal" {
		t.Errorf("serde scope = %v, want normal", scopes["serde"])
	}
	if scopes["tokio"] != "normal" {
		t.Errorf("tokio scope = %v, want normal", scopes["tokio"])
	}
	if scopes["mockall"] != "dev" {
		t.Errorf("mockall scope = %v, want dev", scopes["mockall"])
	}
	if scopes["cc"] != "build" {
		t.Errorf("cc scope = %v, want build", scopes["cc"])
	}
}

func TestParseCargoToml_NoDeps(t *testing.T) {
	content := `[package]
name = "myapp"
version = "0.1.0"
`
	deps := parseCargoToml([]byte(content))
	if len(deps) != 0 {
		t.Fatalf("expected 0 deps, got %d", len(deps))
	}
}

func TestParseCargoToml_SectionEndedByAnotherSection(t *testing.T) {
	content := `[dependencies]
serde = "1.0"

[package]
name = "myapp"
`
	deps := parseCargoToml([]byte(content))
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(deps))
	}
	if deps[0].Name != "serde" {
		t.Errorf("name = %q, want serde", deps[0].Name)
	}
}

// --- Acceptance test: Cargo.toml dependency parsing ---

func TestAcceptance_CargoTomlDependencies(t *testing.T) {
	dir := t.TempDir()

	cargoToml := `[package]
name = "myapp"
version = "0.1.0"
edition = "2021"

[dependencies]
serde = "1.0"
tokio = { version = "1", features = ["full"] }
axum = "0.7"
my_lib = { path = "../my_lib" }
shared = { workspace = true }

[dev-dependencies]
mockall = "0.11"
tempfile = { version = "3.0" }

[build-dependencies]
cc = "1.0"
`
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(cargoToml), 0o644); err != nil {
		t.Fatal(err)
	}

	// Need at least one .rs file for Extract to process
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "lib.rs"), []byte("pub fn hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ext := New()
	allFacts, err := ext.Extract(context.Background(), dir, []string{"src/lib.rs"})
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}

	deps := findFactByKind(allFacts, facts.KindDependency)

	// Helper to find a cargo dep fact by crate name
	findCargoDep := func(crateName string) (facts.Fact, bool) {
		for _, d := range deps {
			if d.File == "Cargo.toml" && d.Name == crateName {
				return d, true
			}
		}
		return facts.Fact{}, false
	}

	// 1. Simple string version: serde = "1.0"
	serde, ok := findCargoDep("serde")
	if !ok {
		t.Fatal("expected dependency fact for serde")
	}
	if serde.Props["version"] != "1.0" {
		t.Errorf("serde version = %v, want 1.0", serde.Props["version"])
	}
	if serde.Props["source"] != "external" {
		t.Errorf("serde source = %v, want external", serde.Props["source"])
	}
	if serde.Props["dep_scope"] != "normal" {
		t.Errorf("serde dep_scope = %v, want normal", serde.Props["dep_scope"])
	}
	if !hasRelation(serde, facts.RelDependsOn, "serde") {
		t.Error("serde missing depends_on relation")
	}

	// 2. Table inline: tokio = { version = "1", features = [...] }
	tokio, ok := findCargoDep("tokio")
	if !ok {
		t.Fatal("expected dependency fact for tokio")
	}
	if tokio.Props["version"] != "1" {
		t.Errorf("tokio version = %v, want 1", tokio.Props["version"])
	}
	if tokio.Props["source"] != "external" {
		t.Errorf("tokio source = %v, want external", tokio.Props["source"])
	}

	// 3. Simple string: axum = "0.7"
	axum, ok := findCargoDep("axum")
	if !ok {
		t.Fatal("expected dependency fact for axum")
	}
	if axum.Props["version"] != "0.7" {
		t.Errorf("axum version = %v, want 0.7", axum.Props["version"])
	}

	// 4. Path dep: my_lib = { path = "../my_lib" } → source: "internal"
	myLib, ok := findCargoDep("my_lib")
	if !ok {
		t.Fatal("expected dependency fact for my_lib")
	}
	if myLib.Props["source"] != "internal" {
		t.Errorf("my_lib source = %v, want internal", myLib.Props["source"])
	}
	if myLib.Props["path"] != "../my_lib" {
		t.Errorf("my_lib path = %v, want ../my_lib", myLib.Props["path"])
	}

	// 5. Workspace dep: shared = { workspace = true }
	shared, ok := findCargoDep("shared")
	if !ok {
		t.Fatal("expected dependency fact for shared")
	}
	if shared.Props["source"] != "external" {
		t.Errorf("shared source = %v, want external (workspace treated as external)", shared.Props["source"])
	}
	if shared.Props["workspace"] != true {
		t.Errorf("shared workspace = %v, want true", shared.Props["workspace"])
	}

	// 6. Dev dependency: mockall
	mockall, ok := findCargoDep("mockall")
	if !ok {
		t.Fatal("expected dependency fact for mockall")
	}
	if mockall.Props["dep_scope"] != "dev" {
		t.Errorf("mockall dep_scope = %v, want dev", mockall.Props["dep_scope"])
	}

	// 7. Dev dep with table: tempfile = { version = "3.0" }
	tempfile, ok := findCargoDep("tempfile")
	if !ok {
		t.Fatal("expected dependency fact for tempfile")
	}
	if tempfile.Props["dep_scope"] != "dev" {
		t.Errorf("tempfile dep_scope = %v, want dev", tempfile.Props["dep_scope"])
	}
	if tempfile.Props["version"] != "3.0" {
		t.Errorf("tempfile version = %v, want 3.0", tempfile.Props["version"])
	}

	// 8. Build dependency: cc
	cc, ok := findCargoDep("cc")
	if !ok {
		t.Fatal("expected dependency fact for cc")
	}
	if cc.Props["dep_scope"] != "build" {
		t.Errorf("cc dep_scope = %v, want build", cc.Props["dep_scope"])
	}
	if cc.Props["version"] != "1.0" {
		t.Errorf("cc version = %v, want 1.0", cc.Props["version"])
	}

	// 9. All cargo deps should have language: "rust"
	for _, d := range deps {
		if d.File == "Cargo.toml" {
			if d.Props["language"] != "rust" {
				t.Errorf("cargo dep %q language = %v, want rust", d.Name, d.Props["language"])
			}
		}
	}
}

// --- Unit tests: call extraction ---

func TestExtractCalls_SimpleFunctionCall(t *testing.T) {
	calls := extractCalls("    setup_logging();")
	if len(calls) != 1 || calls[0] != "setup_logging" {
		t.Errorf("extractCalls simple = %v, want [setup_logging]", calls)
	}
}

func TestExtractCalls_QualifiedCall(t *testing.T) {
	calls := extractCalls("    module::function();")
	if len(calls) != 1 || calls[0] != "module.function" {
		t.Errorf("extractCalls qualified = %v, want [module.function]", calls)
	}
}

func TestExtractCalls_MethodCall(t *testing.T) {
	calls := extractCalls("    self.method();")
	if len(calls) != 1 || calls[0] != "self.method" {
		t.Errorf("extractCalls method = %v, want [self.method]", calls)
	}
}

func TestExtractCalls_ObjectMethodCall(t *testing.T) {
	calls := extractCalls("    obj.method();")
	if len(calls) != 1 || calls[0] != "obj.method" {
		t.Errorf("extractCalls obj method = %v, want [obj.method]", calls)
	}
}

func TestExtractCalls_AssociatedFunction(t *testing.T) {
	calls := extractCalls("    let x = Type::new();")
	if len(calls) != 1 || calls[0] != "Type.new" {
		t.Errorf("extractCalls associated = %v, want [Type.new]", calls)
	}
}

func TestExtractCalls_MacroSkipped(t *testing.T) {
	calls := extractCalls("    println!(\"hello\");")
	if len(calls) != 0 {
		t.Errorf("extractCalls macro = %v, want []", calls)
	}
}

func TestExtractCalls_KeywordsSkipped(t *testing.T) {
	// Keywords themselves should not appear as call targets
	// But actual function calls on the same line should still be extracted
	tests := []struct {
		line string
		want []string // expected calls (keywords excluded)
	}{
		{"    if condition() {", []string{"condition"}},
		{"    while running() {", []string{"running"}},
		{"    for item in list() {", []string{"list"}},
		{"    match value() {", []string{"value"}},
		{"    return result();", []string{"result"}},
		// Pure keyword usage, no real call
		{"    if true {", nil},
		{"    return;", nil},
	}
	for _, tt := range tests {
		calls := extractCalls(tt.line)
		if len(calls) != len(tt.want) {
			t.Errorf("extractCalls(%q) = %v, want %v", tt.line, calls, tt.want)
			continue
		}
		for i, c := range calls {
			if c != tt.want[i] {
				t.Errorf("extractCalls(%q)[%d] = %q, want %q", tt.line, i, c, tt.want[i])
			}
		}
	}
}

func TestExtractCalls_MultipleCalls(t *testing.T) {
	calls := extractCalls("    let x = foo(); let y = bar();")
	found := map[string]bool{}
	for _, c := range calls {
		found[c] = true
	}
	if !found["foo"] || !found["bar"] {
		t.Errorf("extractCalls multiple = %v, want foo and bar", calls)
	}
}

func TestExtractCalls_EmptyLine(t *testing.T) {
	calls := extractCalls("")
	if len(calls) != 0 {
		t.Errorf("extractCalls empty = %v, want []", calls)
	}
}

func TestExtractFile_FnCallsSimple(t *testing.T) {
	ff := extractFromString(t, `fn caller() {
    callee();
}

fn callee() {}
`)
	f, ok := findFact(ff, "src.caller")
	if !ok {
		t.Fatal("expected fact for src.caller")
	}
	if !hasRelation(f, facts.RelCalls, "src.callee") {
		t.Error("caller missing calls relation for src.callee (resolved)")
	}
}

func TestExtractFile_MethodCallsInImpl(t *testing.T) {
	ff := extractFromString(t, `pub struct Foo {}

impl Foo {
    pub fn bar(&self) {
        self.baz();
        helper();
    }

    fn baz(&self) {}
}

fn helper() {}
`)
	bar, ok := findFact(ff, "src.Foo.bar")
	if !ok {
		t.Fatal("expected method fact for src.Foo.bar")
	}
	if !hasRelation(bar, facts.RelCalls, "src.Foo.baz") {
		t.Error("Foo.bar missing calls relation for src.Foo.baz (self.baz resolved)")
	}
	if !hasRelation(bar, facts.RelCalls, "src.helper") {
		t.Error("Foo.bar missing calls relation for src.helper (resolved)")
	}
}

func TestExtractFile_NoCallsForMacros(t *testing.T) {
	ff := extractFromString(t, `fn example() {
    println!("hello");
    vec![1, 2, 3];
    real_call();
}
`)
	f, ok := findFact(ff, "src.example")
	if !ok {
		t.Fatal("expected fact for src.example")
	}
	if hasRelation(f, facts.RelCalls, "println") {
		t.Error("should not have calls relation for macro println!")
	}
	if !hasRelation(f, facts.RelCalls, "real_call") && !hasRelation(f, facts.RelCalls, "src.real_call") {
		t.Error("missing calls relation for real_call")
	}
}

// --- Acceptance test: call graph / calls relation ---

func TestAcceptance_CallGraph(t *testing.T) {
	ff := extractFromString(t, `use std::io;

pub fn main() {
    setup_logging();
    let cfg = Config::load();
    let svc = Service::new(cfg);
    svc.run();
    io::stdout();
    println!("done");
}

fn setup_logging() {
    init_tracing();
}

fn init_tracing() {}

pub struct Config {}

impl Config {
    pub fn load() -> Self {
        Self {}
    }
}

pub struct Service {
    cfg: Config,
}

impl Service {
    pub fn new(cfg: Config) -> Self {
        Self { cfg }
    }

    pub fn run(&self) {
        self.handle_request();
        helper::process();
        if self.cfg_valid() {
            self.start_server();
        }
    }

    fn handle_request(&self) {}
    fn cfg_valid(&self) -> bool { true }
    fn start_server(&self) {}
}
`)

	// 1. Top-level fn main should call setup_logging → resolved to "src.setup_logging"
	mainFn, ok := findFact(ff, "src.main")
	if !ok {
		t.Fatal("expected fact for src.main")
	}
	if !hasRelation(mainFn, facts.RelCalls, "src.setup_logging") {
		t.Error("main missing calls relation for src.setup_logging")
	}

	// 2. main should call Config::load → resolved to "src.Config.load"
	if !hasRelation(mainFn, facts.RelCalls, "src.Config.load") {
		t.Error("main missing calls relation for src.Config.load")
	}

	// 3. main should call Service::new → resolved to "src.Service.new"
	if !hasRelation(mainFn, facts.RelCalls, "src.Service.new") {
		t.Error("main missing calls relation for src.Service.new")
	}

	// 4. main should call svc.run — "svc" is a variable, type unknown, kept as-is
	if !hasRelation(mainFn, facts.RelCalls, "svc.run") {
		t.Error("main missing calls relation for svc.run (unresolvable variable)")
	}

	// 5. main should call io::stdout → io.stdout (external, unresolvable, kept as-is)
	if !hasRelation(mainFn, facts.RelCalls, "io.stdout") {
		t.Error("main missing calls relation for io.stdout")
	}

	// 6. main should NOT have a calls relation for println! (macro invocation)
	if hasRelation(mainFn, facts.RelCalls, "println") {
		t.Error("main should not have calls relation for macro println!")
	}

	// 7. setup_logging should call init_tracing → resolved to "src.init_tracing"
	setupFn, ok := findFact(ff, "src.setup_logging")
	if !ok {
		t.Fatal("expected fact for src.setup_logging")
	}
	if !hasRelation(setupFn, facts.RelCalls, "src.init_tracing") {
		t.Error("setup_logging missing calls relation for src.init_tracing")
	}

	// 8. Service.run should call self.handle_request → resolved to "src.Service.handle_request"
	runMethod, ok := findFact(ff, "src.Service.run")
	if !ok {
		t.Fatal("expected method fact for src.Service.run")
	}
	if !hasRelation(runMethod, facts.RelCalls, "src.Service.handle_request") {
		t.Error("Service.run missing calls relation for src.Service.handle_request")
	}

	// 9. Service.run should call helper::process → helper.process (unresolvable, kept as-is)
	if !hasRelation(runMethod, facts.RelCalls, "helper.process") {
		t.Error("Service.run missing calls relation for helper.process")
	}

	// 10. Service.run should call self.cfg_valid → resolved to "src.Service.cfg_valid"
	if !hasRelation(runMethod, facts.RelCalls, "src.Service.cfg_valid") {
		t.Error("Service.run missing calls relation for src.Service.cfg_valid")
	}

	// 11. Service.run should call self.start_server → resolved to "src.Service.start_server"
	if !hasRelation(runMethod, facts.RelCalls, "src.Service.start_server") {
		t.Error("Service.run missing calls relation for src.Service.start_server")
	}

	// 12. init_tracing has no calls (empty body)
	initFn, ok := findFact(ff, "src.init_tracing")
	if !ok {
		t.Fatal("expected fact for src.init_tracing")
	}
	for _, r := range initFn.Relations {
		if r.Kind == facts.RelCalls {
			t.Errorf("init_tracing should have no calls, found: %s", r.Target)
		}
	}
}

// --- Acceptance test: proc macro support (derive + attribute macros) ---

func TestAcceptance_ProcMacroSupport(t *testing.T) {
	ff := extractFromString(t, `use serde::{Serialize, Deserialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Config {
    pub host: String,
    pub port: u16,
}

#[derive(Debug, PartialEq)]
pub enum Status {
    Active,
    Inactive,
}

#[derive(Copy, Clone)]
pub union FloatInt {
    f: f32,
    i: i32,
}

#[tokio::main]
async fn main() {
    println!("starting");
}

#[test]
fn it_works() {}

#[inline(always)]
pub fn hot_path() {}

#[allow(dead_code)]
#[deprecated(since = "1.0", note = "use new_fn")]
fn old_fn() {}
`)

	// 1. Config struct should have derive implements relations
	config, ok := findFact(ff, "src.Config")
	if !ok {
		t.Fatal("expected fact for src.Config")
	}
	for _, trait := range []string{"Debug", "Clone", "Serialize", "Deserialize"} {
		if !hasRelation(config, facts.RelImplements, trait) {
			t.Errorf("Config missing implements relation for %s", trait)
		}
	}
	// Config should have attribute macros recorded
	attrs, _ := config.Props["attributes"].([]string)
	if !containsStr(attrs, "serde(rename_all = \"camelCase\")") {
		t.Errorf("Config missing serde attribute, got attributes=%v", attrs)
	}

	// 2. Status enum should have derive implements relations
	status, ok := findFact(ff, "src.Status")
	if !ok {
		t.Fatal("expected fact for src.Status")
	}
	for _, trait := range []string{"Debug", "PartialEq"} {
		if !hasRelation(status, facts.RelImplements, trait) {
			t.Errorf("Status missing implements relation for %s", trait)
		}
	}

	// 3. Union should also get derive implements relations
	floatInt, ok := findFact(ff, "src.FloatInt")
	if !ok {
		t.Fatal("expected fact for src.FloatInt")
	}
	for _, trait := range []string{"Copy", "Clone"} {
		if !hasRelation(floatInt, facts.RelImplements, trait) {
			t.Errorf("FloatInt missing implements relation for %s", trait)
		}
	}

	// 4. main should have tokio::main attribute
	mainFn, ok := findFact(ff, "src.main")
	if !ok {
		t.Fatal("expected fact for src.main")
	}
	mainAttrs, _ := mainFn.Props["attributes"].([]string)
	if !containsStr(mainAttrs, "tokio::main") {
		t.Errorf("main missing tokio::main attribute, got attributes=%v", mainAttrs)
	}

	// 5. it_works should have test attribute
	itWorks, ok := findFact(ff, "src.it_works")
	if !ok {
		t.Fatal("expected fact for src.it_works")
	}
	itWorksAttrs, _ := itWorks.Props["attributes"].([]string)
	if !containsStr(itWorksAttrs, "test") {
		t.Errorf("it_works missing test attribute, got attributes=%v", itWorksAttrs)
	}

	// 6. hot_path should have inline attribute
	hotPath, ok := findFact(ff, "src.hot_path")
	if !ok {
		t.Fatal("expected fact for src.hot_path")
	}
	hotPathAttrs, _ := hotPath.Props["attributes"].([]string)
	if !containsStr(hotPathAttrs, "inline(always)") {
		t.Errorf("hot_path missing inline(always) attribute, got attributes=%v", hotPathAttrs)
	}

	// 7. old_fn should have multiple attributes
	oldFn, ok := findFact(ff, "src.old_fn")
	if !ok {
		t.Fatal("expected fact for src.old_fn")
	}
	oldFnAttrs, _ := oldFn.Props["attributes"].([]string)
	if !containsStr(oldFnAttrs, "allow(dead_code)") {
		t.Errorf("old_fn missing allow(dead_code) attribute, got attributes=%v", oldFnAttrs)
	}
	if !containsStr(oldFnAttrs, "deprecated(since = \"1.0\", note = \"use new_fn\")") {
		t.Errorf("old_fn missing deprecated attribute, got attributes=%v", oldFnAttrs)
	}

	// 8. #[cfg(test)] should NOT be treated as a regular attribute (it's a skip signal)
	// Verify existing cfg(test) skip still works
	if _, ok := findFact(ff, "src.it_works"); !ok {
		// it_works is NOT inside cfg(test), so it should exist
		t.Fatal("it_works should exist (it's a standalone #[test], not inside cfg(test) block)")
	}
}

// --- Unit tests: resolveCallTargets ---

func TestResolveCallTargets_SimpleCall(t *testing.T) {
	ff := []facts.Fact{
		{Kind: facts.KindSymbol, Name: "src.caller", File: "src/main.rs", Props: map[string]any{"symbol_kind": facts.SymbolFunc},
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "callee"}}},
		{Kind: facts.KindSymbol, Name: "src.callee", File: "src/main.rs", Props: map[string]any{"symbol_kind": facts.SymbolFunc}},
	}
	resolved := resolveCallTargets(ff)
	caller := resolved[0]
	if !hasRelation(caller, facts.RelCalls, "src.callee") {
		t.Errorf("expected call to src.callee, got relations: %v", caller.Relations)
	}
}

func TestResolveCallTargets_QualifiedCall(t *testing.T) {
	ff := []facts.Fact{
		{Kind: facts.KindSymbol, Name: "src.main", File: "src/main.rs", Props: map[string]any{"symbol_kind": facts.SymbolFunc},
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Config.load"}}},
		{Kind: facts.KindSymbol, Name: "src.Config.load", File: "src/main.rs", Props: map[string]any{"symbol_kind": facts.SymbolMethod, "receiver": "Config"}},
	}
	resolved := resolveCallTargets(ff)
	main := resolved[0]
	if !hasRelation(main, facts.RelCalls, "src.Config.load") {
		t.Errorf("expected call to src.Config.load, got relations: %v", main.Relations)
	}
}

func TestResolveCallTargets_SelfCall(t *testing.T) {
	ff := []facts.Fact{
		{Kind: facts.KindSymbol, Name: "src.Foo.bar", File: "src/main.rs",
			Props:     map[string]any{"symbol_kind": facts.SymbolMethod, "receiver": "Foo"},
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "self.baz"}}},
		{Kind: facts.KindSymbol, Name: "src.Foo.baz", File: "src/main.rs",
			Props: map[string]any{"symbol_kind": facts.SymbolMethod, "receiver": "Foo"}},
	}
	resolved := resolveCallTargets(ff)
	bar := resolved[0]
	if !hasRelation(bar, facts.RelCalls, "src.Foo.baz") {
		t.Errorf("expected self.baz to resolve to src.Foo.baz, got relations: %v", bar.Relations)
	}
}

func TestResolveCallTargets_UnresolvedKept(t *testing.T) {
	ff := []facts.Fact{
		{Kind: facts.KindSymbol, Name: "src.main", File: "src/main.rs", Props: map[string]any{"symbol_kind": facts.SymbolFunc},
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "external_crate.function"}}},
	}
	resolved := resolveCallTargets(ff)
	main := resolved[0]
	// Should keep unresolvable target as-is
	if !hasRelation(main, facts.RelCalls, "external_crate.function") {
		t.Errorf("unresolvable target should be kept as-is, got relations: %v", main.Relations)
	}
}

func TestResolveCallTargets_NonCallRelationsUntouched(t *testing.T) {
	ff := []facts.Fact{
		{Kind: facts.KindSymbol, Name: "src.Foo", File: "src/main.rs", Props: map[string]any{"symbol_kind": facts.SymbolStruct},
			Relations: []facts.Relation{
				{Kind: facts.RelDeclares, Target: "src"},
				{Kind: facts.RelImplements, Target: "Debug"},
			}},
	}
	resolved := resolveCallTargets(ff)
	foo := resolved[0]
	if !hasRelation(foo, facts.RelDeclares, "src") {
		t.Error("declares relation should be untouched")
	}
	if !hasRelation(foo, facts.RelImplements, "Debug") {
		t.Error("implements relation should be untouched")
	}
}

// --- Acceptance test: call target resolution ---
// Call targets must be resolved to fact names so that cohesion/coupling/dead-code
// analyses can link callers to callees. Without resolution, cohesion sees every
// function as a disconnected component (the parser repo shows 0.00 cohesion everywhere).

func TestAcceptance_CallTargetResolution(t *testing.T) {
	ff := extractFromString(t, `pub fn main() {
    setup_logging();
    let cfg = Config::load();
    let svc = Service::new(cfg);
    svc.run();
}

fn setup_logging() {
    init_tracing();
}

fn init_tracing() {}

pub struct Config {}

impl Config {
    pub fn load() -> Self {
        Self {}
    }
}

pub struct Service {
    cfg: Config,
}

impl Service {
    pub fn new(cfg: Config) -> Self {
        Self { cfg }
    }

    pub fn run(&self) {
        self.handle_request();
        self.cfg_valid();
        self.start_server();
    }

    fn handle_request(&self) {}
    fn cfg_valid(&self) -> bool { true }
    fn start_server(&self) {}
}
`)

	// 1. Simple function call: setup_logging() → resolved to "src.setup_logging"
	mainFn, ok := findFact(ff, "src.main")
	if !ok {
		t.Fatal("expected fact for src.main")
	}
	if !hasRelation(mainFn, facts.RelCalls, "src.setup_logging") {
		t.Error("main: setup_logging() should resolve to src.setup_logging")
	}

	// 2. Associated function: Config::load() → resolved to "src.Config.load"
	if !hasRelation(mainFn, facts.RelCalls, "src.Config.load") {
		t.Error("main: Config::load() should resolve to src.Config.load")
	}

	// 3. Associated function: Service::new() → resolved to "src.Service.new"
	if !hasRelation(mainFn, facts.RelCalls, "src.Service.new") {
		t.Error("main: Service::new() should resolve to src.Service.new")
	}

	// 4. Variable method call: svc.run() → resolved to "src.Service.run"
	//    (svc is unknown type, but Service.run exists and "run" matches)
	//    NOTE: if type can't be inferred, we keep "svc.run" unresolved — that's acceptable
	//    The critical case is self.method() resolution.

	// 5. Transitive: setup_logging() calls init_tracing() → "src.init_tracing"
	setupFn, ok := findFact(ff, "src.setup_logging")
	if !ok {
		t.Fatal("expected fact for src.setup_logging")
	}
	if !hasRelation(setupFn, facts.RelCalls, "src.init_tracing") {
		t.Error("setup_logging: init_tracing() should resolve to src.init_tracing")
	}

	// 6. self.method() calls in impl → resolved to "src.Service.handle_request" etc.
	runMethod, ok := findFact(ff, "src.Service.run")
	if !ok {
		t.Fatal("expected method fact for src.Service.run")
	}
	if !hasRelation(runMethod, facts.RelCalls, "src.Service.handle_request") {
		t.Error("Service.run: self.handle_request() should resolve to src.Service.handle_request")
	}
	if !hasRelation(runMethod, facts.RelCalls, "src.Service.cfg_valid") {
		t.Error("Service.run: self.cfg_valid() should resolve to src.Service.cfg_valid")
	}
	if !hasRelation(runMethod, facts.RelCalls, "src.Service.start_server") {
		t.Error("Service.run: self.start_server() should resolve to src.Service.start_server")
	}

	// 7. init_tracing still has no calls (empty body — verify no false positives)
	initFn, ok := findFact(ff, "src.init_tracing")
	if !ok {
		t.Fatal("expected fact for src.init_tracing")
	}
	for _, r := range initFn.Relations {
		if r.Kind == facts.RelCalls {
			t.Errorf("init_tracing should have no calls, found: %s", r.Target)
		}
	}
}

// --- Unit test: generic impl block methods ---

func TestExtractFile_GenericImplBlock(t *testing.T) {
	ff := extractFromString(t, `pub struct Scanner<'a> {
    data: &'a [u8],
}

impl<'a> Scanner<'a> {
    pub fn new(data: &'a [u8]) -> Self {
        Self { data }
    }

    fn peek(&self) -> u8 {
        0
    }

    fn advance(&mut self) {
        self.peek();
    }
}
`)
	// Methods should have receiver = "Scanner" (stripped of generics)
	newFn, ok := findFact(ff, "src.Scanner.new")
	if !ok {
		t.Fatal("expected method fact for src.Scanner.new")
	}
	if newFn.Props["receiver"] != "Scanner" {
		t.Errorf("Scanner.new receiver = %v, want Scanner", newFn.Props["receiver"])
	}

	peek, ok := findFact(ff, "src.Scanner.peek")
	if !ok {
		t.Fatal("expected method fact for src.Scanner.peek")
	}
	if peek.Props["receiver"] != "Scanner" {
		t.Errorf("Scanner.peek receiver = %v, want Scanner", peek.Props["receiver"])
	}

	// self.peek() in advance should resolve to src.Scanner.peek
	advance, ok := findFact(ff, "src.Scanner.advance")
	if !ok {
		t.Fatal("expected method fact for src.Scanner.advance")
	}
	if !hasRelation(advance, facts.RelCalls, "src.Scanner.peek") {
		t.Error("Scanner.advance: self.peek() should resolve to src.Scanner.peek")
		for _, r := range advance.Relations {
			if r.Kind == facts.RelCalls {
				t.Logf("  calls: %s", r.Target)
			}
		}
	}
}

// --- Acceptance test: workspace cross-crate dependency resolution ---
// In a Cargo workspace, `use other_crate::module::Symbol;` should create
// a dependency edge from the importing module to the target crate's module.
// Without this, coupling analysis reports 0% for all workspace crates.

func TestAcceptance_WorkspaceCrossCrateDeps(t *testing.T) {
	dir := t.TempDir()

	// Root workspace Cargo.toml — also the "mylib" crate
	rootCargo := `[workspace]
members = ["crates/*"]

[package]
name = "mylib"
version = "0.1.0"
edition = "2021"

[dependencies]
serde = "1"
`
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(rootCargo), 0o644); err != nil {
		t.Fatal(err)
	}

	// Root crate source with a submodule
	if err := os.MkdirAll(filepath.Join(dir, "src", "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "lib.rs"), []byte("pub mod models;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "models", "mod.rs"), []byte("pub fn load() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Member crate: mylib-cli depends on root crate via path
	cliDir := filepath.Join(dir, "crates", "mylib-cli")
	if err := os.MkdirAll(filepath.Join(cliDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	cliCargo := `[package]
name = "mylib-cli"
version = "0.1.0"
edition = "2021"

[dependencies]
mylib = { path = "../.." }
`
	if err := os.WriteFile(filepath.Join(cliDir, "Cargo.toml"), []byte(cliCargo), 0o644); err != nil {
		t.Fatal(err)
	}

	// CLI source: uses mylib::models (cross-crate import)
	cliSrc := `use mylib::models;

pub fn run() {
    models::load();
}
`
	if err := os.WriteFile(filepath.Join(cliDir, "src", "lib.rs"), []byte(cliSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	ext := New()
	files := []string{
		"src/lib.rs",
		"src/models/mod.rs",
		"crates/mylib-cli/src/lib.rs",
	}

	allFacts, err := ext.Extract(context.Background(), dir, files)
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}

	// The import `use mylib::models` from crates/mylib-cli/src should
	// create a dependency with target pointing to the resolved module "src/models"
	// (because "mylib" is an internal path dep whose src is at the root "src/").
	deps := findFactByKind(allFacts, facts.KindDependency)

	foundCrossImport := false
	for _, d := range deps {
		if d.File != "crates/mylib-cli/src/lib.rs" {
			continue
		}
		for _, r := range d.Relations {
			if r.Kind == facts.RelImports && r.Target == "src/models" {
				foundCrossImport = true
			}
		}
	}
	if !foundCrossImport {
		t.Error("expected cross-crate import from mylib-cli → src/models (resolved through workspace)")
		t.Log("Dependencies from cli/src/lib.rs:")
		for _, d := range deps {
			if d.File == "crates/mylib-cli/src/lib.rs" {
				for _, r := range d.Relations {
					t.Logf("  %s → %s", r.Kind, r.Target)
				}
			}
		}
	}
}

// --- Acceptance test: cross-file call resolution within a module ---
// Functions in file A calling functions in file B (same directory/module)
// must resolve to fact names so cohesion analysis can connect them.

func TestAcceptance_CrossFileCallResolution(t *testing.T) {
	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"testcrate\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// File A: calls validate() which is defined in file B
	fileA := `pub fn process(input: &str) -> bool {
    validate(input)
}
`
	if err := os.WriteFile(filepath.Join(dir, "src", "process.rs"), []byte(fileA), 0o644); err != nil {
		t.Fatal(err)
	}

	// File B: defines validate() and calls helper() from same file
	fileB := `pub fn validate(input: &str) -> bool {
    helper(input)
}

fn helper(input: &str) -> bool {
    !input.is_empty()
}
`
	if err := os.WriteFile(filepath.Join(dir, "src", "validate.rs"), []byte(fileB), 0o644); err != nil {
		t.Fatal(err)
	}

	ext := New()
	allFacts, err := ext.Extract(context.Background(), dir, []string{
		"src/process.rs",
		"src/validate.rs",
	})
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}

	// process() in file A calls validate() in file B → should resolve to "src.validate"
	processFn, ok := findFact(allFacts, "src.process")
	if !ok {
		t.Fatal("expected fact for src.process")
	}
	if !hasRelation(processFn, facts.RelCalls, "src.validate") {
		t.Error("process() should have cross-file call to src.validate (defined in validate.rs)")
		for _, r := range processFn.Relations {
			if r.Kind == facts.RelCalls {
				t.Logf("  calls: %s", r.Target)
			}
		}
	}

	// validate() calls helper() in same file → already resolved by per-file pass
	validateFn, ok := findFact(allFacts, "src.validate")
	if !ok {
		t.Fatal("expected fact for src.validate")
	}
	if !hasRelation(validateFn, facts.RelCalls, "src.helper") {
		t.Error("validate() should have same-file call to src.helper")
	}
}

// --- Acceptance test: mod-relative use statements ---
// `use sibling_mod::Symbol` where sibling_mod is a local `mod` declaration
// should be classified as internal, not external.

func TestAcceptance_ModRelativeUseStatements(t *testing.T) {
	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"testcrate\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// lib.rs declares mod utils; then uses utils::helper
	libRs := `mod utils;

use utils::helper;

pub fn main() {
    helper();
}
`
	if err := os.WriteFile(filepath.Join(dir, "src", "lib.rs"), []byte(libRs), 0o644); err != nil {
		t.Fatal(err)
	}

	utilsRs := `pub fn helper() {}
`
	if err := os.WriteFile(filepath.Join(dir, "src", "utils.rs"), []byte(utilsRs), 0o644); err != nil {
		t.Fatal(err)
	}

	ext := New()
	allFacts, err := ext.Extract(context.Background(), dir, []string{
		"src/lib.rs",
		"src/utils.rs",
	})
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}

	// `use utils::helper` should be classified as internal, not external
	deps := findFactByKind(allFacts, facts.KindDependency)
	for _, d := range deps {
		if d.File != "src/lib.rs" {
			continue
		}
		for _, r := range d.Relations {
			if r.Kind == facts.RelImports && r.Target == "utils" {
				source, _ := d.Props["source"].(string)
				if source != "internal" {
					t.Errorf("use utils::helper should be internal, got source=%q", source)
				}
				return // found and checked
			}
		}
	}
	// If we get here, the import wasn't found at all as "utils"
	t.Log("Dependencies from src/lib.rs:")
	for _, d := range deps {
		if d.File == "src/lib.rs" {
			for _, r := range d.Relations {
				t.Logf("  %s → %s (source=%v)", r.Kind, r.Target, d.Props["source"])
			}
		}
	}
	t.Error("expected to find import targeting 'utils' from src/lib.rs")
}

// --- Acceptance test: crate:: imports resolve to module dirs ---

func TestAcceptance_CrateImportsResolveToModuleDirs(t *testing.T) {
	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, "src", "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"testcrate\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mainRs := `use crate::models::User;

pub fn run() {}
`
	if err := os.WriteFile(filepath.Join(dir, "src", "main.rs"), []byte(mainRs), 0o644); err != nil {
		t.Fatal(err)
	}

	modelsRs := `pub struct User {}
`
	if err := os.WriteFile(filepath.Join(dir, "src", "models", "mod.rs"), []byte(modelsRs), 0o644); err != nil {
		t.Fatal(err)
	}

	ext := New()
	allFacts, err := ext.Extract(context.Background(), dir, []string{
		"src/main.rs",
		"src/models/mod.rs",
	})
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}

	// `use crate::models::User` should produce an import targeting "src/models"
	// (the module directory), not "crate::models::User" (raw Rust path)
	deps := findFactByKind(allFacts, facts.KindDependency)
	foundModuleImport := false
	for _, d := range deps {
		if d.File != "src/main.rs" {
			continue
		}
		for _, r := range d.Relations {
			if r.Kind == facts.RelImports && r.Target == "src/models" {
				foundModuleImport = true
			}
		}
	}
	if !foundModuleImport {
		t.Error("use crate::models::User should resolve import target to src/models")
		t.Log("Dependencies from src/main.rs:")
		for _, d := range deps {
			if d.File == "src/main.rs" {
				for _, r := range d.Relations {
					t.Logf("  %s → %s (source=%v)", r.Kind, r.Target, d.Props["source"])
				}
			}
		}
	}
}

// --- Acceptance test: generics wired into pipeline (type_params in Props) ---
// Verifies that extractTypeParams, extractWhereClause, extractReturnType
// are actually called from the extraction pipeline and their results appear
// in the fact Props map.

func TestAcceptance_GenericsWiredIntoPipeline(t *testing.T) {
	ff := extractFromString(t, `pub struct HashMap<K, V> {
    data: Vec<(K, V)>,
}

pub enum Result<T, E> {
    Ok(T),
    Err(E),
}

pub fn transform<T: Clone, U>(input: T) -> U where U: From<T> {
    U::from(input)
}

pub trait Iterator<Item> {
    fn next(&mut self) -> Option<Item>;
}

pub struct Scanner<'a> {
    data: &'a [u8],
}

impl<'a> Scanner<'a> {
    pub fn new(data: &'a [u8]) -> Self {
        Self { data }
    }

    pub fn peek(&self) -> u8 {
        0
    }
}
`)

	// 1. Struct with type_params
	hm, ok := findFact(ff, "src.HashMap")
	if !ok {
		t.Fatal("expected fact for src.HashMap")
	}
	tp, _ := hm.Props["type_params"].([]string)
	if len(tp) != 2 || tp[0] != "K" || tp[1] != "V" {
		t.Errorf("HashMap type_params = %v, want [K V]", tp)
	}

	// 2. Enum with type_params
	res, ok := findFact(ff, "src.Result")
	if !ok {
		t.Fatal("expected fact for src.Result")
	}
	tp, _ = res.Props["type_params"].([]string)
	if len(tp) != 2 || tp[0] != "T" || tp[1] != "E" {
		t.Errorf("Result type_params = %v, want [T E]", tp)
	}

	// 3. Function with type_params, bounds, where_clause, return_type
	xform, ok := findFact(ff, "src.transform")
	if !ok {
		t.Fatal("expected fact for src.transform")
	}
	tp, _ = xform.Props["type_params"].([]string)
	if len(tp) != 2 || tp[0] != "T" || tp[1] != "U" {
		t.Errorf("transform type_params = %v, want [T U]", tp)
	}
	bounds, _ := xform.Props["bounds"].(map[string][]string)
	if bounds == nil || len(bounds["T"]) == 0 {
		t.Errorf("transform bounds = %v, want T: [Clone]", bounds)
	}
	wc, _ := xform.Props["where_clause"].(string)
	if wc == "" {
		t.Error("transform where_clause should not be empty")
	}
	rt, _ := xform.Props["return_type"].(string)
	if rt != "U" {
		t.Errorf("transform return_type = %q, want U", rt)
	}

	// 4. Trait with type_params
	iter, ok := findFact(ff, "src.Iterator")
	if !ok {
		t.Fatal("expected fact for src.Iterator")
	}
	tp, _ = iter.Props["type_params"].([]string)
	if len(tp) != 1 || tp[0] != "Item" {
		t.Errorf("Iterator type_params = %v, want [Item]", tp)
	}

	// 5. Struct with lifetime
	scanner, ok := findFact(ff, "src.Scanner")
	if !ok {
		t.Fatal("expected fact for src.Scanner")
	}
	lts, _ := scanner.Props["lifetimes"].([]string)
	if len(lts) != 1 || lts[0] != "'a" {
		t.Errorf("Scanner lifetimes = %v, want ['a]", lts)
	}

	// 6. Impl method gets impl-level lifetimes + return_type
	newFn, ok := findFact(ff, "src.Scanner.new")
	if !ok {
		t.Fatal("expected method fact for src.Scanner.new")
	}
	lts, _ = newFn.Props["lifetimes"].([]string)
	if len(lts) != 1 || lts[0] != "'a" {
		t.Errorf("Scanner.new lifetimes = %v, want ['a] (inherited from impl)", lts)
	}
	rt, _ = newFn.Props["return_type"].(string)
	if rt != "Self" {
		t.Errorf("Scanner.new return_type = %q, want Self", rt)
	}

	// 7. Method return type
	peekFn, ok := findFact(ff, "src.Scanner.peek")
	if !ok {
		t.Fatal("expected method fact for src.Scanner.peek")
	}
	rt, _ = peekFn.Props["return_type"].(string)
	if rt != "u8" {
		t.Errorf("Scanner.peek return_type = %q, want u8", rt)
	}
}

// --- Acceptance test: module hierarchy wired into pipeline ---
// Verifies that buildModuleHierarchy is called from Extract() and
// produces module facts with parent_module relationships.

func TestAcceptance_ModuleHierarchyWiredIntoPipeline(t *testing.T) {
	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, "src", "handlers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"testcrate\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// lib.rs declares mod handlers; and mod utils;
	libRs := `pub mod handlers;
mod utils;
`
	if err := os.WriteFile(filepath.Join(dir, "src", "lib.rs"), []byte(libRs), 0o644); err != nil {
		t.Fatal(err)
	}

	// handlers/mod.rs declares mod routes;
	handlersModRs := `pub mod routes;
`
	if err := os.WriteFile(filepath.Join(dir, "src", "handlers", "mod.rs"), []byte(handlersModRs), 0o644); err != nil {
		t.Fatal(err)
	}

	ext := New()
	allFacts, err := ext.Extract(context.Background(), dir, []string{
		"src/lib.rs",
		"src/handlers/mod.rs",
	})
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}

	modules := findFactByKind(allFacts, facts.KindModule)

	// Check for hierarchy-derived module facts with parent_module
	foundHandlersHierarchy := false
	foundUtilsHierarchy := false
	foundRoutesHierarchy := false
	for _, m := range modules {
		parent, _ := m.Props["parent_module"].(string)
		switch {
		case m.Name == "src/handlers" && parent == "src":
			foundHandlersHierarchy = true
		case m.Name == "src/utils" && parent == "src":
			foundUtilsHierarchy = true
		case m.Name == "src/handlers/routes" && parent == "src/handlers":
			foundRoutesHierarchy = true
		}
	}

	if !foundHandlersHierarchy {
		t.Error("expected module hierarchy fact for src/handlers with parent_module=src")
	}
	if !foundUtilsHierarchy {
		t.Error("expected module hierarchy fact for src/utils with parent_module=src")
	}
	if !foundRoutesHierarchy {
		t.Error("expected module hierarchy fact for src/handlers/routes with parent_module=src/handlers")
	}
}

// containsStr checks if a string slice contains a specific value.
func containsStr(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
