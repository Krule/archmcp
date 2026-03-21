package rubyextractor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dejo1307/archmcp/internal/facts"
)

// --- Test helpers (following rustextractor/rust_test.go pattern) ---

// extractFromRubyString writes Ruby source to a temp file and runs extractFileAST.
func extractFromRubyString(t *testing.T, src string, relFile string, isRails bool, exported bool) []facts.Fact {
	t.Helper()
	return extractFileAST([]byte(src), relFile, isRails, exported)
}

// findFact finds a fact by name.
func findFact(ff []facts.Fact, name string) (facts.Fact, bool) {
	for _, f := range ff {
		if f.Name == name {
			return f, true
		}
	}
	return facts.Fact{}, false
}

// findFactsByKind returns all facts of a given kind.
func findFactsByKind(ff []facts.Fact, kind string) []facts.Fact {
	var result []facts.Fact
	for _, f := range ff {
		if f.Kind == kind {
			result = append(result, f)
		}
	}
	return result
}

// hasRelation checks if a fact has a specific relation.
func hasRelation(f facts.Fact, relKind, target string) bool {
	for _, r := range f.Relations {
		if r.Kind == relKind && r.Target == target {
			return true
		}
	}
	return false
}

// assertFact asserts a fact exists with given name and returns it.
func assertFact(t *testing.T, ff []facts.Fact, name string) facts.Fact {
	t.Helper()
	f, ok := findFact(ff, name)
	if !ok {
		t.Fatalf("missing fact %q", name)
	}
	return f
}

// assertSymbolKind asserts the symbol_kind property of a fact.
func assertSymbolKind(t *testing.T, f facts.Fact, want string) {
	t.Helper()
	sk, _ := f.Props["symbol_kind"].(string)
	if sk != want {
		t.Errorf("%s symbol_kind = %q, want %q", f.Name, sk, want)
	}
}

// assertProp asserts a string property value.
func assertProp(t *testing.T, f facts.Fact, key, want string) {
	t.Helper()
	got, _ := f.Props[key].(string)
	if got != want {
		t.Errorf("%s prop[%q] = %q, want %q", f.Name, key, got, want)
	}
}

// assertBoolProp asserts a boolean property value.
func assertBoolProp(t *testing.T, f facts.Fact, key string, want bool) {
	t.Helper()
	got, _ := f.Props[key].(bool)
	if got != want {
		t.Errorf("%s prop[%q] = %v, want %v", f.Name, key, got, want)
	}
}

func TestExtractFile_BasicClassAndMethod(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "order.rb")
	src := `# frozen_string_literal: true

module Orders
  class Order < ApplicationRecord
    def total
      items.sum(:price)
    end

    def self.recent
      where("created_at > ?", 1.day.ago)
    end
  end
end
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	result := extractFile(f, "packages/orders/app/models/order.rb", true, false)

	// Collect by kind and name.
	byName := make(map[string]facts.Fact)
	for _, fact := range result {
		byName[fact.Name] = fact
	}

	// Module Orders.
	mod, ok := byName["Orders"]
	if !ok {
		t.Fatal("missing module Orders")
	}
	if mod.Kind != facts.KindSymbol {
		t.Errorf("Orders kind = %q, want symbol", mod.Kind)
	}
	sk, _ := mod.Props["symbol_kind"].(string)
	if sk != facts.SymbolInterface {
		t.Errorf("Orders symbol_kind = %q, want interface", sk)
	}

	// Class Orders::Order.
	cls, ok := byName["Orders::Order"]
	if !ok {
		t.Fatal("missing class Orders::Order")
	}
	if cls.Kind != facts.KindSymbol {
		t.Errorf("Orders::Order kind = %q, want symbol", cls.Kind)
	}
	sk, _ = cls.Props["symbol_kind"].(string)
	if sk != facts.SymbolClass {
		t.Errorf("Orders::Order symbol_kind = %q, want class", sk)
	}
	superclass, _ := cls.Props["superclass"].(string)
	if superclass != "ApplicationRecord" {
		t.Errorf("superclass = %q, want ApplicationRecord", superclass)
	}
	// Should have implements relation to ApplicationRecord.
	hasImpl := false
	for _, r := range cls.Relations {
		if r.Kind == facts.RelImplements && r.Target == "ApplicationRecord" {
			hasImpl = true
		}
	}
	if !hasImpl {
		t.Error("Orders::Order missing implements relation to ApplicationRecord")
	}

	// Instance method Orders::Order#total.
	meth, ok := byName["Orders::Order#total"]
	if !ok {
		t.Fatal("missing method Orders::Order#total")
	}
	sk, _ = meth.Props["symbol_kind"].(string)
	if sk != facts.SymbolMethod {
		t.Errorf("total symbol_kind = %q, want method", sk)
	}

	// Class method Orders::Order.recent.
	cmeth, ok := byName["Orders::Order.recent"]
	if !ok {
		t.Fatal("missing class method Orders::Order.recent")
	}
	sk, _ = cmeth.Props["symbol_kind"].(string)
	if sk != facts.SymbolFunc {
		t.Errorf("recent symbol_kind = %q, want function", sk)
	}
}

func TestStorageFacts_DeclaresTargetIsDirectory(t *testing.T) {
	relFile := "packages/items/app/models/item.rb"

	fileFacts := []facts.Fact{
		{
			Kind: facts.KindSymbol,
			Name: "Item",
			File: relFile,
			Line: 3,
			Props: map[string]any{
				"symbol_kind": facts.SymbolClass,
				"superclass":  "ApplicationRecord",
				"language":    "ruby",
			},
		},
	}

	result := extractStorageFacts(relFile, fileFacts)
	if len(result) == 0 {
		t.Fatal("expected at least one storage fact")
	}

	storageFact := result[0]
	if storageFact.Name != "Item" {
		t.Errorf("storage fact name = %q, want Item", storageFact.Name)
	}

	// The declares target must be the directory, not the class name.
	if len(storageFact.Relations) == 0 {
		t.Fatal("storage fact has no relations")
	}
	declTarget := storageFact.Relations[0].Target
	want := "packages/items/app/models"
	if declTarget != want {
		t.Errorf("declares target = %q, want %q", declTarget, want)
	}
	if declTarget == "Item" {
		t.Error("declares target must not be the class name (self-loop)")
	}
}

func TestAssociationFactNames_IncludeFilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "order.rb")
	src := `class Order < ApplicationRecord
  belongs_to :user
  has_many :items
