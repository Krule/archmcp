package rustextractor

import (
	"testing"

	"github.com/dejo1307/archmcp/internal/facts"
)

// --- helpers ---

func findRoutesByKind(ff []facts.Fact) []facts.Fact {
	var result []facts.Fact
	for _, f := range ff {
		if f.Kind == facts.KindRoute {
			result = append(result, f)
		}
	}
	return result
}

func findRoute(ff []facts.Fact, path, method string) (facts.Fact, bool) {
	for _, f := range ff {
		if f.Kind == facts.KindRoute && f.Name == path && f.Props["method"] == method {
			return f, true
		}
	}
	return facts.Fact{}, false
}

// === ACCEPTANCE TEST ===
// Tests all three frameworks in realistic Rust source files.

func TestAcceptance_ExtractRoutes_AllFrameworks(t *testing.T) {
	files := map[string][]byte{
		// Axum routes
		"src/api/routes.rs": []byte(`use axum::{Router, routing::{get, post, delete}};

pub fn router() -> Router {
    Router::new()
        .route("/api/users", get(list_users))
        .route("/api/users", post(create_user))
        .route("/api/users/:id", get(get_user))
        .route("/api/users/:id", delete(delete_user))
}

async fn list_users() -> impl IntoResponse {}
async fn create_user() -> impl IntoResponse {}
async fn get_user() -> impl IntoResponse {}
async fn delete_user() -> impl IntoResponse {}
`),
		// Actix-web attribute macros
		"src/handlers/user.rs": []byte(`use actix_web::{get, post, put, web, HttpResponse};

#[get("/users")]
async fn list_users() -> HttpResponse {
    HttpResponse::Ok().finish()
}

#[post("/users")]
async fn create_user() -> HttpResponse {
    HttpResponse::Ok().finish()
}

#[put("/users/{id}")]
async fn update_user() -> HttpResponse {
    HttpResponse::Ok().finish()
}
`),
		// Actix-web programmatic routes
		"src/handlers/config.rs": []byte(`use actix_web::web;

pub fn configure(cfg: &mut web::ServiceConfig) {
    cfg.service(
        web::resource("/config")
            .route(web::get().to(get_config))
            .route(web::post().to(update_config))
    );
}

async fn get_config() -> HttpResponse {}
async fn update_config() -> HttpResponse {}
`),
		// Rocket attribute macros
		"src/rocket_routes.rs": []byte(`use rocket::serde::json::Json;

#[get("/health")]
fn health_check() -> &'static str {
    "OK"
}

#[post("/items", data = "<item>")]
fn create_item(item: Json<Item>) -> Json<Item> {
    item
}

#[delete("/items/<id>")]
fn delete_item(id: u64) -> Status {
    Status::Ok
}
`),
		// Non-route Rust file: should produce no routes
		"src/models.rs": []byte(`pub struct User {
    pub id: u64,
    pub name: String,
}
`),
	}

	routes := ExtractRoutes(files)

	// --- Axum routes ---
	if r, ok := findRoute(routes, "/api/users", "GET"); !ok {
		t.Error("expected axum route GET /api/users")
	} else {
		if r.Props["handler"] != "list_users" {
			t.Errorf("axum GET /api/users handler = %v, want list_users", r.Props["handler"])
		}
		if r.Props["framework"] != "axum" {
			t.Errorf("axum GET /api/users framework = %v, want axum", r.Props["framework"])
		}
		if r.Props["language"] != "rust" {
			t.Errorf("axum GET /api/users language = %v, want rust", r.Props["language"])
		}
	}
	if _, ok := findRoute(routes, "/api/users", "POST"); !ok {
		t.Error("expected axum route POST /api/users")
	}
	if _, ok := findRoute(routes, "/api/users/:id", "GET"); !ok {
		t.Error("expected axum route GET /api/users/:id")
	}
	if _, ok := findRoute(routes, "/api/users/:id", "DELETE"); !ok {
		t.Error("expected axum route DELETE /api/users/:id")
	}

	// --- Actix-web attribute routes ---
	if r, ok := findRoute(routes, "/users", "GET"); !ok {
		t.Error("expected actix-web route GET /users")
	} else {
		if r.Props["handler"] != "list_users" {
			t.Errorf("actix GET /users handler = %v, want list_users", r.Props["handler"])
		}
		if r.Props["framework"] != "actix-web" {
			t.Errorf("actix GET /users framework = %v, want actix-web", r.Props["framework"])
		}
	}
	if _, ok := findRoute(routes, "/users", "POST"); !ok {
		t.Error("expected actix-web route POST /users")
	}
	if _, ok := findRoute(routes, "/users/{id}", "PUT"); !ok {
		t.Error("expected actix-web route PUT /users/{id}")
	}

	// --- Actix-web programmatic routes ---
	if _, ok := findRoute(routes, "/config", "GET"); !ok {
		t.Error("expected actix-web programmatic route GET /config")
	}
	if _, ok := findRoute(routes, "/config", "POST"); !ok {
		t.Error("expected actix-web programmatic route POST /config")
	}

	// --- Rocket routes ---
	if r, ok := findRoute(routes, "/health", "GET"); !ok {
		t.Error("expected rocket route GET /health")
	} else {
		if r.Props["handler"] != "health_check" {
			t.Errorf("rocket GET /health handler = %v, want health_check", r.Props["handler"])
		}
		if r.Props["framework"] != "rocket" {
			t.Errorf("rocket GET /health framework = %v, want rocket", r.Props["framework"])
		}
	}
	if _, ok := findRoute(routes, "/items", "POST"); !ok {
		t.Error("expected rocket route POST /items")
	}
	if _, ok := findRoute(routes, "/items/<id>", "DELETE"); !ok {
		t.Error("expected rocket route DELETE /items/<id>")
	}

	// --- Non-route file should produce no route facts ---
	for _, r := range routes {
		if r.File == "src/models.rs" {
			t.Errorf("expected no route facts from src/models.rs, got %v", r)
		}
	}

	// --- All routes should be KindRoute with proper structure ---
	for _, r := range routes {
		if r.Kind != facts.KindRoute {
			t.Errorf("route %v has kind %v, want route", r.Name, r.Kind)
		}
		if len(r.Relations) == 0 {
			t.Errorf("route %v has no relations", r.Name)
		}
		if r.File == "" {
			t.Errorf("route %v has empty file", r.Name)
		}
	}
}

