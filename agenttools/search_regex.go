package agenttools

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxSearchRepeat = 4096
const maxExpandedSearchRegex = 128 << 10
const maxSearchRegexInstructions = 32768

// Search exposes locations, never capture groups. For oversized counted
// repetitions, expand the SAME regular language into concatenated mandatory
// and optional atoms, then use Go's non-backtracking engine. Never replace a
// finite bound with .* or accept backreferences/lookaround. Both expanded
// source and compiled program are bounded, including nested repetitions.
func compileSearchRegex(expression string) (*regexp.Regexp, error) {
	re, err := regexp.Compile(expression)
	if err == nil {
		return re, nil
	}
	var parseError *syntax.Error
	if !errors.As(err, &parseError) || parseError.Code != syntax.ErrInvalidRepeatSize {
		return nil, err
	}
	expanded, normalized, err := expandSearchRegex(expression, 0)
	if err != nil {
		return nil, err
	}
	// Replacing each syntactic count with {1} validates the original atom /
	// quantifier structure. Expansion must not accidentally repair a** or
	// a*{2000}, bad group syntax, or a quantifier with no preceding atom.
	if _, err := syntax.Parse(normalized, syntax.Perl); err != nil {
		return nil, err
	}
	parsed, err := syntax.Parse(expanded, syntax.Perl)
	if err != nil {
		return nil, err
	}
	program, err := syntax.Compile(parsed.Simplify())
	if err != nil {
		return nil, err
	}
	if len(program.Inst) > maxSearchRegexInstructions {
		return nil, fmt.Errorf("expanded regex exceeds %d instructions", maxSearchRegexInstructions)
	}
	return regexp.Compile(expanded)
}

func expandSearchRegex(expression string, depth int) (string, string, error) {
	if depth > 100 {
		return "", "", fmt.Errorf("regex group nesting exceeds 100")
	}
	var output, normalized strings.Builder
	for position := 0; position < len(expression); {
		start := position
		var atom, norm string
		switch expression[position] {
		case '(':
			end, err := regexGroupEnd(expression, position)
			if err != nil {
				return "", "", err
			}
			inner, innerNorm, err := expandSearchRegex(expression[position+1:end-1], depth+1)
			if err != nil {
				return "", "", err
			}
			atom, norm, position = "("+inner+")", "("+innerNorm+")", end
		case '[':
			end, err := regexClassEnd(expression, position)
			if err != nil {
				return "", "", err
			}
			position = end
			atom = expression[start:position]
			norm = atom
		case '\\':
			position = regexEscapeEnd(expression, position)
			atom = expression[start:position]
			norm = atom
			if strings.HasPrefix(atom, `\Q`) {
				quoted := strings.TrimSuffix(strings.TrimPrefix(atom, `\Q`), `\E`)
				if quoted == "" {
					continue
				}
				_, size := utf8.DecodeLastRuneInString(quoted)
				prefix := regexp.QuoteMeta(quoted[:len(quoted)-size])
				output.WriteString(prefix)
				normalized.WriteString(prefix)
				atom = regexp.QuoteMeta(quoted[len(quoted)-size:])
				norm = atom
			}
		default:
			_, size := utf8.DecodeRuneInString(expression[position:])
			position += size
			atom = expression[start:position]
			norm = atom
		}
		minimum, maximum, end, counted, err := searchRepeat(expression, position)
		if err != nil {
			return "", "", err
		}
		if counted {
			lazy := end < len(expression) && expression[end] == '?'
			if lazy {
				end++
			}
			norm += "{1}"
			if lazy {
				norm += "?"
			}
			unit := "(?:" + atom + ")"
			optional := unit + "?"
			if lazy {
				optional += "?"
			}
			copies := maximum
			if copies < 0 {
				copies = minimum + 1
			}
			if (len(unit)+2)*copies+output.Len() > maxExpandedSearchRegex {
				return "", "", fmt.Errorf("expanded regex exceeds %d bytes", maxExpandedSearchRegex)
			}
			atom = strings.Repeat(unit, minimum)
			if maximum < 0 {
				atom += unit + "*"
				if lazy {
					atom += "?"
				}
			} else {
				atom += strings.Repeat(optional, maximum-minimum)
			}
			position = end
		}
		output.WriteString(atom)
		normalized.WriteString(norm)
		if output.Len() > maxExpandedSearchRegex {
			return "", "", fmt.Errorf("expanded regex exceeds %d bytes", maxExpandedSearchRegex)
		}
	}
	return output.String(), normalized.String(), nil
}

