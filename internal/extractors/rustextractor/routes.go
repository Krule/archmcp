package rustextractor

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dejo1307/archmcp/internal/facts"
)

// Route patterns for Rust web frameworks.
var (
	// axum: .route("/path", get(handler))
	// Captures: path, method_fn, handler
	axumRouteRe = regexp.MustCompile(
		`\.route\(\s*"([^"]+)"\s*,\s*(get|post|put|delete|patch|head|options)\s*\(\s*(\w+)\s*\)`)

	// axum method_router chaining: .route("/path", get(h1).post(h2))
	// After the initial .route("path", method(handler)), capture chained .method(handler)
	axumMethodChainRe = regexp.MustCompile(
		`\.(get|post|put|delete|patch|head|options)\s*\(\s*(\w+)\s*\)`)

	// actix-web / rocket attribute macros: #[get("/path")], #[post("/path", ...)]
	// Captures: method, path
	attrMacroRouteRe = regexp.MustCompile(
		`#\[(get|post|put|delete|patch|head|options)\s*\(\s*"([^"]+)"`)

	// actix-web programmatic: web::resource("/path")
	// Captures: path
	actixResourceRe = regexp.MustCompile(
		`web::resource\s*\(\s*"([^"]+)"`)

	// actix-web programmatic route: .route(web::get().to(handler))
	// Captures: method, handler
	actixProgrammaticRouteRe = regexp.MustCompile(
		`\.route\s*\(\s*web::(get|post|put|delete|patch|head|options)\s*\(\s*\)\s*\.to\s*\(\s*(\w+)\s*\)`)

	// fn declaration (to find handler after attribute macros)
	fnDeclRe = regexp.MustCompile(
		`^\s*(?:pub\s+)?(?:async\s+)?fn\s+(\w+)`)
)

// framework detection: check for use/extern crate imports.
func detectRustFramework(content string) string {
	// Check for axum
	if strings.Contains(content, "use axum") || strings.Contains(content, "axum::") {
		return "axum"
	}
	// Check for actix-web
	if strings.Contains(content, "use actix_web") || strings.Contains(content, "actix_web::") {
		return "actix-web"
	}
	// Check for rocket - rocket uses attribute macros #[get], #[post] etc. without explicit import sometimes
	if strings.Contains(content, "use rocket") || strings.Contains(content, "rocket::") {
		return "rocket"
	}
	// Fallback: check for attribute macros that look like route definitions
	if attrMacroRouteRe.MatchString(content) {
		return "rocket" // default to rocket for bare attribute macros
	}
	return ""
}

// ExtractRoutes scans Rust source files for HTTP route definitions across
// axum, actix-web, and rocket frameworks. Returns route facts.
func ExtractRoutes(files map[string][]byte) []facts.Fact {
	var result []facts.Fact

	for relFile, content := range files {
		if !strings.HasSuffix(strings.ToLower(relFile), ".rs") {
			continue
		}

		src := string(content)
		framework := detectRustFramework(src)
		if framework == "" {
			continue
		}

		dir := filepath.Dir(relFile)
		lines := strings.Split(src, "\n")

		switch framework {
		case "axum":
			result = append(result, extractAxumRoutes(lines, relFile, dir)...)
		case "actix-web":
			result = append(result, extractActixRoutes(lines, relFile, dir)...)
		case "rocket":
			result = append(result, extractRocketRoutes(lines, relFile, dir)...)
		}
	}

	return result
}

// extractAxumRoutes handles axum Router::new().route("/path", get(handler)) patterns.
func extractAxumRoutes(lines []string, relFile, dir string) []facts.Fact {
	var result []facts.Fact

	for lineNum, line := range lines {
		// Match .route("/path", method(handler))
		if m := axumRouteRe.FindStringSubmatch(line); m != nil {
			path := m[1]
			method := strings.ToUpper(m[2])
			handler := m[3]

			result = append(result, makeRouteFact(path, method, handler, "axum", relFile, dir, lineNum+1))

			// Check for method_router chaining: .route("/path", get(h1).post(h2))
			// Find everything after the initial match
			restOfLine := line[strings.Index(line, m[0])+len(m[0]):]
			chainMatches := axumMethodChainRe.FindAllStringSubmatch(restOfLine, -1)
			for _, cm := range chainMatches {
				chainMethod := strings.ToUpper(cm[1])
				chainHandler := cm[2]
				result = append(result, makeRouteFact(path, chainMethod, chainHandler, "axum", relFile, dir, lineNum+1))
			}
		}
	}

	return result
}

// extractActixRoutes handles actix-web attribute macros and programmatic routes.
func extractActixRoutes(lines []string, relFile, dir string) []facts.Fact {
	var result []facts.Fact

	// Track current resource path for programmatic routes
	var currentResourcePath string

	for lineNum, line := range lines {
		// Attribute macros: #[get("/path")]
		if m := attrMacroRouteRe.FindStringSubmatch(line); m != nil {
			method := strings.ToUpper(m[1])
			path := m[2]
			handler := lookAheadForHandler(lines, lineNum)
			result = append(result, makeRouteFact(path, method, handler, "actix-web", relFile, dir, lineNum+1))
			continue
		}

		// Programmatic: web::resource("/path")
		if m := actixResourceRe.FindStringSubmatch(line); m != nil {
			currentResourcePath = m[1]
		}

		// Programmatic: .route(web::get().to(handler))
		if currentResourcePath != "" {
			if m := actixProgrammaticRouteRe.FindStringSubmatch(line); m != nil {
				method := strings.ToUpper(m[1])
				handler := m[2]
				result = append(result, makeRouteFact(currentResourcePath, method, handler, "actix-web", relFile, dir, lineNum+1))
			}
		}

		// Reset resource path on closing patterns (heuristic: line with just );)
		trimmed := strings.TrimSpace(line)
		if trimmed == ");" || trimmed == "}" {
			if currentResourcePath != "" && !actixResourceRe.MatchString(line) && !actixProgrammaticRouteRe.MatchString(line) {
				currentResourcePath = ""
			}
		}
	}

	return result
}

// extractRocketRoutes handles rocket #[get("/path")], #[post("/path")] attribute macros.
func extractRocketRoutes(lines []string, relFile, dir string) []facts.Fact {
	var result []facts.Fact

	for lineNum, line := range lines {
		if m := attrMacroRouteRe.FindStringSubmatch(line); m != nil {
			method := strings.ToUpper(m[1])
			path := m[2]
			handler := lookAheadForHandler(lines, lineNum)
			result = append(result, makeRouteFact(path, method, handler, "rocket", relFile, dir, lineNum+1))
		}
	}

	return result
}

// lookAheadForHandler scans the next few lines after an attribute macro for a fn declaration.
func lookAheadForHandler(lines []string, lineNum int) string {
	for j := lineNum + 1; j < len(lines) && j <= lineNum+3; j++ {
		if fm := fnDeclRe.FindStringSubmatch(lines[j]); fm != nil {
			return fm[1]
		}
	}
	return ""
}

// makeRouteFact creates a facts.Fact for a route.
func makeRouteFact(path, method, handler, framework, relFile, dir string, line int) facts.Fact {
	props := map[string]any{
		"method":    method,
		"framework": framework,
		"language":  "rust",
	}
	if handler != "" {
		props["handler"] = handler
	}

	return facts.Fact{
		Kind:  facts.KindRoute,
		Name:  path,
		File:  relFile,
		Line:  line,
		Props: props,
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: dir},
		},
	}
}