// === UNIT TESTS ===

func TestExtractRoutes_Axum_BasicRoutes(t *testing.T) {
	files := map[string][]byte{
		"src/main.rs": []byte(`use axum::{Router, routing::get};

fn app() -> Router {
    Router::new()
        .route("/health", get(health))
        .route("/api/items", get(list_items))
}
`),
	}

	routes := ExtractRoutes(files)
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}

	if _, ok := findRoute(routes, "/health", "GET"); !ok {
		t.Error("expected GET /health")
	}
	if _, ok := findRoute(routes, "/api/items", "GET"); !ok {
		t.Error("expected GET /api/items")
	}
}

func TestExtractRoutes_Axum_AllMethods(t *testing.T) {
	files := map[string][]byte{
		"src/routes.rs": []byte(`use axum::{Router, routing::{get, post, put, delete, patch, head, options}};

fn app() -> Router {
    Router::new()
        .route("/r", get(h))
        .route("/r", post(h))
        .route("/r", put(h))
        .route("/r", delete(h))
        .route("/r", patch(h))
        .route("/r", head(h))
        .route("/r", options(h))
}
`),
	}

	routes := ExtractRoutes(files)
	methods := map[string]bool{}
	for _, r := range routes {
		methods[r.Props["method"].(string)] = true
	}
	for _, m := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"} {
		if !methods[m] {
			t.Errorf("missing method %s", m)
		}
	}
}

func TestExtractRoutes_Axum_MethodRouter(t *testing.T) {
	files := map[string][]byte{
		"src/routes.rs": []byte(`use axum::{Router, routing::{get, post}};

fn app() -> Router {
    Router::new()
        .route("/items", get(list).post(create))
}
`),
	}

	routes := ExtractRoutes(files)
	if _, ok := findRoute(routes, "/items", "GET"); !ok {
		t.Error("expected GET /items from method_router chaining")
	}
	if _, ok := findRoute(routes, "/items", "POST"); !ok {
		t.Error("expected POST /items from method_router chaining")
	}
}