end
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	relFile := "packages/orders/app/models/order.rb"
	result := extractAssociationsFromFile(path, relFile)

	if len(result) == 0 {
		t.Fatal("expected association facts")
	}

	for _, fact := range result {
		if fact.Kind != facts.KindDependency {
			continue
		}
		if !strings.HasPrefix(fact.Name, relFile+":") {
			t.Errorf("association fact name %q should start with file path %q", fact.Name, relFile+":")
		}
	}

	// Verify specific associations.
	names := make(map[string]bool)
	for _, fact := range result {
		names[fact.Name] = true
	}
	if !names[relFile+":belongs_to :user"] {
		t.Error("missing belongs_to :user with file prefix")
	}
	if !names[relFile+":has_many :items"] {
		t.Error("missing has_many :items with file prefix")
	}
}

// --- RelCalls extraction tests ---

// hasCall returns true if the fact has a RelCalls relation to target.
func hasCall(f facts.Fact, target string) bool {
	for _, r := range f.Relations {
		if r.Kind == facts.RelCalls && r.Target == target {
			return true
		}
	}
	return false
}

func TestExtractFile_QualifiedClassMethodCall(t *testing.T) {
	src := `module Items
  class FetchService
    def call(ids)
      Items::Facade.fetch_item_fields(ids, ITEM_FIELDS)
    end
  end
end
`
	dir := t.TempDir()
	path := filepath.Join(dir, "fetch_service.rb")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	result := extractFile(f, "packages/items/app/services/fetch_service.rb", false, true)

	byName := make(map[string]facts.Fact)
	for _, fact := range result {
		byName[fact.Name] = fact
	}

	meth, ok := byName["Items::FetchService#call"]
	if !ok {
		t.Fatal("missing method Items::FetchService#call")
	}
	if !hasCall(meth, "Items::Facade.fetch_item_fields") {
		t.Errorf("Items::FetchService#call missing RelCalls -> Items::Facade.fetch_item_fields; relations = %v", meth.Relations)
	}
}

func TestExtractFile_MultiLevelNamespaceCall(t *testing.T) {
	src := `module HomepageSources
  class Builder
    def build(ids)
      HomepageSources::ItemDto.from_ids(ids)
    end
  end
end
`
	dir := t.TempDir()
	path := filepath.Join(dir, "builder.rb")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	result := extractFile(f, "packages/homepage_sources/app/builder.rb", false, true)

	byName := make(map[string]facts.Fact)
	for _, fact := range result {
		byName[fact.Name] = fact
	}

	meth, ok := byName["HomepageSources::Builder#build"]
	if !ok {
		t.Fatal("missing method HomepageSources::Builder#build")
	}
	if !hasCall(meth, "HomepageSources::ItemDto.from_ids") {
		t.Errorf("HomepageSources::Builder#build missing RelCalls -> HomepageSources::ItemDto.from_ids; relations = %v", meth.Relations)
	}
}

func TestExtractFile_ReceiverVariableCall(t *testing.T) {
	src := `class OrderProcessor
  def process(order)
    service.call(order)
  end
end
`
	dir := t.TempDir()
	path := filepath.Join(dir, "order_processor.rb")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	result := extractFile(f, "app/models/order_processor.rb", false, true)

	byName := make(map[string]facts.Fact)
	for _, fact := range result {
		byName[fact.Name] = fact
	}

	meth, ok := byName["OrderProcessor#process"]
	if !ok {
		t.Fatal("missing method OrderProcessor#process")
	}
	if !hasCall(meth, "service.call") {
		t.Errorf("OrderProcessor#process missing RelCalls -> service.call; relations = %v", meth.Relations)
	}
}

func TestExtractFile_CallsDeduplication(t *testing.T) {
	src := `class Dispatcher
  def run(ids)
    Items::Facade.fetch_item_fields(ids, FIELDS)
    Items::Facade.fetch_item_fields(ids, OTHER_FIELDS)
  end
end
`
	dir := t.TempDir()
	path := filepath.Join(dir, "dispatcher.rb")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	result := extractFile(f, "app/dispatcher.rb", false, true)

	byName := make(map[string]facts.Fact)
	for _, fact := range result {
		byName[fact.Name] = fact
	}

	meth, ok := byName["Dispatcher#run"]
	if !ok {
		t.Fatal("missing method Dispatcher#run")
	}

	count := 0
	for _, r := range meth.Relations {
		if r.Kind == facts.RelCalls && r.Target == "Items::Facade.fetch_item_fields" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 RelCalls edge to Items::Facade.fetch_item_fields, got %d", count)
	}
}

func TestExtractFile_TopLevelMethodCalls(t *testing.T) {
	// Ruby allows method calls without parentheses; qualifiedCallRe must capture
	// them even when there is no trailing '(' character.
	src := `def bootstrap
  Config.load_defaults
  Rails.application.initialize!
end
`
	dir := t.TempDir()
	path := filepath.Join(dir, "init.rb")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	result := extractFile(f, "config/init.rb", false, true)

	byName := make(map[string]facts.Fact)
	for _, fact := range result {
		byName[fact.Name] = fact
	}

	meth, ok := byName["config.bootstrap"]
	if !ok {
		t.Fatal("missing top-level method config.bootstrap")
	}
	// Config.load_defaults has no parens — qualifiedCallRe must still fire.
	if !hasCall(meth, "Config.load_defaults") {
		t.Errorf("config.bootstrap missing RelCalls -> Config.load_defaults; relations = %v", meth.Relations)
	}
	// Rails.application.initialize! — qualifiedCallRe captures the first segment: Rails.application.
	if !hasCall(meth, "Rails.application") {
		t.Errorf("config.bootstrap missing RelCalls -> Rails.application; relations = %v", meth.Relations)
	}
}

