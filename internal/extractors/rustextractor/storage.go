package rustextractor

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dejo1307/archmcp/internal/facts"
)

// SQL patterns — shared with Go extractor approach.
var (
	reCreateTable = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?` + "`?" + `"?'?(\w+)`)
	reInsertInto  = regexp.MustCompile(`(?i)INSERT\s+INTO\s+` + "`?" + `"?'?(\w+)`)
	reUpdate      = regexp.MustCompile(`(?i)UPDATE\s+` + "`?" + `"?'?(\w+)\s+SET\b`)
	reDeleteFrom  = regexp.MustCompile(`(?i)DELETE\s+FROM\s+` + "`?" + `"?'?(\w+)`)
	reSelectFrom  = regexp.MustCompile(`(?i)\bFROM\s+` + "`?" + `"?'?(\w+)`)
	reAlterTable  = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+` + "`?" + `"?'?(\w+)`)
)

// Rust-specific patterns.
var (
	// diesel table! { ... } — matches "table! {" on one line, then "tablename (" on next
	reDieselTableBlock = regexp.MustCompile(`table!\s*\{`)
	reDieselTableName  = regexp.MustCompile(`^\s+(\w+)\s*\(`)

	// sqlx::query!("...") or sqlx::query_as!(Type, "...")
	reSqlxQuery   = regexp.MustCompile(`sqlx::query!\s*\(\s*"([^"]+)"`)
	reSqlxQueryAs = regexp.MustCompile(`sqlx::query_as!\s*\(\s*\w+\s*,\s*"([^"]+)"`)

	// #[derive(...Queryable...)] — indicates READ ORM model
	reDeriveQueryable = regexp.MustCompile(`#\[derive\([^)]*\bQueryable\b[^)]*\)\]`)

	// #[derive(...Insertable...)] — indicates WRITE ORM model
	reDeriveInsertable = regexp.MustCompile(`#\[derive\([^)]*\bInsertable\b[^)]*\)\]`)

	// #[diesel(table_name = <name>)]
	reDieselTableNameAttr = regexp.MustCompile(`#\[diesel\(\s*table_name\s*=\s*(\w+)\s*\)\]`)

	// #[derive(...DeriveEntityModel...)] — sea-orm
	reDeriveEntityModel = regexp.MustCompile(`#\[derive\([^)]*\bDeriveEntityModel\b[^)]*\)\]`)

	// #[sea_orm(table_name = "name")]
	reSeaOrmTableName = regexp.MustCompile(`#\[sea_orm\(\s*table_name\s*=\s*"(\w+)"\s*\)\]`)

	// struct Name after derive
	reStructName = regexp.MustCompile(`(?:pub\s+)?struct\s+(\w+)`)

	// String literal content extractor
	reStringLiteral = regexp.MustCompile(`"([^"]*)"`)
)

// ExtractStorage scans Rust source files for storage/database patterns
// (diesel, sqlx, sea-orm, raw SQL) and returns Storage facts.
func ExtractStorage(files map[string][]byte) []facts.Fact {
	var result []facts.Fact

	for relFile, content := range files {
		if !isRustFile(relFile) {
			continue
		}
		ff := extractStorageFromFile(relFile, content)
		result = append(result, ff...)
	}

	return result
}

