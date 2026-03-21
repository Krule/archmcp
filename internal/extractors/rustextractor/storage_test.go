package rustextractor

import (
	"testing"

	"github.com/dejo1307/archmcp/internal/facts"
)

// --- Acceptance test (OUTER LOOP) ---
// Exercises ExtractStorage against a realistic Rust project with diesel, sqlx, sea-orm, and raw SQL.

func TestAcceptance_ExtractStorage_AllPatterns(t *testing.T) {
	files := map[string][]byte{
		// diesel: table! macro
		"src/schema.rs": []byte(`
table! {
    users (id) {
        id -> Integer,
        name -> Text,
        email -> Text,
    }
}

table! {
    posts (id) {
        id -> Integer,
        title -> Text,
        user_id -> Integer,
    }
}
`),
		// diesel: #[derive(Queryable)]
		"src/models.rs": []byte(`
use diesel::Queryable;

#[derive(Queryable)]
pub struct User {
    pub id: i32,
    pub name: String,
}

#[derive(Insertable)]
#[diesel(table_name = orders)]
pub struct NewOrder {
    pub user_id: i32,
    pub total: f64,
}
`),
		// sqlx: query! and query_as! macros
		"src/db/queries.rs": []byte(`
use sqlx;

pub async fn get_users(pool: &PgPool) -> Vec<User> {
    sqlx::query!("SELECT id, name FROM accounts WHERE active = true")
        .fetch_all(pool)
        .await
        .unwrap()
}

pub async fn get_order(pool: &PgPool, id: i32) -> Order {
    sqlx::query_as!(Order, "SELECT * FROM orders WHERE id = $1", id)
        .fetch_one(pool)
        .await
        .unwrap()
}

pub async fn insert_item(pool: &PgPool) {
    sqlx::query!("INSERT INTO items (name, price) VALUES ($1, $2)", name, price)
        .execute(pool)
        .await
        .unwrap()
}
`),
		// sea-orm: DeriveEntityModel
		"src/entity/customer.rs": []byte(`
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, DeriveEntityModel)]
#[sea_orm(table_name = "customers")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub id: i32,
    pub name: String,
}
`),
		// Raw SQL strings (like Go SQL detection)
		"src/migrations.rs": []byte(`
pub fn run_migrations() {
    let create = "CREATE TABLE IF NOT EXISTS sessions (id TEXT PRIMARY KEY, data BLOB)";
    let alter = "ALTER TABLE sessions ADD COLUMN expires_at INTEGER";
    let update = "UPDATE sessions SET data = ? WHERE id = ?";
    let delete = "DELETE FROM sessions WHERE expires_at < ?";
}
`),
		// No storage patterns — should produce no facts
		"src/utils.rs": []byte(`
pub fn add(a: i32, b: i32) -> i32 {
    a + b
}
`),
	}

	storageFacts := ExtractStorage(files)

	// --- diesel table! macro ---
	assertStorageFact(t, storageFacts, "users", "table", "CREATE", "src/schema.rs")
	assertStorageFact(t, storageFacts, "posts", "table", "CREATE", "src/schema.rs")

	// --- diesel #[derive(Queryable)] ---
	assertStorageFact(t, storageFacts, "User", "orm_model", "READ", "src/models.rs")

	// --- diesel #[derive(Insertable)] with table_name ---
	assertStorageFact(t, storageFacts, "orders", "orm_model", "WRITE", "src/models.rs")

	// --- sqlx query! macros ---
	assertStorageFact(t, storageFacts, "accounts", "table_reference", "SELECT", "src/db/queries.rs")
	assertStorageFact(t, storageFacts, "orders", "table_reference", "SELECT", "src/db/queries.rs")
	assertStorageFact(t, storageFacts, "items", "table_reference", "INSERT", "src/db/queries.rs")

	// --- sea-orm DeriveEntityModel ---
	assertStorageFact(t, storageFacts, "customers", "orm_model", "READ", "src/entity/customer.rs")

	// --- Raw SQL ---
	assertStorageFact(t, storageFacts, "sessions", "table", "CREATE", "src/migrations.rs")
	assertStorageFact(t, storageFacts, "sessions", "table_reference", "UPDATE", "src/migrations.rs")
	assertStorageFact(t, storageFacts, "sessions", "table_reference", "DELETE", "src/migrations.rs")

	// --- No false positives from utils.rs ---
	for _, f := range storageFacts {
		if f.File == "src/utils.rs" {
			t.Errorf("unexpected storage fact from utils.rs: %+v", f)
		}
	}

	// --- All facts have language: "rust" ---
	for _, f := range storageFacts {
		if f.Props["language"] != "rust" {
			t.Errorf("fact %q has language %v, want rust", f.Name, f.Props["language"])
		}
	}

	// --- All facts are KindStorage ---
	for _, f := range storageFacts {
		if f.Kind != facts.KindStorage {
			t.Errorf("fact %q has kind %v, want storage", f.Name, f.Kind)
		}
	}
}