func TestExtractFile_EndlessMethodCall(t *testing.T) {
	// Ruby 3.0+ endless method: def name(args) = Expr.call(args)
	// The call is on the same line as the def — must be captured directly.
	src := `module HomepageSources
  class ItemDto
    ITEM_FIELDS = %i[id title].freeze

    def fields_by_id(item_ids) = Items::Facade.fetch_item_fields(item_ids, ITEM_FIELDS).index_by { |item| item[:id] }
  end
end
`
	dir := t.TempDir()
	path := filepath.Join(dir, "item_dto.rb")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	result := extractFile(f, "packages/homepage_sources/app/public/homepage_sources/item_dto.rb", false, true)

	byName := make(map[string]facts.Fact)
	for _, fact := range result {
		byName[fact.Name] = fact
	}

	meth, ok := byName["HomepageSources::ItemDto#fields_by_id"]
	if !ok {
		t.Fatal("missing method HomepageSources::ItemDto#fields_by_id")
	}
	if !hasCall(meth, "Items::Facade.fetch_item_fields") {
		t.Errorf("fields_by_id missing RelCalls -> Items::Facade.fetch_item_fields; relations = %v", meth.Relations)
	}
}

func TestExtractRubyCalls_QualifiedAndReceiver(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{
			line: "      Items::Facade.fetch_item_fields(ids, ITEM_FIELDS)",
			want: []string{"Items::Facade.fetch_item_fields"},
		},
		{
			line: "      service.call(x)",
			want: []string{"service.call"},
		},
		{
			line: "      Foo::Bar::Baz.do_thing(a, b)",
			want: []string{"Foo::Bar::Baz.do_thing"},
		},
		{
			// Chained call: qualifiedCallRe captures Rails.logger (first segment);
			// receiverCallRe captures logger.info (lowercase receiver with parens).
			line: "      Rails.logger.info('msg')",
			want: []string{"Rails.logger", "logger.info"},
		},
	}

	for _, tc := range cases {
		got := extractRubyCalls(tc.line)
		gotSet := make(map[string]bool)
		for _, g := range got {
			gotSet[g] = true
		}
		for _, w := range tc.want {
			if !gotSet[w] {
				t.Errorf("extractRubyCalls(%q): missing %q in %v", tc.line, w, got)
			}
		}
	}
}

func TestPackwerk_RootDependencyNormalization(t *testing.T) {
	dir := t.TempDir()

	// Create packwerk.yml.
	packwerkYml := `package_paths:
  - "."
  - "packages/*"
`
	if err := os.WriteFile(filepath.Join(dir, "packwerk.yml"), []byte(packwerkYml), 0o644); err != nil {
		t.Fatal(err)
	}

	// Root package.yml.
	rootPkg := `enforce_dependencies: true
`
	if err := os.WriteFile(filepath.Join(dir, "package.yml"), []byte(rootPkg), 0o644); err != nil {
		t.Fatal(err)
	}

	// A sub-package that depends on root (".").
	pkgDir := filepath.Join(dir, "packages", "orders")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ordersPkg := `enforce_dependencies: true
dependencies:
  - "."
  - "packages/payments"
`
	if err := os.WriteFile(filepath.Join(pkgDir, "package.yml"), []byte(ordersPkg), 0o644); err != nil {
		t.Fatal(err)
	}

	info := parsePackwerk(dir)
	if !info.detected {
		t.Fatal("packwerk should be detected")
	}

	// Find the orders module fact.
	var ordersFact *facts.Fact
	for i, f := range info.facts {
		if f.Name == "packages/orders" {
			ordersFact = &info.facts[i]
			break
		}
	}
	if ordersFact == nil {
		t.Fatal("missing packages/orders module fact")
	}

	// The dependency on "." should be normalized to "root".
	hasDotTarget := false
	hasRootTarget := false
	for _, r := range ordersFact.Relations {
		if r.Kind == facts.RelDependsOn {
			if r.Target == "." {
				hasDotTarget = true
			}
			if r.Target == "root" {
				hasRootTarget = true
			}
		}
	}
	if hasDotTarget {
		t.Error("dependency target '.' should have been normalized to 'root'")
	}
	if !hasRootTarget {
		t.Error("expected dependency target 'root' after normalization")
	}

	// The root module should be named "root", not ".".
	var rootFact *facts.Fact
	for i, f := range info.facts {
		if f.Name == "root" {
			rootFact = &info.facts[i]
			break
		}
	}
	if rootFact == nil {
		t.Fatal("missing root module fact (should be named 'root', not '.')")
	}
}

// --- Comprehensive characterization tests ---

// 1. Symbols: module
func TestExtractFile_Module(t *testing.T) {
	src := `module Payments
end
`
	result := extractFromRubyString(t, src, "app/models/payments.rb", false, true)
	f := assertFact(t, result, "Payments")
	if f.Kind != facts.KindSymbol {
		t.Errorf("kind = %q, want symbol", f.Kind)
	}
	assertSymbolKind(t, f, facts.SymbolInterface)
	assertProp(t, f, "language", "ruby")
	if !hasRelation(f, facts.RelDeclares, "app/models") {
		t.Error("missing declares relation to directory")
	}
}

func TestExtractFile_NestedModule(t *testing.T) {
	src := `module Payments
  module Gateway
  end
end
`
	result := extractFromRubyString(t, src, "app/models/gateway.rb", false, true)
	assertFact(t, result, "Payments")
	f := assertFact(t, result, "Payments::Gateway")
	assertSymbolKind(t, f, facts.SymbolInterface)
}

func TestExtractFile_ClassNoInheritance(t *testing.T) {
	src := `class Validator
end
`
	result := extractFromRubyString(t, src, "lib/validator.rb", false, true)
	f := assertFact(t, result, "Validator")
	assertSymbolKind(t, f, facts.SymbolClass)
	// No superclass prop
	if _, ok := f.Props["superclass"]; ok {
		t.Error("should not have superclass prop")
	}
}

