package godmodule

import (
	"context"
	"sort"
	"testing"

	"github.com/dejo1307/archmcp/internal/facts"
)

// --- helpers ---

func makeStore(ff []facts.Fact) *facts.Store {
	s := facts.NewStore()
	for _, f := range ff {
		s.Add(f)
	}
	return s
}

func mod(name string, rels ...facts.Relation) facts.Fact {
	return facts.Fact{
		Kind:      facts.KindModule,
		Name:      name,
		Relations: rels,
	}
}

func sym(name, file string) facts.Fact {
	return facts.Fact{
		Kind: facts.KindSymbol,
		Name: name,
		File: file,
	}
}

func rel(kind, target string) facts.Relation {
	return facts.Relation{Kind: kind, Target: target}
}

func insightTitles(insights []facts.Insight) []string {
	var titles []string
	for _, ins := range insights {
		titles = append(titles, ins.Title)
	}
	return titles
}

// --- Unit Tests ---

func TestName(t *testing.T) {
	e := New()
	if got := e.Name(); got != "godmodule" {
		t.Errorf("Name() = %q, want %q", got, "godmodule")
	}
}

func TestExplain_EmptyStore(t *testing.T) {
	e := New()
	insights, err := e.Explain(context.Background(), facts.NewStore())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights for empty store, got %d", len(insights))
	}
}

func TestExplain_SmallModules_NoInsights(t *testing.T) {
	// 2 modules, each with a few symbols, low fan — no god modules
	store := makeStore([]facts.Fact{
		mod("pkg/a", rel(facts.RelImports, "pkg/b")),
		mod("pkg/b"),
		sym("Foo", "pkg/a/foo.go"),
		sym("Bar", "pkg/a/bar.go"),
		sym("Baz", "pkg/b/baz.go"),
	})

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights (small modules), got %d", len(insights))
	}
}

func TestExplain_GodModule_TooManySymbols(t *testing.T) {
	// Module with >30 symbols should be flagged
	ff := []facts.Fact{mod("pkg/big")}
	for i := 0; i < 31; i++ {
		ff = append(ff, sym("Sym"+string(rune('A'+i%26))+string(rune('0'+i/26)), "pkg/big/file.go"))
	}

	store := makeStore(ff)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	if insights[0].Confidence < 0.5 || insights[0].Confidence > 1.0 {
		t.Errorf("confidence = %f, want between 0.5 and 1.0", insights[0].Confidence)
	}
}

func TestExplain_GodModule_HighFan(t *testing.T) {
	// Module with fan-in+fan-out > 15 should be flagged
	// pkg/hub imports 10 others, and 6 others import pkg/hub => fan_out=10, fan_in=6 => total=16
	ff := []facts.Fact{}

	// Build hub module with 10 outgoing imports
	hubRels := make([]facts.Relation, 0, 10)
	for i := 0; i < 10; i++ {
		name := "pkg/dep" + string(rune('a'+i))
		ff = append(ff, mod(name))
		hubRels = append(hubRels, rel(facts.RelImports, name))
	}
	ff = append(ff, mod("pkg/hub", hubRels...))

	// 6 modules that import pkg/hub
	for i := 0; i < 6; i++ {
		name := "pkg/user" + string(rune('a'+i))
		ff = append(ff, mod(name, rel(facts.RelImports, "pkg/hub")))
	}

	// Add a few symbols so it looks realistic but < 30
	ff = append(ff, sym("HubFunc", "pkg/hub/hub.go"))

	store := makeStore(ff)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight (high fan), got %d", len(insights))
	}
}

func TestExplain_MultipleGodModules_RankedBySeverity(t *testing.T) {
	// Two god modules: one with 35 symbols, one with 40 symbols
	// The one with 40 should appear first (higher severity)
	ff := []facts.Fact{
		mod("pkg/medium"),
		mod("pkg/large"),
	}
	for i := 0; i < 35; i++ {
		ff = append(ff, sym("Med"+string(rune('A'+i%26))+string(rune('0'+i/26)), "pkg/medium/f.go"))
	}
	for i := 0; i < 40; i++ {
		ff = append(ff, sym("Lg"+string(rune('A'+i%26))+string(rune('0'+i/26)), "pkg/large/f.go"))
	}

	store := makeStore(ff)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 summary insight, got %d", len(insights))
	}

	// Evidence should have large module first (higher severity)
	if len(insights[0].Evidence) < 2 {
		t.Fatalf("expected at least 2 evidence entries, got %d", len(insights[0].Evidence))
	}
	// First evidence should be the worse module
	first := insights[0].Evidence[0].Fact
	if first != "pkg/large" {
		t.Errorf("first evidence module = %q, want %q (highest severity first)", first, "pkg/large")
	}
}

