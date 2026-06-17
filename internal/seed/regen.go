package seed

import (
	"math/rand"
	"regexp/syntax"
	"strings"
)

const regenDefaultRepeat = 5

// regenFromPattern generates a random string matching the given regex pattern.
// Returns the generated string and any error from parsing.
func regenFromPattern(pattern string, rng *rand.Rand) (string, error) {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return "", err
	}
	re = re.Simplify()
	return regenWalk(re, rng), nil
}

func regenWalk(re *syntax.Regexp, rng *rand.Rand) string {
	switch re.Op {
	case syntax.OpLiteral:
		return string(re.Rune)

	case syntax.OpCharClass:
		// re.Rune has pairs [lo1, hi1, lo2, hi2, ...]
		n := len(re.Rune) / 2
		if n == 0 {
			return ""
		}
		pair := rng.Intn(n)
		lo := re.Rune[pair*2]
		hi := re.Rune[pair*2+1]
		r := lo + rune(rng.Intn(int(hi-lo+1)))
		return string(r)

	case syntax.OpConcat:
		var b strings.Builder
		for _, sub := range re.Sub {
			b.WriteString(regenWalk(sub, rng))
		}
		return b.String()

	case syntax.OpAlternate:
		i := rng.Intn(len(re.Sub))
		return regenWalk(re.Sub[i], rng)

	case syntax.OpStar:
		count := rng.Intn(regenDefaultRepeat + 1) // 0 to regenDefaultRepeat
		return regenRepeat(re.Sub[0], count, rng)

	case syntax.OpPlus:
		count := 1 + rng.Intn(regenDefaultRepeat) // 1 to regenDefaultRepeat
		return regenRepeat(re.Sub[0], count, rng)

	case syntax.OpQuest:
		if rng.Intn(2) == 0 {
			return ""
		}
		return regenWalk(re.Sub[0], rng)

	case syntax.OpRepeat:
		max := re.Max
		if max == -1 {
			max = re.Min + regenDefaultRepeat
		}
		count := re.Min
		if max > re.Min {
			count += rng.Intn(max - re.Min + 1)
		}
		return regenRepeat(re.Sub[0], count, rng)

	case syntax.OpAnyCharNotNL, syntax.OpAnyChar:
		r := rune(33 + rng.Intn(94)) // printable ASCII 33-126
		return string(r)

	case syntax.OpCapture:
		return regenWalk(re.Sub[0], rng)

	case syntax.OpBeginLine, syntax.OpEndLine,
		syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary,
		syntax.OpEmptyMatch, syntax.OpNoMatch:
		return ""

	default:
		return ""
	}
}

func regenRepeat(re *syntax.Regexp, count int, rng *rand.Rand) string {
	if count <= 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < count; i++ {
		b.WriteString(regenWalk(re, rng))
	}
	return b.String()
}