// --- Test helpers ---

func assertStorageFact(t *testing.T, ff []facts.Fact, name, storageKind, operation, file string) {
	t.Helper()
	for _, f := range ff {
		if f.Name == name &&
			f.Props["storage_kind"] == storageKind &&
			f.Props["operation"] == operation &&
			f.File == file {
			return
		}
	}
	t.Errorf("expected storage fact: name=%q storage_kind=%q operation=%q file=%q\ngot facts: %s",
		name, storageKind, operation, file, formatFacts(ff))
}

func formatFacts(ff []facts.Fact) string {
	if len(ff) == 0 {
		return "(none)"
	}
	var s string
	for _, f := range ff {
		s += "\n  " + f.Name + " [" + f.File + "] kind=" + str(f.Props["storage_kind"]) + " op=" + str(f.Props["operation"])
	}
	return s
}

func str(v any) string {
	if v == nil {
		return "<nil>"
	}
	return v.(string)
}

// --- Unit tests (INNER LOOP) ---

func TestExtractStorage_RawSQL_CreateTable(t *testing.T) {
	files := map[string][]byte{
		"src/db.rs": []byte(`
let q = "CREATE TABLE IF NOT EXISTS users (id INT PRIMARY KEY, name TEXT)";
let q2 = "CREATE TABLE orders (id INT, total REAL)";
`),
	}
	ff := ExtractStorage(files)
	assertStorageFact(t, ff, "users", "table", "CREATE", "src/db.rs")
	assertStorageFact(t, ff, "orders", "table", "CREATE", "src/db.rs")
}

func TestExtractStorage_RawSQL_SelectInsertUpdateDelete(t *testing.T) {
	files := map[string][]byte{
		"src/repo.rs": []byte(`
let s = "SELECT id, name FROM accounts WHERE active = 1";
let i = "INSERT INTO items (name) VALUES ($1)";
let u = "UPDATE items SET name = $1 WHERE id = $2";
let d = "DELETE FROM items WHERE id = $1";
`),
	}
	ff := ExtractStorage(files)
	assertStorageFact(t, ff, "accounts", "table_reference", "SELECT", "src/repo.rs")
	assertStorageFact(t, ff, "items", "table_reference", "INSERT", "src/repo.rs")
	assertStorageFact(t, ff, "items", "table_reference", "UPDATE", "src/repo.rs")
	assertStorageFact(t, ff, "items", "table_reference", "DELETE", "src/repo.rs")
}

func TestExtractStorage_RawSQL_AlterTable(t *testing.T) {
	files := map[string][]byte{
		"src/migrate.rs": []byte(`
let q = "ALTER TABLE users ADD COLUMN phone TEXT";
`),
	}
	ff := ExtractStorage(files)
	assertStorageFact(t, ff, "users", "table", "ALTER", "src/migrate.rs")
}

func TestExtractStorage_RawSQL_Dedup(t *testing.T) {
	files := map[string][]byte{
		"src/repo.rs": []byte(`
let q1 = "SELECT id FROM users WHERE active = 1";
let q2 = "SELECT name FROM users WHERE id = ?";
`),
	}
	ff := ExtractStorage(files)
	selectCount := 0
	for _, f := range ff {
		if f.Name == "users" && f.Props["operation"] == "SELECT" {
			selectCount++
		}
	}
	if selectCount != 1 {
		t.Errorf("expected 1 deduplicated SELECT for users, got %d", selectCount)
	}
}

