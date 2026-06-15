package sqlexpr

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse parses a SQL expression string into an AST.
func Parse(input string) (Node, error) {
	tokens, err := tokenize(input)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens, pos: 0}
	node, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.current().kind != tokenEOF {
		tok := p.current()
		return nil, &ParseError{Pos: tok.pos, Token: tok.value, Msg: "unexpected token"}
	}
	return node, nil
}

type parser struct {
	tokens []token
	pos    int
}

func (p *parser) current() token {
	if p.pos >= len(p.tokens) {
		return token{kind: tokenEOF, pos: -1}
	}
	return p.tokens[p.pos]
}

func (p *parser) advance() token {
	tok := p.current()
	p.pos++
	return tok
}

func (p *parser) expect(kind tokenKind) (token, error) {
	tok := p.current()
	if tok.kind != kind {
		return tok, &ParseError{Pos: tok.pos, Token: tok.value, Msg: fmt.Sprintf("expected token kind %d", kind)}
	}
	p.pos++
	return tok, nil
}

func (p *parser) isKeyword(kw string) bool {
	tok := p.current()
	return tok.kind == tokenIdent && strings.EqualFold(tok.value, kw)
}

func (p *parser) parseExpr() (Node, error) {
	return p.parseOr()
}

func (p *parser) parseOr() (Node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isKeyword("OR") {
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryOp{Op: "OR", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (Node, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.isKeyword("AND") {
		p.advance()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &BinaryOp{Op: "AND", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseNot() (Node, error) {
	if p.isKeyword("NOT") {
		p.advance()
		operand, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &UnaryOp{Op: "NOT", Operand: operand}, nil
	}
	return p.parseComparison()
}

func (p *parser) parseComparison() (Node, error) {
	left, err := p.parseConcat()
	if err != nil {
		return nil, err
	}

	tok := p.current()

	// Standard operator comparisons: =, !=, <>, <, >, <=, >=
	switch tok.kind {
	case tokenEquals, tokenNotEquals, tokenLess, tokenGreater, tokenLessEqual, tokenGreaterEqual:
		op := p.advance()
		right, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		return &BinaryOp{Op: op.value, Left: left, Right: right}, nil
	}

	// IS NULL / IS NOT NULL / IS DISTINCT FROM
	if p.isKeyword("IS") {
		p.advance()
		if p.isKeyword("NOT") {
			p.advance()
			if p.isKeyword("NULL") {
				p.advance()
				return &UnaryOp{Op: "IS NOT NULL", Operand: left}, nil
			}
			return nil, &ParseError{Pos: p.current().pos, Token: p.current().value, Msg: "expected NULL after IS NOT"}
		}
		if p.isKeyword("NULL") {
			p.advance()
			return &UnaryOp{Op: "IS NULL", Operand: left}, nil
		}
		if p.isKeyword("DISTINCT") {
			p.advance()
			if !p.isKeyword("FROM") {
				return nil, &ParseError{Pos: p.current().pos, Token: p.current().value, Msg: "expected FROM after IS DISTINCT"}
			}
			p.advance()
			right, err := p.parseConcat()
			if err != nil {
				return nil, err
			}
			return &BinaryOp{Op: "IS DISTINCT FROM", Left: left, Right: right}, nil
		}
		return nil, &ParseError{Pos: p.current().pos, Token: p.current().value, Msg: "expected NULL, NOT NULL, or DISTINCT FROM after IS"}
	}

	// NOT IN / NOT LIKE / NOT ILIKE (must check before bare IN/LIKE/ILIKE since NOT comes first)
	if p.isKeyword("NOT") {
		if p.pos+1 < len(p.tokens) {
			next := p.tokens[p.pos+1]
			if next.kind == tokenIdent {
				nextLower := strings.ToLower(next.value)
				if nextLower == "in" {
					p.advance() // consume NOT
					p.advance() // consume IN
					args, err := p.parseParenList()
					if err != nil {
						return nil, err
					}
					return &BinaryOp{Op: "NOT IN", Left: left, Right: &FuncCall{Name: "NOT IN", Args: args}}, nil
				}
				if nextLower == "like" {
					p.advance() // consume NOT
					p.advance() // consume LIKE
					right, err := p.parseConcat()
					if err != nil {
						return nil, err
					}
					return &BinaryOp{Op: "NOT LIKE", Left: left, Right: right}, nil
				}
				if nextLower == "ilike" {
					p.advance() // consume NOT
					p.advance() // consume ILIKE
					right, err := p.parseConcat()
					if err != nil {
						return nil, err
					}
					return &BinaryOp{Op: "NOT ILIKE", Left: left, Right: right}, nil
				}
			}
		}
		// NOT without IN/LIKE/ILIKE -- not a comparison keyword, fall through
	}

	// IN (...)
	if p.isKeyword("IN") {
		p.advance()
		args, err := p.parseParenList()
		if err != nil {
			return nil, err
		}
		return &BinaryOp{Op: "IN", Left: left, Right: &FuncCall{Name: "IN", Args: args}}, nil
	}

	// BETWEEN x AND y
	if p.isKeyword("BETWEEN") {
		p.advance()
		lo, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		if !p.isKeyword("AND") {
			return nil, &ParseError{Pos: p.current().pos, Token: p.current().value, Msg: "expected AND in BETWEEN expression"}
		}
		p.advance()
		hi, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		return &BinaryOp{Op: "BETWEEN", Left: left, Right: &BinaryOp{Op: "AND", Left: lo, Right: hi}}, nil
	}

	// LIKE / ILIKE
	if p.isKeyword("LIKE") {
		p.advance()
		right, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		return &BinaryOp{Op: "LIKE", Left: left, Right: right}, nil
	}
	if p.isKeyword("ILIKE") {
		p.advance()
		right, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		return &BinaryOp{Op: "ILIKE", Left: left, Right: right}, nil
	}

	return left, nil
}

// parseParenList parses a parenthesized comma-separated list of expressions.
func (p *parser) parseParenList() ([]Node, error) {
	if _, err := p.expect(tokenLParen); err != nil {
		return nil, &ParseError{Pos: p.current().pos, Token: p.current().value, Msg: "expected '('"}
	}
	var items []Node
	if p.current().kind != tokenRParen {
		for {
			item, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			items = append(items, item)
			if p.current().kind != tokenComma {
				break
			}
			p.advance()
		}
	}
	if _, err := p.expect(tokenRParen); err != nil {
		return nil, &ParseError{Pos: p.current().pos, Token: p.current().value, Msg: "unclosed parenthesized list"}
	}
	return items, nil
}

func (p *parser) parseConcat() (Node, error) {
	left, err := p.parseAddSub()
	if err != nil {
		return nil, err
	}
	for p.current().kind == tokenPipe {
		p.advance()
		right, err := p.parseAddSub()
		if err != nil {
			return nil, err
		}
		left = &BinaryOp{Op: "||", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseAddSub() (Node, error) {
	left, err := p.parseMulDiv()
	if err != nil {
		return nil, err
	}
	for p.current().kind == tokenPlus || p.current().kind == tokenMinus {
		op := p.advance()
		right, err := p.parseMulDiv()
		if err != nil {
			return nil, err
		}
		left = &BinaryOp{Op: op.value, Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseMulDiv() (Node, error) {
	left, err := p.parseCast()
	if err != nil {
		return nil, err
	}
	for p.current().kind == tokenStar || p.current().kind == tokenSlash || p.current().kind == tokenPercent {
		op := p.advance()
		right, err := p.parseCast()
		if err != nil {
			return nil, err
		}
		left = &BinaryOp{Op: op.value, Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseCast() (Node, error) {
	node, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for p.current().kind == tokenDoubleColon {
		p.advance()
		typeTok, err := p.expect(tokenIdent)
		if err != nil {
			return nil, &ParseError{Pos: p.current().pos, Token: p.current().value, Msg: "expected type name after ::"}
		}
		node = &Cast{Expr: node, TypeName: typeTok.value}
	}
	return node, nil
}

func (p *parser) parsePrimary() (Node, error) {
	tok := p.current()

	switch tok.kind {
	case tokenLParen:
		p.advance()
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokenRParen); err != nil {
			return nil, &ParseError{Pos: tok.pos, Token: "(", Msg: "unclosed parenthesis"}
		}
		return &ParenExpr{Inner: inner}, nil

	case tokenString:
		p.advance()
		return &StringLiteral{Value: tok.value}, nil

	case tokenInt:
		p.advance()
		val, err := strconv.Atoi(tok.value)
		if err != nil {
			return nil, &ParseError{Pos: tok.pos, Token: tok.value, Msg: "invalid integer"}
		}
		return &IntLiteral{Value: val}, nil

	case tokenFloat:
		p.advance()
		val, err := strconv.ParseFloat(tok.value, 64)
		if err != nil {
			return nil, &ParseError{Pos: tok.pos, Token: tok.value, Msg: "invalid float"}
		}
		return &FloatLiteral{Value: val}, nil

	case tokenMinus:
		p.advance()
		operand, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &UnaryOp{Op: "-", Operand: operand}, nil

	case tokenIdent:
		lower := strings.ToLower(tok.value)

		// boolean literals
		if lower == "true" {
			p.advance()
			return &BoolLiteral{Value: true}, nil
		}
		if lower == "false" {
			p.advance()
			return &BoolLiteral{Value: false}, nil
		}

		// EXISTS
		if lower == "exists" {
			return p.parseExists()
		}

		// CASE
		if lower == "case" {
			return p.parseCaseExpr()
		}

		// identifier: could be column ref, qualified name, or function call
		return p.parseIdentExpr()

	default:
		return nil, &ParseError{Pos: tok.pos, Token: tok.value, Msg: "unexpected token"}
	}
}

func (p *parser) parseExists() (Node, error) {
	p.advance() // consume EXISTS

	if _, err := p.expect(tokenLParen); err != nil {
		return nil, &ParseError{Pos: p.current().pos, Token: p.current().value, Msg: "expected '(' after EXISTS"}
	}

	if !p.isKeyword("SELECT") {
		return nil, &ParseError{Pos: p.current().pos, Token: p.current().value, Msg: "expected SELECT after EXISTS("}
	}
	p.advance() // consume SELECT

	// parse column list
	var columns []Node
	for {
		col, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		columns = append(columns, col)
		if p.current().kind != tokenComma {
			break
		}
		p.advance() // consume comma
	}

	// FROM
	if !p.isKeyword("FROM") {
		return nil, &ParseError{Pos: p.current().pos, Token: p.current().value, Msg: "expected FROM in SELECT"}
	}
	p.advance()

	// table reference (potentially qualified)
	fromRef, err := p.parseTableRef()
	if err != nil {
		return nil, err
	}

	// WHERE (optional)
	var where Node
	if p.isKeyword("WHERE") {
		p.advance()
		where, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
	}

	if _, err := p.expect(tokenRParen); err != nil {
		return nil, &ParseError{Pos: p.current().pos, Token: p.current().value, Msg: "unclosed EXISTS subquery"}
	}

	return &ExistsExpr{
		Subquery: &SelectExpr{
			Columns: columns,
			From:    fromRef,
			Where:   where,
		},
	}, nil
}

func (p *parser) parseCaseExpr() (Node, error) {
	p.advance() // consume CASE

	var whens []WhenClause
	for p.isKeyword("WHEN") {
		p.advance() // consume WHEN
		condition, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if !p.isKeyword("THEN") {
			return nil, &ParseError{Pos: p.current().pos, Token: p.current().value, Msg: "expected THEN in CASE expression"}
		}
		p.advance() // consume THEN
		result, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		whens = append(whens, WhenClause{Condition: condition, Result: result})
	}

	var elseNode Node
	if p.isKeyword("ELSE") {
		p.advance() // consume ELSE
		var err error
		elseNode, err = p.parseOr()
		if err != nil {
			return nil, err
		}
	}

	if !p.isKeyword("END") {
		return nil, &ParseError{Pos: p.current().pos, Token: p.current().value, Msg: "expected END in CASE expression"}
	}
	p.advance() // consume END

	return &CaseExpr{Whens: whens, Else: elseNode}, nil
}

func (p *parser) parseTableRef() (*ColumnRef, error) {
	nameTok, err := p.expect(tokenIdent)
	if err != nil {
		return nil, &ParseError{Pos: p.current().pos, Token: p.current().value, Msg: "expected table name"}
	}
	parts := []string{nameTok.value}
	for p.current().kind == tokenDot {
		p.advance()
		next, err := p.expect(tokenIdent)
		if err != nil {
			return nil, &ParseError{Pos: p.current().pos, Token: p.current().value, Msg: "expected identifier after '.'"}
		}
		parts = append(parts, next.value)
	}
	return &ColumnRef{Parts: parts}, nil
}

func (p *parser) parseIdentExpr() (Node, error) {
	first := p.advance() // consume the first identifier
	parts := []string{first.value}

	// consume qualified name: ident.ident.ident...
	for p.current().kind == tokenDot {
		p.advance() // consume dot
		next, err := p.expect(tokenIdent)
		if err != nil {
			return nil, &ParseError{Pos: p.current().pos, Token: p.current().value, Msg: "expected identifier after '.'"}
		}
		parts = append(parts, next.value)
	}

	// check for function call
	if p.current().kind == tokenLParen {
		p.advance() // consume (
		name := strings.Join(parts, ".")
		var args []Node
		if p.current().kind != tokenRParen {
			for {
				arg, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				args = append(args, arg)
				if p.current().kind != tokenComma {
					break
				}
				p.advance() // consume comma
			}
		}
		if _, err := p.expect(tokenRParen); err != nil {
			return nil, &ParseError{Pos: p.current().pos, Token: p.current().value, Msg: "unclosed function call"}
		}
		return &FuncCall{Name: name, Args: args}, nil
	}

	return &ColumnRef{Parts: parts}, nil
}
