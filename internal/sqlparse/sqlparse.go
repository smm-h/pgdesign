package sqlparse

import (
	"strings"

	pg_query "github.com/wasilibs/go-pgquery"
)

// SplitStatements splits a SQL string into individual statements using
// pg_query's parser. This correctly handles dollar-quoted strings,
// string literals containing semicolons, and other PostgreSQL syntax.
func SplitStatements(sql string) ([]string, error) {
	trimmed := strings.TrimSpace(sql)
	if trimmed == "" {
		return nil, nil
	}

	result, err := pg_query.Parse(trimmed)
	if err != nil {
		return nil, err
	}

	var stmts []string
	for i, raw := range result.Stmts {
		loc := int(raw.StmtLocation)
		length := int(raw.StmtLen)
		if length == 0 && i == len(result.Stmts)-1 {
			// Last statement: StmtLen == 0 means rest of string.
			length = len(trimmed) - loc
		}
		text := strings.TrimSpace(trimmed[loc : loc+length])
		if text == "" {
			continue
		}
		if !strings.HasSuffix(text, ";") {
			text += ";"
		}
		stmts = append(stmts, text)
	}
	return stmts, nil
}