func TestExtractStorage_RawSQL_NoiseFiltered(t *testing.T) {
	files := map[string][]byte{
		"src/query.rs": []byte(`
let q = "SELECT * FROM dual";
`),
	}
	ff := ExtractStorage(files)
	for _, f := range ff {
		if f.Name == "dual" {
			t.Errorf("SQL noise word 'dual' should be filtered, got: %+v", f)
		}
	}
}

func TestExtractStorage_DieselTableMacro(t *testing.T) {
	files := map[string][]byte{
		"src/schema.rs": []byte(`
table! {
    products (id) {
        id -> Integer,
        name -> Text,
    }
}
`),
	}
	ff := ExtractStorage(files)
	assertStorageFact(t, ff, "products", "table", "CREATE", "src/schema.rs")
}

func TestExtractStorage_DieselQueryable(t *testing.T) {
	files := map[string][]byte{
		"src/models.rs": []byte(`
#[derive(Queryable)]
pub struct Product {
    pub id: i32,
    pub name: String,
}
`),
	}
	ff := ExtractStorage(files)
	assertStorageFact(t, ff, "Product", "orm_model", "READ", "src/models.rs")
}

func TestExtractStorage_DieselInsertable(t *testing.T) {
	files := map[string][]byte{
		"src/models.rs": []byte(`
#[derive(Insertable)]
#[diesel(table_name = products)]
pub struct NewProduct {
    pub name: String,
}
`),
	}
	ff := ExtractStorage(files)
	assertStorageFact(t, ff, "products", "orm_model", "WRITE", "src/models.rs")
}

func TestExtractStorage_SqlxQueryMacro(t *testing.T) {
	files := map[string][]byte{
		"src/db.rs": []byte(`
sqlx::query!("SELECT id, name FROM widgets WHERE active = true")
    .fetch_all(pool)
    .await?;
`),
	}
	ff := ExtractStorage(files)
	assertStorageFact(t, ff, "widgets", "table_reference", "SELECT", "src/db.rs")
}

func TestExtractStorage_SqlxQueryAsMacro(t *testing.T) {
	files := map[string][]byte{
		"src/db.rs": []byte(`
sqlx::query_as!(Widget, "SELECT * FROM gadgets WHERE id = $1", id)
    .fetch_one(pool)
    .await?;
`),
	}
	ff := ExtractStorage(files)
	assertStorageFact(t, ff, "gadgets", "table_reference", "SELECT", "src/db.rs")
}

func TestExtractStorage_SeaOrmDeriveEntityModel(t *testing.T) {
	files := map[string][]byte{
		"src/entity/product.rs": []byte(`
#[derive(Clone, Debug, DeriveEntityModel)]
#[sea_orm(table_name = "products")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub id: i32,
    pub name: String,
}
`),
	}
	ff := ExtractStorage(files)
	assertStorageFact(t, ff, "products", "orm_model", "READ", "src/entity/product.rs")
}

func TestExtractStorage_NonRsFileIgnored(t *testing.T) {
	files := map[string][]byte{
		"src/main.go": []byte(`package main
const q = "SELECT * FROM users"
`),
		"README.md": []byte(`# SELECT FROM users`),
	}
	ff := ExtractStorage(files)
	if len(ff) != 0 {
		t.Errorf("expected 0 facts for non-.rs files, got %d", len(ff))
	}
}

func TestExtractStorage_NoStorage(t *testing.T) {
	files := map[string][]byte{
		"src/lib.rs": []byte(`
pub fn hello() -> String {
    "hello".to_string()
}
`),
	}
	ff := ExtractStorage(files)
	if len(ff) != 0 {
		t.Errorf("expected 0 storage facts, got %d", len(ff))
	}
}

func TestExtractStorage_RelationsPointToDir(t *testing.T) {
	files := map[string][]byte{
		"src/db/repo.rs": []byte(`
let q = "SELECT * FROM users";
`),
	}
	ff := ExtractStorage(files)
	if len(ff) == 0 {
		t.Fatal("expected at least one storage fact")
	}
	f := ff[0]
	if len(f.Relations) == 0 {
		t.Fatal("expected at least one relation")
	}
	if f.Relations[0].Kind != facts.RelDeclares {
		t.Errorf("relation kind = %q, want %q", f.Relations[0].Kind, facts.RelDeclares)
	}
	if f.Relations[0].Target != "src/db" {
		t.Errorf("relation target = %q, want %q", f.Relations[0].Target, "src/db")
	}
}