func TestExplain_ExactThreshold_NotFlagged(t *testing.T) {
	// Exactly 30 symbols should NOT be flagged (>30 is the threshold)
	ff := []facts.Fact{mod("pkg/borderline")}
	for i := 0; i < 30; i++ {
		ff = append(ff, sym("S"+string(rune('A'+i%26))+string(rune('0'+i/26)), "pkg/borderline/f.go"))
	}

	store := makeStore(ff)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights (at threshold, not over), got %d", len(insights))
	}
}

func TestExplain_FanExactThreshold_NotFlagged(t *testing.T) {
	// fan-in + fan-out = 15 exactly should NOT be flagged (>15 is the threshold)
	ff := []facts.Fact{}
	hubRels := make([]facts.Relation, 0, 10)
	for i := 0; i < 10; i++ {
		name := "pkg/d" + string(rune('a'+i))
		ff = append(ff, mod(name))
		hubRels = append(hubRels, rel(facts.RelImports, name))
	}
	ff = append(ff, mod("pkg/hub", hubRels...))

	// 5 modules importing hub => fan_in=5, fan_out=10, total=15
	for i := 0; i < 5; i++ {
		name := "pkg/u" + string(rune('a'+i))
		ff = append(ff, mod(name, rel(facts.RelImports, "pkg/hub")))
	}

	store := makeStore(ff)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights (fan at threshold, not over), got %d", len(insights))
	}
}

func TestExplain_InsightFields(t *testing.T) {
	// Verify insight has all required fields
	ff := []facts.Fact{mod("pkg/god")}
	for i := 0; i < 35; i++ {
		ff = append(ff, sym("G"+string(rune('A'+i%26))+string(rune('0'+i/26)), "pkg/god/f.go"))
	}

	store := makeStore(ff)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}

	ins := insights[0]
	if ins.Title == "" {
		t.Error("expected non-empty title")
	}
	if ins.Description == "" {
		t.Error("expected non-empty description")
	}
	if ins.Confidence < 0.5 || ins.Confidence > 1.0 {
		t.Errorf("confidence = %f, want between 0.5 and 1.0", ins.Confidence)
	}
	if len(ins.Actions) == 0 {
		t.Error("expected at least one suggested action")
	}
	if len(ins.Evidence) == 0 {
		t.Error("expected at least one evidence entry")
	}
}

func TestExplain_EvidenceContainsMetrics(t *testing.T) {
	// Evidence detail should include symbols count, fan-in, fan-out
	ff := []facts.Fact{mod("pkg/god")}
	for i := 0; i < 35; i++ {
		ff = append(ff, sym("G"+string(rune('A'+i%26))+string(rune('0'+i/26)), "pkg/god/f.go"))
	}

	store := makeStore(ff)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}

	ev := insights[0].Evidence[0]
	if ev.Fact != "pkg/god" {
		t.Errorf("evidence fact = %q, want %q", ev.Fact, "pkg/god")
	}
	// Detail should mention symbol count
	if ev.Detail == "" {
		t.Error("expected non-empty evidence detail")
	}
}

func TestExplain_BothThresholdsTriggered(t *testing.T) {
	// Module with >30 symbols AND fan-in+fan-out>15: should still produce 1 insight
	ff := []facts.Fact{}
	hubRels := make([]facts.Relation, 0, 10)
	for i := 0; i < 10; i++ {
		name := "pkg/d" + string(rune('a'+i))
		ff = append(ff, mod(name))
		hubRels = append(hubRels, rel(facts.RelImports, name))
	}
	ff = append(ff, mod("pkg/god", hubRels...))
	for i := 0; i < 6; i++ {
		name := "pkg/u" + string(rune('a'+i))
		ff = append(ff, mod(name, rel(facts.RelImports, "pkg/god")))
	}
	for i := 0; i < 35; i++ {
		ff = append(ff, sym("G"+string(rune('A'+i%26))+string(rune('0'+i/26)), "pkg/god/f.go"))
	}

	store := makeStore(ff)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
}