func TestExtractFile_ClassInheritance(t *testing.T) {
	src := `class User < ApplicationRecord
end
`
	result := extractFromRubyString(t, src, "app/models/user.rb", false, true)
	f := assertFact(t, result, "User")
	assertSymbolKind(t, f, facts.SymbolClass)
	assertProp(t, f, "superclass", "ApplicationRecord")
	if !hasRelation(f, facts.RelImplements, "ApplicationRecord") {
		t.Error("missing implements relation")
	}
}

func TestExtractFile_ClassScopeResolutionSuperclass(t *testing.T) {
	src := `class LegacyUser < ActiveRecord::Base
end
`
	result := extractFromRubyString(t, src, "app/models/legacy.rb", false, true)
	f := assertFact(t, result, "LegacyUser")
	assertProp(t, f, "superclass", "ActiveRecord::Base")
}

func TestExtractFile_InstanceMethod(t *testing.T) {
	src := `class Greeter
  def greet(name)
  end
end
`
	result := extractFromRubyString(t, src, "lib/greeter.rb", false, true)
	f := assertFact(t, result, "Greeter#greet")
	assertSymbolKind(t, f, facts.SymbolMethod)
}

func TestExtractFile_ClassMethodSelf(t *testing.T) {
	src := `class Factory
  def self.build
  end
end
`
	result := extractFromRubyString(t, src, "lib/factory.rb", false, true)
	f := assertFact(t, result, "Factory.build")
	assertSymbolKind(t, f, facts.SymbolFunc)
}

func TestExtractFile_SingletonClass(t *testing.T) {
	src := `class Config
  class << self
    def load
    end
  end
end
`
	result := extractFromRubyString(t, src, "lib/config.rb", false, true)
	f := assertFact(t, result, "Config.load")
	assertSymbolKind(t, f, facts.SymbolFunc)
}

func TestExtractFile_Constant(t *testing.T) {
	src := `class Settings
  MAX_RETRIES = 3
  DEFAULT_TIMEOUT = 30
end
`
	result := extractFromRubyString(t, src, "lib/settings.rb", false, true)
	f := assertFact(t, result, "Settings::MAX_RETRIES")
	assertSymbolKind(t, f, facts.SymbolConstant)
	f2 := assertFact(t, result, "Settings::DEFAULT_TIMEOUT")
	assertSymbolKind(t, f2, facts.SymbolConstant)
}

func TestExtractFile_ConstantMustBeAllCaps(t *testing.T) {
	src := `class Foo
  MyClass = Class.new
  VERSION = "1.0"
end
`
	result := extractFromRubyString(t, src, "lib/foo.rb", false, true)
	// MyClass is not ALL_CAPS, should not be a constant fact
	_, found := findFact(result, "Foo::MyClass")
	if found {
		t.Error("MyClass should not be treated as constant (not ALL_CAPS)")
	}
	// VERSION is ALL_CAPS
	assertFact(t, result, "Foo::VERSION")
}

func TestExtractFile_AttrReader(t *testing.T) {
	src := `class Person
  attr_reader :name, :age
end
`
	result := extractFromRubyString(t, src, "lib/person.rb", false, true)
	f := assertFact(t, result, "Person#name")
	assertSymbolKind(t, f, facts.SymbolVariable)
	assertProp(t, f, "attr_kind", "reader")
	f2 := assertFact(t, result, "Person#age")
	assertSymbolKind(t, f2, facts.SymbolVariable)
}

func TestExtractFile_AttrWriter(t *testing.T) {
	src := `class Person
  attr_writer :email
end
`
	result := extractFromRubyString(t, src, "lib/person.rb", false, true)
	f := assertFact(t, result, "Person#email")
	assertSymbolKind(t, f, facts.SymbolVariable)
	assertProp(t, f, "attr_kind", "writer")
}

func TestExtractFile_AttrAccessor(t *testing.T) {
	src := `class Car
  attr_accessor :color, :speed
end
`
	result := extractFromRubyString(t, src, "lib/car.rb", false, true)
	f := assertFact(t, result, "Car#color")
	assertProp(t, f, "attr_kind", "accessor")
	assertFact(t, result, "Car#speed")
}

// --- Visibility tests ---

func TestExtractFile_PrivateMethod(t *testing.T) {
	src := `class Service
  def public_method
  end

  private

  def secret_method
  end
end
`
	result := extractFromRubyString(t, src, "lib/service.rb", false, true)
	pub := assertFact(t, result, "Service#public_method")
	assertBoolProp(t, pub, "exported", true)
	priv := assertFact(t, result, "Service#secret_method")
	assertBoolProp(t, priv, "exported", false)
}

func TestExtractFile_ProtectedMethod(t *testing.T) {
	src := `class Base
  protected

  def helper
  end
end
`
	result := extractFromRubyString(t, src, "lib/base.rb", false, true)
	f := assertFact(t, result, "Base#helper")
	assertBoolProp(t, f, "exported", false)
}

func TestExtractFile_PublicAfterPrivate(t *testing.T) {
	src := `class Toggle
  private

  def hidden
  end

  public

  def visible
  end
end
`
	result := extractFromRubyString(t, src, "lib/toggle.rb", false, true)
	hidden := assertFact(t, result, "Toggle#hidden")
	assertBoolProp(t, hidden, "exported", false)
	visible := assertFact(t, result, "Toggle#visible")
	assertBoolProp(t, visible, "exported", true)
}

func TestExtractFile_ModuleFunction(t *testing.T) {
	src := `module Utils
  module_function

  def helper
  end
end
`
	result := extractFromRubyString(t, src, "lib/utils.rb", false, true)
	f := assertFact(t, result, "Utils.helper")
	assertSymbolKind(t, f, facts.SymbolFunc)
}