func searchRepeat(expression string, position int) (int, int, int, bool, error) {
	if position >= len(expression) || expression[position] != '{' {
		return 0, 0, position, false, nil
	}
	end := strings.IndexByte(expression[position:], '}')
	if end < 0 {
		return 0, 0, position, false, nil
	}
	end += position + 1
	parts := strings.Split(expression[position+1:end-1], ",")
	if len(parts) > 2 {
		return 0, 0, position, false, nil
	}
	parse := func(value string) (int, bool) {
		if value == "" || len(value) > 1 && value[0] == '0' {
			return 0, false
		}
		for _, c := range value {
			if c < '0' || c > '9' {
				return 0, false
			}
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			return maxSearchRepeat + 1, true
		}
		return n, true
	}
	minimum, valid := parse(parts[0])
	if !valid {
		return 0, 0, position, false, nil
	}
	maximum := minimum
	if len(parts) == 2 {
		if parts[1] == "" {
			maximum = -1
		} else {
			maximum, valid = parse(parts[1])
			if !valid {
				return 0, 0, position, false, nil
			}
		}
	}
	if minimum > maxSearchRepeat || maximum > maxSearchRepeat {
		return 0, 0, position, false, fmt.Errorf("counted regex repetition supports bounds 0..%d", maxSearchRepeat)
	}
	if maximum >= 0 && minimum > maximum {
		return 0, 0, position, false, fmt.Errorf("invalid repeat count: {%s}", expression[position+1:end-1])
	}
	return minimum, maximum, end, true, nil
}

func regexEscapeEnd(expression string, position int) int {
	end := min(position+2, len(expression))
	if end-position < 2 {
		return end
	}
	switch expression[position+1] {
	case 'Q':
		if close := strings.Index(expression[end:], `\E`); close >= 0 {
			return end + close + 2
		}
		return len(expression)
	case 'p', 'P', 'x':
		if end < len(expression) && expression[end] == '{' {
			if close := strings.IndexByte(expression[end:], '}'); close >= 0 {
				return end + close + 1
			}
		} else if expression[position+1] == 'x' {
			return min(end+2, len(expression))
		} else {
			return min(end+1, len(expression))
		}
	default:
		if expression[position+1] >= '0' && expression[position+1] <= '7' {
			for end < len(expression) && end < position+4 && expression[end] >= '0' && expression[end] <= '7' {
				end++
			}
		}
	}
	return end
}

func regexClassEnd(expression string, position int) (int, error) {
	first := position + 1
	if first < len(expression) && expression[first] == '^' {
		first++
	}
	for end := first; end < len(expression); end++ {
		if expression[end] == '\\' {
			end = regexEscapeEnd(expression, end) - 1
			continue
		}
		if expression[end] == '[' && strings.HasPrefix(expression[end:], "[:") {
			if close := strings.Index(expression[end+2:], ":]"); close >= 0 {
				end += close + 3
				continue
			}
		}
		if expression[end] == ']' && end != first {
			return end + 1, nil
		}
	}
	return 0, fmt.Errorf("unterminated regex character class")
}

func regexGroupEnd(expression string, position int) (int, error) {
	depth := 1
	for end := position + 1; end < len(expression); end++ {
		switch expression[end] {
		case '\\':
			end = regexEscapeEnd(expression, end) - 1
		case '[':
			close, err := regexClassEnd(expression, end)
			if err != nil {
				return 0, err
			}
			end = close - 1
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return end + 1, nil
			}
		}
	}
	return 0, fmt.Errorf("unterminated regex group")
}