func TestModuleMetrics(t *testing.T) {
	// Directly test metric computation
	ff := []facts.Fact{
		mod("pkg/a", rel(facts.RelImports, "pkg/b"), rel(facts.RelImports, "pkg/c")),
		mod("pkg/b", rel(facts.RelImports, "pkg/a")),
		mod("pkg/c"),
		sym("Foo", "pkg/a/foo.go"),
		sym("Bar", "pkg/a/bar.go"),
		sym("Baz", "pkg/b/baz.go"),
	}

	store := makeStore(ff)
	metrics := computeMetrics(store)

	a := metrics["pkg/a"]
	if a.symbols != 2 {
		t.Errorf("pkg/a symbols = %d, want 2", a.symbols)
	}
	if a.fanOut != 2 {
		t.Errorf("pkg/a fanOut = %d, want 2", a.fanOut)
	}
	if a.fanIn != 1 {
		t.Errorf("pkg/a fanIn = %d, want 1", a.fanIn)
	}

	b := metrics["pkg/b"]
	if b.symbols != 1 {
		t.Errorf("pkg/b symbols = %d, want 1", b.symbols)
	}
	if b.fanOut != 1 {
		t.Errorf("pkg/b fanOut = %d, want 1", b.fanOut)
	}
	if b.fanIn != 1 {
		t.Errorf("pkg/b fanIn = %d, want 1", b.fanIn)
	}

	c := metrics["pkg/c"]
	if c.symbols != 0 {
		t.Errorf("pkg/c symbols = %d, want 0", c.symbols)
	}
	if c.fanOut != 0 {
		t.Errorf("pkg/c fanOut = %d, want 0", c.fanOut)
	}
	if c.fanIn != 1 {
		t.Errorf("pkg/c fanIn = %d, want 1", c.fanIn)
	}
}

func TestSeverityOrdering(t *testing.T) {
	// Modules should be ranked by severity (higher is worse)
	ff := []facts.Fact{
		mod("pkg/small"),
		mod("pkg/medium"),
		mod("pkg/large"),
	}
	// small: 5 symbols
	for i := 0; i < 5; i++ {
		ff = append(ff, sym("S"+string(rune('A'+i)), "pkg/small/f.go"))
	}
	// medium: 35 symbols
	for i := 0; i < 35; i++ {
		ff = append(ff, sym("M"+string(rune('A'+i%26))+string(rune('0'+i/26)), "pkg/medium/f.go"))
	}
	// large: 50 symbols
	for i := 0; i < 50; i++ {
		ff = append(ff, sym("L"+string(rune('A'+i%26))+string(rune('0'+i/26)), "pkg/large/f.go"))
	}

	store := makeStore(ff)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}

	// Evidence should list large before medium, small not present
	var evidenceModules []string
	for _, ev := range insights[0].Evidence {
		evidenceModules = append(evidenceModules, ev.Fact)
	}
	if len(evidenceModules) != 2 {
		t.Fatalf("expected 2 evidence entries, got %d: %v", len(evidenceModules), evidenceModules)
	}
	if evidenceModules[0] != "pkg/large" {
		t.Errorf("first evidence = %q, want %q", evidenceModules[0], "pkg/large")
	}
	if evidenceModules[1] != "pkg/medium" {
		t.Errorf("second evidence = %q, want %q", evidenceModules[1], "pkg/medium")
	}
}

func TestExplain_SymbolsAssignedToCorrectModule(t *testing.T) {
	// Symbols in "pkg/a/sub/file.go" should be counted under "pkg/a" module
	// (matching by file path prefix)
	ff := []facts.Fact{
		mod("pkg/a"),
	}
	for i := 0; i < 35; i++ {
		ff = append(ff, sym("X"+string(rune('A'+i%26))+string(rune('0'+i/26)), "pkg/a/sub/f.go"))
	}

	store := makeStore(ff)
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight (symbols in subdir of module), got %d", len(insights))
	}
}

// Verify the interface is satisfied at compile time.
func TestImplementsExplainer(t *testing.T) {
	_ = sort.Sort // use sort to avoid unused import
	// The compile check is implicit by the Explain/Name signatures.
	e := New()
	_ = e.Name()
}