func TestExtractFile_VisibilityResetsOnNestedScope(t *testing.T) {
	src := `class Outer
  private

  def outer_private
  end

  class Inner
    def inner_public
    end
  end
end
`
	result := extractFromRubyString(t, src, "lib/outer.rb", false, true)
	outer := assertFact(t, result, "Outer#outer_private")
	assertBoolProp(t, outer, "exported", false)
	inner := assertFact(t, result, "Outer::Inner#inner_public")
	assertBoolProp(t, inner, "exported", true)
}

// --- Mixin tests ---

func TestExtractFile_Include(t *testing.T) {
	src := `class User
  include Serializable
end
`
	result := extractFromRubyString(t, src, "app/models/user.rb", false, true)
	deps := findFactsByKind(result, facts.KindDependency)
	found := false
	for _, d := range deps {
		if mk, _ := d.Props["mixin_kind"].(string); mk == "include" {
			if hasRelation(d, facts.RelImplements, "Serializable") {
				found = true
			}
		}
	}
	if !found {
		t.Error("missing include Serializable dependency")
	}
}

func TestExtractFile_Extend(t *testing.T) {
	src := `class Document
  extend ClassMethods
end
`
	result := extractFromRubyString(t, src, "app/models/doc.rb", false, true)
	deps := findFactsByKind(result, facts.KindDependency)
	found := false
	for _, d := range deps {
		if mk, _ := d.Props["mixin_kind"].(string); mk == "extend" {
			if hasRelation(d, facts.RelImplements, "ClassMethods") {
				found = true
			}
		}
	}
	if !found {
		t.Error("missing extend ClassMethods dependency")
	}
}

func TestExtractFile_Prepend(t *testing.T) {
	src := `class Logger
  prepend Buffering
end
`
	result := extractFromRubyString(t, src, "lib/logger.rb", false, true)
	deps := findFactsByKind(result, facts.KindDependency)
	found := false
	for _, d := range deps {
		if mk, _ := d.Props["mixin_kind"].(string); mk == "prepend" {
			if hasRelation(d, facts.RelImplements, "Buffering") {
				found = true
			}
		}
	}
	if !found {
		t.Error("missing prepend Buffering dependency")
	}
}

func TestExtractFile_IncludeScopeResolution(t *testing.T) {
	src := `class Worker
  include Sidekiq::Worker
end
`
	result := extractFromRubyString(t, src, "app/workers/worker.rb", false, true)
	deps := findFactsByKind(result, facts.KindDependency)
	found := false
	for _, d := range deps {
		if hasRelation(d, facts.RelImplements, "Sidekiq::Worker") {
			found = true
		}
	}
	if !found {
		t.Error("missing include Sidekiq::Worker dependency")
	}
}

// --- Import tests ---

func TestExtractFile_Require(t *testing.T) {
	src := `require "json"
require "net/http"
`
	result := extractFromRubyString(t, src, "lib/client.rb", false, true)
	deps := findFactsByKind(result, facts.KindDependency)
	if len(deps) < 2 {
		t.Fatalf("expected at least 2 dependencies, got %d", len(deps))
	}
	// Check json import
	foundJSON := false
	for _, d := range deps {
		if hasRelation(d, facts.RelImports, "json") {
			foundJSON = true
		}
	}
	if !foundJSON {
		t.Error("missing require json")
	}
}

func TestExtractFile_RequireRelative(t *testing.T) {
	src := `require_relative "../helpers/formatter"
`
	result := extractFromRubyString(t, src, "lib/service.rb", false, true)
	deps := findFactsByKind(result, facts.KindDependency)
	if len(deps) == 0 {
		t.Fatal("expected dependency fact")
	}
	d := deps[0]
	if !hasRelation(d, facts.RelImports, "../helpers/formatter") {
		t.Errorf("missing imports relation; relations = %v", d.Relations)
	}
	rr, _ := d.Props["require_relative"].(bool)
	if !rr {
		t.Error("expected require_relative=true")
	}
}

// --- ActiveSupport::Concern tests ---

func TestExtractFile_ActiveSupportConcern(t *testing.T) {
	src := `module Searchable
  extend ActiveSupport::Concern

  def search(query)
  end
end
`
	result := extractFromRubyString(t, src, "app/models/concerns/searchable.rb", false, true)
	// extend ActiveSupport::Concern should NOT create a mixin dependency
	for _, f := range result {
		if f.Kind == facts.KindDependency {
			if mk, _ := f.Props["mixin_kind"].(string); mk == "extend" {
				if hasRelation(f, facts.RelImplements, "ActiveSupport::Concern") {
					t.Error("ActiveSupport::Concern should be detected as concern, not emitted as mixin")
				}
			}
		}
	}
	// The module should NOT have concern=true because isConcern is consumed by the next module
	// Actually, looking at the code: extend ASC sets w.isConcern = true, and the NEXT module
	// declaration consumes it. But since Searchable is already declared before extend,
	// the concern flag may not be set on Searchable itself.
	// The key assertion: no mixin dependency for ActiveSupport::Concern
}

// --- ActiveRecord association tests ---

func TestExtractFile_HasMany(t *testing.T) {
	dir := t.TempDir()
	src := `class Post < ApplicationRecord
  has_many :comments
end
`
	path := filepath.Join(dir, "post.rb")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	relFile := "app/models/post.rb"
	result := extractAssociationsFromFile(path, relFile)
	if len(result) == 0 {
		t.Fatal("expected association facts")
	}
	f := result[0]
	ak, _ := f.Props["association_kind"].(string)
	if ak != "has_many" {
		t.Errorf("association_kind = %q, want has_many", ak)
	}
	// has_many :comments -> singularize -> comment -> CamelCase -> Comment
	if !hasRelation(f, facts.RelDependsOn, "Comment") {
		t.Errorf("expected depends_on Comment; relations = %v", f.Relations)
	}
}

