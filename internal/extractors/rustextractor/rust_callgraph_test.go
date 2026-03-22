package rustextractor

import (
	"testing"

	"github.com/dejo1307/archmcp/internal/facts"
)

// --- Acceptance Test: Enhanced Call Graph Coverage ---
// Tests for closure calls, turbofish, async/await chains, and selective macro calls.

func TestAcceptance_EnhancedCallGraph(t *testing.T) {
	ff := extractFromString(t, `fn process(x: i32) -> i32 { x * 2 }
fn handle(item: &i32) {}

fn example() {
    // Closure calls: calls inside closure bodies
    let items = vec![1, 2, 3];
    items.iter().map(|x| process(*x));
    items.iter().for_each(|item| {
        handle(item);
    });

    // Turbofish: generic_function node
    let collected: Vec<i32> = items.iter().copied().collect::<Vec<_>>();
    let parsed: i32 = "42".parse::<i32>().unwrap();

    // User-defined macro calls
    my_macro!(arg);
}
`)

	example, ok := findFact(ff, "src.example")
	if !ok {
		t.Fatal("expected fact for src.example")
	}

	// 1. Closure call: process() called inside closure body
	if !hasRelation(example, facts.RelCalls, "src.process") {
		t.Error("example missing calls relation for src.process (closure body call)")
	}

	// 2. Closure call: handle() called inside closure body
	if !hasRelation(example, facts.RelCalls, "src.handle") {
		t.Error("example missing calls relation for src.handle (closure body call)")
	}

	// 3. Turbofish: the collect call itself
	hasCollect := hasCallContaining(example, "collect")
	if !hasCollect {
		t.Error("example missing calls for collect (turbofish generic_function)")
	}

	// 4. Turbofish: parse::<i32>() should be extracted
	hasParse := hasCallContaining(example, "parse")
	if !hasParse {
		t.Error("example missing calls for parse (turbofish generic_function)")
	}

	// 5. User-defined macro: my_macro! should appear as a call
	hasMacro := hasCallContaining(example, "my_macro")
	if !hasMacro {
		t.Error("example missing calls for my_macro (user-defined macro)")
	}

	// 6. Standard library macros should NOT appear as calls
	if hasCallContaining(example, "vec") {
		t.Error("example should NOT have calls for vec! (std macro)")
	}

	// 7. Std macros println!, eprintln!, format!, etc. should NOT appear
	if hasCallContaining(example, "println") {
		t.Error("example should NOT have calls for println! (std macro)")
	}
}

// TestAcceptance_AsyncAwaitChain tests async/await chain call extraction.
func TestAcceptance_AsyncAwaitChain(t *testing.T) {
	ff := extractFromString(t, `async fn fetch() -> String { String::new() }
async fn parse(s: String) -> i32 { 42 }

async fn pipeline() {
    let data = fetch().await;
    let result = parse(data).await;
    let chained = fetch().await.len();
}
`)

	pipeline, ok := findFact(ff, "src.pipeline")
	if !ok {
		t.Fatal("expected fact for src.pipeline")
	}

	// 1. fetch() called through .await
	if !hasRelation(pipeline, facts.RelCalls, "src.fetch") {
		t.Error("pipeline missing calls for src.fetch (through await)")
	}

	// 2. parse() called through .await
	if !hasRelation(pipeline, facts.RelCalls, "src.parse") {
		t.Error("pipeline missing calls for src.parse (through await)")
	}

	// 3. chained: fetch().await.len() — should capture len
	if !hasCallContaining(pipeline, "len") {
		t.Error("pipeline missing calls for len (chained after await)")
	}
}

// --- Unit Tests: extractCallTarget for new node types ---

// TestExtractCallTarget_GenericFunction tests turbofish call extraction.
func TestExtractCallTarget_GenericFunction(t *testing.T) {
	ff := extractFromString(t, `fn turbofish_test() {
    let v: Vec<i32> = (0..10).collect::<Vec<_>>();
    let n: i32 = "42".parse::<i32>().unwrap();
    HashMap::<String, i32>::new();
}
`)
	f, ok := findFact(ff, "src.turbofish_test")
	if !ok {
		t.Fatal("expected fact for src.turbofish_test")
	}

	// collect::<Vec<_>>() should be extracted
	if !hasCallContaining(f, "collect") {
		t.Error("turbofish_test missing collect call (generic_function)")
	}
	// parse::<i32>() should be extracted
	if !hasCallContaining(f, "parse") {
		t.Error("turbofish_test missing parse call (generic_function)")
	}
	// HashMap::<String, i32>::new() — qualified turbofish
	if !hasCallContaining(f, "new") {
		t.Error("turbofish_test missing HashMap::new call (scoped turbofish)")
	}
}

