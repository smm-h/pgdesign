package sqlparse

import (
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"
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

// DeparseExpr converts a pg_query AST expression node back to its SQL string
// representation. It works by wrapping the node in a synthetic SELECT statement
// and deparsing the full statement, then stripping the "SELECT " prefix.
func DeparseExpr(node *pg.Node) (string, error) {
	synthetic := &pg.ParseResult{
		Stmts: []*pg.RawStmt{{
			Stmt: &pg.Node{
				Node: &pg.Node_SelectStmt{
					SelectStmt: &pg.SelectStmt{
						TargetList: []*pg.Node{{
							Node: &pg.Node_ResTarget{
								ResTarget: &pg.ResTarget{Val: node},
							},
						}},
					},
				},
			},
		}},
	}
	sql, err := pg_query.Deparse(synthetic)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(sql, "SELECT "), nil
}