func TestExtractFile_BelongsTo(t *testing.T) {
	dir := t.TempDir()
	src := `class Comment < ApplicationRecord
  belongs_to :post
end
`
	path := filepath.Join(dir, "comment.rb")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	result := extractAssociationsFromFile(path, "app/models/comment.rb")
	if len(result) == 0 {
		t.Fatal("expected association facts")
	}
	f := result[0]
	ak, _ := f.Props["association_kind"].(string)
	if ak != "belongs_to" {
		t.Errorf("association_kind = %q, want belongs_to", ak)
	}
	// belongs_to :post -> Post (no singularize)
	if !hasRelation(f, facts.RelDependsOn, "Post") {
		t.Errorf("expected depends_on Post; relations = %v", f.Relations)
	}
}

func TestExtractFile_HasOne(t *testing.T) {
	dir := t.TempDir()
	src := `class User < ApplicationRecord
  has_one :profile
end
`
	path := filepath.Join(dir, "user.rb")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	result := extractAssociationsFromFile(path, "app/models/user.rb")
	if len(result) == 0 {
		t.Fatal("expected association facts")
	}
	f := result[0]
	ak, _ := f.Props["association_kind"].(string)
	if ak != "has_one" {
		t.Errorf("association_kind = %q, want has_one", ak)
	}
	// has_one :profile -> Profile (no singularize)
	if !hasRelation(f, facts.RelDependsOn, "Profile") {
		t.Errorf("expected depends_on Profile; relations = %v", f.Relations)
	}
}

func TestExtractFile_HasAndBelongsToMany(t *testing.T) {
	dir := t.TempDir()
	src := `class Student < ApplicationRecord
  has_and_belongs_to_many :courses
end
`
	path := filepath.Join(dir, "student.rb")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	result := extractAssociationsFromFile(path, "app/models/student.rb")
	if len(result) == 0 {
		t.Fatal("expected association facts")
	}
	f := result[0]
	ak, _ := f.Props["association_kind"].(string)
	if ak != "has_and_belongs_to_many" {
		t.Errorf("association_kind = %q, want has_and_belongs_to_many", ak)
	}
	// has_and_belongs_to_many :courses -> singularize -> course -> Course
	if !hasRelation(f, facts.RelDependsOn, "Course") {
		t.Errorf("expected depends_on Course; relations = %v", f.Relations)
	}
}

func TestExtractFile_Scope(t *testing.T) {
	dir := t.TempDir()
	src := `class Article < ApplicationRecord
  scope :published, -> { where(published: true) }
  scope :recent, -> { order(created_at: :desc) }
end
`
	path := filepath.Join(dir, "article.rb")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	result := extractAssociationsFromFile(path, "app/models/article.rb")
	pubScope := false
	recScope := false
	for _, f := range result {
		if f.Name == "scope:published" {
			pubScope = true
			assertSymbolKind(t, f, facts.SymbolFunc)
			sc, _ := f.Props["scope"].(bool)
			if !sc {
				t.Error("expected scope=true")
			}
		}
		if f.Name == "scope:recent" {
			recScope = true
		}
	}
	if !pubScope {
		t.Error("missing scope:published")
	}
	if !recScope {
		t.Error("missing scope:recent")
	}
}

// --- Edge case tests ---

func TestExtractFile_EmptyFile(t *testing.T) {
	result := extractFromRubyString(t, "", "lib/empty.rb", false, true)
	if len(result) != 0 {
		t.Errorf("expected no facts for empty file, got %d", len(result))
	}
}

func TestExtractFile_CommentsOnly(t *testing.T) {
	src := `# This is a comment
# Another comment
# frozen_string_literal: true
`
	result := extractFromRubyString(t, src, "lib/comments.rb", false, true)
	if len(result) != 0 {
		t.Errorf("expected no facts for comments-only file, got %d", len(result))
	}
}

func TestExtractFile_DeeplyNested(t *testing.T) {
	src := `module A
  module B
    module C
      class D
        def deep_method
        end
      end
    end
  end
end
`
	result := extractFromRubyString(t, src, "lib/deep.rb", false, true)
	assertFact(t, result, "A")
	assertFact(t, result, "A::B")
	assertFact(t, result, "A::B::C")
	assertFact(t, result, "A::B::C::D")
	f := assertFact(t, result, "A::B::C::D#deep_method")
	assertSymbolKind(t, f, facts.SymbolMethod)
}

func TestExtractFile_ReopenedClass(t *testing.T) {
	src := `class String
  def blank?
  end
end
`
	result := extractFromRubyString(t, src, "lib/ext/string.rb", false, true)
	assertFact(t, result, "String")
	assertFact(t, result, "String#blank?")
}

func TestExtractFile_TopLevelMethod(t *testing.T) {
	src := `def setup_database
end
`
	result := extractFromRubyString(t, src, "db/setup.rb", false, true)
	// Top-level method uses dir.methodname format but is still SymbolMethod
	// (only singleton/eigenclass/module_function methods are SymbolFunc)
	f := assertFact(t, result, "db.setup_database")
	assertSymbolKind(t, f, facts.SymbolMethod)
}

func TestExtractFile_MultipleClassesInFile(t *testing.T) {
	src := `class Error < StandardError
end

class NotFoundError < Error
end

class ValidationError < Error
end
`
	result := extractFromRubyString(t, src, "lib/errors.rb", false, true)
	assertFact(t, result, "Error")
	nf := assertFact(t, result, "NotFoundError")
	assertProp(t, nf, "superclass", "Error")
	ve := assertFact(t, result, "ValidationError")
	assertProp(t, ve, "superclass", "Error")
}

func TestExtractFile_RailsFrameworkProp(t *testing.T) {
	src := `class User < ApplicationRecord
  def name
  end
end
`
	result := extractFromRubyString(t, src, "app/models/user.rb", true, false)
	f := assertFact(t, result, "User")
	assertProp(t, f, "framework", "rails")
	m := assertFact(t, result, "User#name")
	assertProp(t, m, "framework", "rails")
}

func TestExtractFile_NonRailsNoFrameworkProp(t *testing.T) {
	src := `class Lib
end
`
	result := extractFromRubyString(t, src, "lib/lib.rb", false, true)
	f := assertFact(t, result, "Lib")
	if _, ok := f.Props["framework"]; ok {
		t.Error("non-rails should not have framework prop")
	}
}