// extractStorageFromFile scans a single Rust file for storage patterns.
func extractStorageFromFile(relFile string, content []byte) []facts.Fact {
	var result []facts.Fact
	dir := filepath.Dir(relFile)
	lines := strings.Split(string(content), "\n")

	// Dedup (table, operation) per file
	type tableOp struct{ table, op string }
	seen := make(map[tableOp]bool)

	emit := func(name, storageKind, operation string, line int) {
		key := tableOp{name, operation}
		if seen[key] {
			return
		}
		seen[key] = true
		result = append(result, facts.Fact{
			Kind: facts.KindStorage,
			Name: name,
			File: relFile,
			Line: line,
			Props: map[string]any{
				"storage_kind": storageKind,
				"operation":    operation,
				"language":     "rust",
			},
			Relations: []facts.Relation{
				{Kind: facts.RelDeclares, Target: dir},
			},
		})
	}

	// State for multi-line pattern detection
	inDieselTableBlock := false
	pendingQueryable := false
	pendingInsertable := false
	pendingEntityModel := false
	pendingDieselTableName := ""
	pendingSeaOrmTableName := ""

	for i, line := range lines {
		lineNum := i + 1

		// --- diesel table! macro (multi-line) ---
		if reDieselTableBlock.MatchString(line) {
			inDieselTableBlock = true
			continue
		}
		if inDieselTableBlock {
			if m := reDieselTableName.FindStringSubmatch(line); m != nil {
				tableName := m[1]
				emit(tableName, "table", "CREATE", lineNum)
				inDieselTableBlock = false
			}
			// If we see closing brace at top level, reset
			if strings.TrimSpace(line) == "}" {
				inDieselTableBlock = false
			}
			continue
		}

		// --- diesel #[derive(Queryable)] ---
		if reDeriveQueryable.MatchString(line) {
			pendingQueryable = true
		}

		// --- diesel #[derive(Insertable)] ---
		if reDeriveInsertable.MatchString(line) {
			pendingInsertable = true
		}

		// --- #[diesel(table_name = X)] ---
		if m := reDieselTableNameAttr.FindStringSubmatch(line); m != nil {
			pendingDieselTableName = m[1]
		}

		// --- sea-orm #[derive(DeriveEntityModel)] ---
		if reDeriveEntityModel.MatchString(line) {
			pendingEntityModel = true
		}

		// --- #[sea_orm(table_name = "X")] ---
		if m := reSeaOrmTableName.FindStringSubmatch(line); m != nil {
			pendingSeaOrmTableName = m[1]
		}

		// --- struct after pending ORM derives ---
		if m := reStructName.FindStringSubmatch(line); m != nil {
			structName := m[1]

			if pendingInsertable && pendingDieselTableName != "" {
				// Insertable with explicit table_name → use table name, WRITE
				emit(pendingDieselTableName, "orm_model", "WRITE", lineNum)
			} else if pendingInsertable {
				// Insertable without table_name → use struct name, WRITE
				emit(structName, "orm_model", "WRITE", lineNum)
			}

			if pendingQueryable {
				emit(structName, "orm_model", "READ", lineNum)
			}

			if pendingEntityModel && pendingSeaOrmTableName != "" {
				emit(pendingSeaOrmTableName, "orm_model", "READ", lineNum)
			} else if pendingEntityModel {
				emit(structName, "orm_model", "READ", lineNum)
			}

			// Reset pending state
			pendingQueryable = false
			pendingInsertable = false
			pendingEntityModel = false
			pendingDieselTableName = ""
			pendingSeaOrmTableName = ""
		}

		// --- sqlx::query! and sqlx::query_as! ---
		for _, re := range []*regexp.Regexp{reSqlxQuery, reSqlxQueryAs} {
			if m := re.FindStringSubmatch(line); m != nil {
				sql := m[1]
				extractSQLOps(sql, lineNum, emit)
			}
		}

		// --- Raw SQL strings in quotes ---
		// Look for string literals containing SQL keywords
		extractRawSQL(line, lineNum, emit)
	}

	return result
}

// extractSQLOps extracts table references from a SQL string.
func extractSQLOps(sql string, lineNum int, emit func(string, string, string, int)) {
	sqlPatterns := []struct {
		re        *regexp.Regexp
		operation string
		kind      string
	}{
		{reCreateTable, "CREATE", "table"},
		{reAlterTable, "ALTER", "table"},
		{reInsertInto, "INSERT", "table_reference"},
		{reUpdate, "UPDATE", "table_reference"},
		{reDeleteFrom, "DELETE", "table_reference"},
		{reSelectFrom, "SELECT", "table_reference"},
	}

	for _, pat := range sqlPatterns {
		matches := pat.re.FindAllStringSubmatch(sql, -1)
		for _, m := range matches {
			tableName := m[1]
			if isSQLNoiseRust(tableName) {
				continue
			}
			emit(tableName, pat.kind, pat.operation, lineNum)
		}
	}
}

// extractRawSQL looks for SQL patterns in string literals on a line.
// It avoids re-matching sqlx macros by checking for those first.
func extractRawSQL(line string, lineNum int, emit func(string, string, string, int)) {
	// Skip lines already handled by sqlx patterns
	if strings.Contains(line, "sqlx::query") {
		return
	}

	// Extract string literal content from the line
	// Match both "..." and r"..." and r#"..."#
	strs := extractStringLiterals(line)
	for _, s := range strs {
		extractSQLOps(s, lineNum, emit)
	}
}

// extractStringLiterals extracts content of string literals from a Rust line.
func extractStringLiterals(line string) []string {
	var result []string
	for _, m := range reStringLiteral.FindAllStringSubmatch(line, -1) {
		result = append(result, m[1])
	}
	return result
}

// isSQLNoiseRust returns true for common SQL keywords that are not table names.
func isSQLNoiseRust(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "select", "from", "where", "set", "into", "values", "table",
		"index", "view", "trigger", "procedure", "function",
		"dual", "information_schema", "pg_catalog":
		return true
	}
	return false
}
