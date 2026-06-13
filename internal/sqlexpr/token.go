package sqlexpr

import (
	"fmt"
	"strings"
	"unicode"
)

type tokenKind int

const (
	tokenIdent tokenKind = iota
	tokenString
	tokenInt
	tokenLParen
	tokenRParen
	tokenDot
	tokenDoubleColon
	tokenEquals
	tokenNotEquals
	tokenComma
	tokenPipe
	tokenStar
	tokenPlus
	tokenMinus
	tokenEOF
)

type token struct {
	kind  tokenKind
	value string
	pos   int
}

func tokenize(input string) ([]token, error) {
	var tokens []token
	runes := []rune(input)
	i := 0

	for i < len(runes) {
		// skip whitespace
		if unicode.IsSpace(runes[i]) {
			i++
			continue
		}

		pos := i

		switch {
		case runes[i] == '(':
			tokens = append(tokens, token{kind: tokenLParen, value: "(", pos: pos})
			i++

		case runes[i] == ')':
			tokens = append(tokens, token{kind: tokenRParen, value: ")", pos: pos})
			i++

		case runes[i] == ',':
			tokens = append(tokens, token{kind: tokenComma, value: ",", pos: pos})
			i++

		case runes[i] == '.':
			tokens = append(tokens, token{kind: tokenDot, value: ".", pos: pos})
			i++

		case runes[i] == '=' :
			tokens = append(tokens, token{kind: tokenEquals, value: "=", pos: pos})
			i++

		case runes[i] == ':' && i+1 < len(runes) && runes[i+1] == ':':
			tokens = append(tokens, token{kind: tokenDoubleColon, value: "::", pos: pos})
			i += 2

		case runes[i] == '!' && i+1 < len(runes) && runes[i+1] == '=':
			tokens = append(tokens, token{kind: tokenNotEquals, value: "!=", pos: pos})
			i += 2

		case runes[i] == '<' && i+1 < len(runes) && runes[i+1] == '>':
			tokens = append(tokens, token{kind: tokenNotEquals, value: "<>", pos: pos})
			i += 2

		case runes[i] == '|' && i+1 < len(runes) && runes[i+1] == '|':
			tokens = append(tokens, token{kind: tokenPipe, value: "||", pos: pos})
			i += 2

		case runes[i] == '*':
			tokens = append(tokens, token{kind: tokenStar, value: "*", pos: pos})
			i++

		case runes[i] == '+':
			tokens = append(tokens, token{kind: tokenPlus, value: "+", pos: pos})
			i++

		case runes[i] == '-':
			tokens = append(tokens, token{kind: tokenMinus, value: "-", pos: pos})
			i++

		case runes[i] == '\'':
			// single-quoted string
			i++ // skip opening quote
			var sb strings.Builder
			for {
				if i >= len(runes) {
					return nil, fmt.Errorf("sqlexpr: unclosed string literal at position %d", pos)
				}
				if runes[i] == '\'' {
					// check for escaped quote ''
					if i+1 < len(runes) && runes[i+1] == '\'' {
						sb.WriteRune('\'')
						i += 2
						continue
					}
					// end of string
					i++
					break
				}
				sb.WriteRune(runes[i])
				i++
			}
			tokens = append(tokens, token{kind: tokenString, value: sb.String(), pos: pos})

		case unicode.IsDigit(runes[i]):
			start := i
			for i < len(runes) && unicode.IsDigit(runes[i]) {
				i++
			}
			tokens = append(tokens, token{kind: tokenInt, value: string(runes[start:i]), pos: pos})

		case runes[i] == '_' || unicode.IsLetter(runes[i]):
			start := i
			for i < len(runes) && (runes[i] == '_' || unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i])) {
				i++
			}
			tokens = append(tokens, token{kind: tokenIdent, value: string(runes[start:i]), pos: pos})

		default:
			return nil, fmt.Errorf("sqlexpr: unexpected character %q at position %d", runes[i], pos)
		}
	}

	tokens = append(tokens, token{kind: tokenEOF, value: "", pos: i})
	return tokens, nil
}