func TestExtractRoutes_ActixWeb_AttributeMacros(t *testing.T) {
	files := map[string][]byte{
		"src/handlers.rs": []byte(`use actix_web::{get, post, delete, HttpResponse};

#[get("/items")]
async fn list_items() -> HttpResponse {
    HttpResponse::Ok().finish()
}

#[post("/items")]
async fn create_item() -> HttpResponse {
    HttpResponse::Ok().finish()
}

#[delete("/items/{id}")]
async fn delete_item() -> HttpResponse {
    HttpResponse::Ok().finish()
}
`),
	}

	routes := ExtractRoutes(files)
	if len(routes) != 3 {
		t.Fatalf("expected 3 routes, got %d", len(routes))
	}
	if r, ok := findRoute(routes, "/items", "GET"); !ok {
		t.Error("expected GET /items")
	} else if r.Props["handler"] != "list_items" {
		t.Errorf("handler = %v, want list_items", r.Props["handler"])
	}
}

func TestExtractRoutes_ActixWeb_Programmatic(t *testing.T) {
	files := map[string][]byte{
		"src/config.rs": []byte(`use actix_web::web;

pub fn configure(cfg: &mut web::ServiceConfig) {
    cfg.service(
        web::resource("/api/data")
            .route(web::get().to(get_data))
            .route(web::post().to(create_data))
    );
}
`),
	}

	routes := ExtractRoutes(files)
	if _, ok := findRoute(routes, "/api/data", "GET"); !ok {
		t.Error("expected GET /api/data")
	}
	if _, ok := findRoute(routes, "/api/data", "POST"); !ok {
		t.Error("expected POST /api/data")
	}
}

func TestExtractRoutes_Rocket_AttributeMacros(t *testing.T) {
	files := map[string][]byte{
		"src/api.rs": []byte(`#[get("/")]
fn index() -> &'static str {
    "Hello"
}

#[post("/submit", data = "<form>")]
fn submit(form: Form<MyForm>) -> Redirect {
    Redirect::to("/")
}
`),
	}

	routes := ExtractRoutes(files)
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
	if r, ok := findRoute(routes, "/", "GET"); !ok {
		t.Error("expected GET /")
	} else if r.Props["handler"] != "index" {
		t.Errorf("handler = %v, want index", r.Props["handler"])
	}
	if _, ok := findRoute(routes, "/submit", "POST"); !ok {
		t.Error("expected POST /submit")
	}
}

func TestExtractRoutes_NoRouteFiles(t *testing.T) {
	files := map[string][]byte{
		"src/lib.rs": []byte(`pub fn add(a: i32, b: i32) -> i32 {
    a + b
}
`),
	}

	routes := ExtractRoutes(files)
	if len(routes) != 0 {
		t.Errorf("expected 0 routes, got %d", len(routes))
	}
}

func TestExtractRoutes_NonRustFilesIgnored(t *testing.T) {
	files := map[string][]byte{
		"src/main.py": []byte(`from flask import Flask`),
		"README.md":   []byte(`# My project`),
	}

	routes := ExtractRoutes(files)
	if len(routes) != 0 {
		t.Errorf("expected 0 routes for non-.rs files, got %d", len(routes))
	}
}

func TestExtractRoutes_FactStructure(t *testing.T) {
	files := map[string][]byte{
		"src/routes.rs": []byte(`use axum::{Router, routing::get};

fn app() -> Router {
    Router::new().route("/test", get(handler))
}
`),
	}

	routes := ExtractRoutes(files)
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	r := routes[0]
	if r.Kind != facts.KindRoute {
		t.Errorf("Kind = %v, want route", r.Kind)
	}
	if r.Name != "/test" {
		t.Errorf("Name = %v, want /test", r.Name)
	}
	if r.File != "src/routes.rs" {
		t.Errorf("File = %v, want src/routes.rs", r.File)
	}
	if r.Line == 0 {
		t.Error("Line should not be 0")
	}
	if r.Props["method"] != "GET" {
		t.Errorf("method = %v, want GET", r.Props["method"])
	}
	if r.Props["handler"] != "handler" {
		t.Errorf("handler = %v, want handler", r.Props["handler"])
	}
	if r.Props["framework"] != "axum" {
		t.Errorf("framework = %v, want axum", r.Props["framework"])
	}
	if r.Props["language"] != "rust" {
		t.Errorf("language = %v, want rust", r.Props["language"])
	}
	if len(r.Relations) == 0 {
		t.Error("expected at least one relation")
	}
	if r.Relations[0].Kind != facts.RelDeclares {
		t.Errorf("relation kind = %v, want declares", r.Relations[0].Kind)
	}
}