// --- File and line tracking tests ---

func TestExtractFile_LineNumbers(t *testing.T) {
	src := `module Api
  class Controller
    def index
    end
  end
end
`
	result := extractFromRubyString(t, src, "app/controllers/api.rb", false, true)
	mod := assertFact(t, result, "Api")
	if mod.Line != 1 {
		t.Errorf("Api line = %d, want 1", mod.Line)
	}
	cls := assertFact(t, result, "Api::Controller")
	if cls.Line != 2 {
		t.Errorf("Controller line = %d, want 2", cls.Line)
	}
	meth := assertFact(t, result, "Api::Controller#index")
	if meth.Line != 3 {
		t.Errorf("index line = %d, want 3", meth.Line)
	}
}

func TestExtractFile_FilePathStored(t *testing.T) {
	src := `class Foo
end
`
	relFile := "app/models/foo.rb"
	result := extractFromRubyString(t, src, relFile, false, true)
	f := assertFact(t, result, "Foo")
	if f.File != relFile {
		t.Errorf("file = %q, want %q", f.File, relFile)
	}
}

// --- Export flag tests ---

func TestExtractFile_ExportedByPackwerk(t *testing.T) {
	src := `class Public
  def api_method
  end
end
`
	result := extractFromRubyString(t, src, "app/models/public.rb", false, true)
	f := assertFact(t, result, "Public")
	assertBoolProp(t, f, "exported", true)
	m := assertFact(t, result, "Public#api_method")
	assertBoolProp(t, m, "exported", true)
}

func TestExtractFile_NotExportedByPackwerk(t *testing.T) {
	src := `class Internal
  def hidden_method
  end
end
`
	result := extractFromRubyString(t, src, "app/models/internal.rb", false, false)
	f := assertFact(t, result, "Internal")
	assertBoolProp(t, f, "exported", false)
	m := assertFact(t, result, "Internal#hidden_method")
	assertBoolProp(t, m, "exported", false)
}

func TestExtractFile_PrivateMethodNotExportedEvenWithPackwerk(t *testing.T) {
	src := `class Svc
  private

  def secret
  end
end
`
	result := extractFromRubyString(t, src, "app/models/svc.rb", false, true)
	f := assertFact(t, result, "Svc#secret")
	assertBoolProp(t, f, "exported", false)
}

// --- Storage fact tests ---

func TestExtractFile_StorageFactForARModel(t *testing.T) {
	relFile := "app/models/user.rb"
	fileFacts := []facts.Fact{
		{
			Kind: facts.KindSymbol,
			Name: "User",
			File: relFile,
			Props: map[string]any{
				"symbol_kind": facts.SymbolClass,
				"superclass":  "ApplicationRecord",
			},
		},
	}
	result := extractStorageFacts(relFile, fileFacts)
	if len(result) == 0 {
		t.Fatal("expected storage fact")
	}
	sf := result[0]
	if sf.Kind != facts.KindStorage {
		t.Errorf("kind = %q, want storage", sf.Kind)
	}
	if sf.Name != "User" {
		t.Errorf("name = %q, want User", sf.Name)
	}
	table, _ := sf.Props["table"].(string)
	if table != "users" {
		t.Errorf("table = %q, want users", table)
	}
}

func TestExtractFile_StorageFactActiveRecordBase(t *testing.T) {
	relFile := "app/models/legacy.rb"
	fileFacts := []facts.Fact{
		{
			Kind: facts.KindSymbol,
			Name: "Legacy",
			File: relFile,
			Props: map[string]any{
				"symbol_kind": facts.SymbolClass,
				"superclass":  "ActiveRecord::Base",
			},
		},
	}
	result := extractStorageFacts(relFile, fileFacts)
	if len(result) == 0 {
		t.Fatal("expected storage fact for ActiveRecord::Base")
	}
}

func TestExtractFile_StorageFactModelSuffix(t *testing.T) {
	relFile := "app/models/item.rb"
	fileFacts := []facts.Fact{
		{
			Kind: facts.KindSymbol,
			Name: "Item",
			File: relFile,
			Props: map[string]any{
				"symbol_kind": facts.SymbolClass,
				"superclass":  "ItemsModel",
			},
		},
	}
	result := extractStorageFacts(relFile, fileFacts)
	if len(result) == 0 {
		t.Fatal("expected storage fact for Model-suffix superclass")
	}
}

func TestExtractFile_NoStorageFactForNonAR(t *testing.T) {
	relFile := "lib/service.rb"
	fileFacts := []facts.Fact{
		{
			Kind: facts.KindSymbol,
			Name: "Service",
			File: relFile,
			Props: map[string]any{
				"symbol_kind": facts.SymbolClass,
				"superclass":  "BaseService",
			},
		},
	}
	result := extractStorageFacts(relFile, fileFacts)
	if len(result) != 0 {
		t.Errorf("expected no storage facts for non-AR class, got %d", len(result))
	}
}

// --- Helper function unit tests ---

