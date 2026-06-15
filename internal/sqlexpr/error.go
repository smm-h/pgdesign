package sqlexpr

import "fmt"

// ParseError is a structured error with position information from the parser.
type ParseError struct {
	Pos   int    // byte offset in input
	Token string // the problematic token
	Msg   string // human-readable message
}

func (e *ParseError) Error() string {
	if e.Token != "" {
		return fmt.Sprintf("sqlexpr: %s (token %q at position %d)", e.Msg, e.Token, e.Pos)
	}
	return fmt.Sprintf("sqlexpr: %s (at position %d)", e.Msg, e.Pos)
}