// TestExtractCallTarget_MacroInvocation tests user-defined macro extraction.
func TestExtractCallTarget_MacroInvocation(t *testing.T) {
	ff := extractFromString(t, `fn macro_test() {
    // User-defined macros should be captured
    my_macro!(arg1, arg2);
    sqlx::query!("SELECT * FROM users");
    
    // Standard library macros should be EXCLUDED
    println!("hello");
    eprintln!("error");
    format!("value: {}", 42);
    vec![1, 2, 3];
    todo!("not yet");
    unimplemented!();
    unreachable!();
    panic!("oh no");
    assert!(true);
    assert_eq!(1, 1);
    assert_ne!(1, 2);
    debug_assert!(true);
    write!(f, "hello");
    writeln!(f, "hello");
    cfg!(test);
    env!("PATH");
    include!("file.rs");
    concat!("a", "b");
    stringify!(expr);
    line!();
    column!();
    file!();
    module_path!();
}
`)
	f, ok := findFact(ff, "src.macro_test")
	if !ok {
		t.Fatal("expected fact for src.macro_test")
	}

	// User-defined macro: my_macro! should be captured
	if !hasCallContaining(f, "my_macro") {
		t.Error("macro_test missing my_macro call")
	}
	// User-defined macro: sqlx::query! should be captured
	if !hasCallContaining(f, "query") {
		t.Error("macro_test missing sqlx.query call")
	}

	// Standard macros should be EXCLUDED
	stdMacros := []string{
		"println", "eprintln", "format", "vec", "todo",
		"unimplemented", "unreachable", "panic",
		"assert", "assert_eq", "assert_ne", "debug_assert",
		"write", "writeln", "cfg", "env", "include",
		"concat", "stringify", "line", "column", "file", "module_path",
	}
	for _, m := range stdMacros {
		if hasCallContaining(f, m) {
			t.Errorf("macro_test should NOT have call for std macro %s!", m)
		}
	}
}

// TestExtractCallTarget_ClosureInMethodChain tests calls inside closures.
func TestExtractCallTarget_ClosureInMethodChain(t *testing.T) {
	ff := extractFromString(t, `fn transform(x: i32) -> i32 { x * 2 }
fn validate(x: &i32) -> bool { *x > 0 }

fn closure_test() {
    let data = vec![1, 2, 3];
    let result: Vec<i32> = data
        .iter()
        .filter(|x| validate(x))
        .map(|x| transform(*x))
        .collect();
}
`)
	f, ok := findFact(ff, "src.closure_test")
	if !ok {
		t.Fatal("expected fact for src.closure_test")
	}

	// Calls inside closure bodies should be captured
	if !hasRelation(f, facts.RelCalls, "src.transform") {
		t.Error("closure_test missing transform call (inside map closure)")
	}
	if !hasRelation(f, facts.RelCalls, "src.validate") {
		t.Error("closure_test missing validate call (inside filter closure)")
	}
}

// TestExtractCallTarget_AwaitExpression tests async/await call extraction.
func TestExtractCallTarget_AwaitExpression(t *testing.T) {
	ff := extractFromString(t, `async fn fetch_data() -> String { String::new() }
async fn process_data(s: &str) -> i32 { 42 }

async fn complex_async() {
    // Simple await
    let data = fetch_data().await;
    
    // Method chain after await
    let len = fetch_data().await.len();
    
    // Nested awaits
    let result = process_data(&fetch_data().await).await;
}
`)
	f, ok := findFact(ff, "src.complex_async")
	if !ok {
		t.Fatal("expected fact for src.complex_async")
	}

	if !hasRelation(f, facts.RelCalls, "src.fetch_data") {
		t.Error("complex_async missing fetch_data call")
	}
	if !hasRelation(f, facts.RelCalls, "src.process_data") {
		t.Error("complex_async missing process_data call")
	}
}

// --- Helper ---

// hasCallContaining checks if any calls relation target contains the substring.
func hasCallContaining(f facts.Fact, substr string) bool {
	for _, r := range f.Relations {
		if r.Kind == facts.RelCalls {
			for i := 0; i <= len(r.Target)-len(substr); i++ {
				if r.Target[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}