func TestIsAllCaps(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"MAX_RETRIES", true},
		{"VERSION", true},
		{"API_V2", true},
		{"AB", true},
		{"A", false},           // too short
		{"", false},            // empty
		{"MyClass", false},     // mixed case
		{"foo", false},         // lowercase
		{"HELLO_world", false}, // mixed
	}
	for _, tc := range cases {
		got := isAllCaps(tc.input)
		if got != tc.want {
			t.Errorf("isAllCaps(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestIsRubyFile(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"app/models/user.rb", true},
		{"lib/Foo.RB", true}, // case insensitive
		{"config/routes.rb", true},
		{"Gemfile", false},
		{"app.py", false},
		{"lib/test.rbs", false},
	}
	for _, tc := range cases {
		got := isRubyFile(tc.input)
		if got != tc.want {
			t.Errorf("isRubyFile(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestIsARBaseClass(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"ApplicationRecord", true},
		{"ActiveRecord::Base", true},
		{"ItemsModel", true}, // Model suffix convention
		{"ShippingModel", true},
		{"BaseService", false},
		{"StandardError", false},
		{"", false},
	}
	for _, tc := range cases {
		got := isARBaseClass(tc.input)
		if got != tc.want {
			t.Errorf("isARBaseClass(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestInferTableName(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"User", "users"},
		{"Item", "items"},
		{"UserAddress", "user_addresses"},
		{"Api::V2::Item", "items"}, // takes last segment
		{"Category", "categories"}, // y -> ies
		{"Address", "addresses"},   // ss -> sses
	}
	for _, tc := range cases {
		got := inferTableName(tc.input)
		if got != tc.want {
			t.Errorf("inferTableName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestPluralize(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"user", "users"},
		{"address", "addresses"},   // ss ending
		{"wish", "wishes"},         // sh ending
		{"match", "matches"},       // ch ending
		{"box", "boxes"},           // x ending
		{"buzz", "buzzes"},         // z ending
		{"category", "categories"}, // consonant+y
		{"day", "days"},            // vowel+y
		{"items", "items"},         // already plural
		{"", ""},                   // empty
	}
	for _, tc := range cases {
		got := pluralize(tc.input)
		if got != tc.want {
			t.Errorf("pluralize(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSingularize(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"users", "user"},
		{"categories", "category"}, // ies -> y
		{"addresses", "address"},   // sses -> ss
		{"wishes", "wish"},         // shes -> sh
		{"matches", "match"},       // ches -> ch
		{"boxes", "box"},           // xes -> x
		{"buzzes", "buzz"},         // zes -> z
		{"class", "class"},         // ss ending stays
		{"item", "item"},           // no s ending
	}
	for _, tc := range cases {
		got := singularize(tc.input)
		if got != tc.want {
			t.Errorf("singularize(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSnakeToCamel(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"user", "User"},
		{"line_item", "LineItem"},
		{"order_item_detail", "OrderItemDetail"},
	}
	for _, tc := range cases {
		got := snakeToCamel(tc.input)
		if got != tc.want {
			t.Errorf("snakeToCamel(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestCamelToSnake(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"User", "user"},
		{"UserAddress", "user_address"},
		{"APIKey", "a_p_i_key"},
	}
	for _, tc := range cases {
		got := camelToSnake(tc.input)
		if got != tc.want {
			t.Errorf("camelToSnake(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// --- Detect tests ---

func TestDetect_Gemfile(t *testing.T) {
	dir := t.TempDir()
	ext := New()

	// No Gemfile -> false
	detected, err := ext.Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if detected {
		t.Error("Detect() = true for empty dir, want false")
	}

	// With Gemfile -> true
	if err := os.WriteFile(filepath.Join(dir, "Gemfile"), []byte("source \"https://rubygems.org\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	detected, err = ext.Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !detected {
		t.Error("Detect() = false with Gemfile, want true")
	}
}

func TestName(t *testing.T) {
	ext := New()
	if ext.Name() != "ruby" {
		t.Errorf("Name() = %q, want ruby", ext.Name())
	}
}

// --- OpenAPI spec path ---

func TestExtractFile_OpenapiSpecPath(t *testing.T) {
	src := `class UsersController < ApplicationController
  openapi_spec_path "doc/openapi/v1/users.yaml"
end
`
	result := extractFromRubyString(t, src, "app/controllers/api/v1/users_controller.rb", false, true)
	deps := findFactsByKind(result, facts.KindDependency)
	found := false
	for _, d := range deps {
		if hasRelation(d, facts.RelDependsOn, "doc/openapi/v1/users.yaml") {
			found = true
			specFile, _ := d.Props["spec_file"].(string)
			if specFile != "doc/openapi/v1/users.yaml" {
				t.Errorf("spec_file = %q, want doc/openapi/v1/users.yaml", specFile)
			}
		}
	}
	if !found {
		t.Error("missing openapi_spec_path dependency")
	}
}

// --- Explicit table name ---

func TestExtractFile_ExplicitTableName(t *testing.T) {
	dir := t.TempDir()
	src := `class User < ApplicationRecord
  self.table_name = 'legacy_users'
end
`
	path := filepath.Join(dir, "user.rb")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	result := extractAssociationsFromFile(path, "app/models/user.rb")
	found := false
	for _, f := range result {
		if f.Kind == facts.KindStorage && f.Name == "legacy_users" {
			found = true
			sk, _ := f.Props["storage_kind"].(string)
			if sk != "table" {
				t.Errorf("storage_kind = %q, want table", sk)
			}
			explicit, _ := f.Props["explicit"].(bool)
			if !explicit {
				t.Error("expected explicit=true")
			}
		}
	}
	if !found {
		t.Error("missing explicit table name storage fact")
	}
}

func TestExtractFile_ModuleFunctionResetsOnNestedScope(t *testing.T) {
	src := `module Outer
  module_function

  def outer_func
  end

  module Inner
    def inner_method
    end
  end
end
`
	result := extractFromRubyString(t, src, "lib/outer.rb", false, true)
	outer := assertFact(t, result, "Outer.outer_func")
	assertSymbolKind(t, outer, facts.SymbolFunc)
	// Inner module resets module_function
	inner := assertFact(t, result, "Outer::Inner#inner_method")
	assertSymbolKind(t, inner, facts.SymbolMethod)
}

func TestExtractFile_MultipleAttrsInOneCall(t *testing.T) {
	src := `class Config
  attr_accessor :host, :port, :timeout
end
`
	result := extractFromRubyString(t, src, "lib/config.rb", false, true)
	assertFact(t, result, "Config#host")
	assertFact(t, result, "Config#port")
	assertFact(t, result, "Config#timeout")
}

func TestExtractFile_TopLevelConstant(t *testing.T) {
	src := `MAX_CONNECTIONS = 100
`
	result := extractFromRubyString(t, src, "config/limits.rb", false, true)
	f := assertFact(t, result, "config.MAX_CONNECTIONS")
	assertSymbolKind(t, f, facts.SymbolConstant)
}
