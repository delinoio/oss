// Package rules validates and evaluates RealQA's documented safe title rules.
package rules

import (
	"errors"
	"net/url"
	"regexp"
	"regexp/syntax"
	"strings"
	"unicode/utf8"
)

const (
	MaxPatternBytes      = 512
	MaxCompiledInst      = 2048
	MaxRulesPerPreset    = 64
	MaxExactProcessBytes = 255
	MaxURLTemplateBytes  = 2048
	MaxExpandedURLBytes  = 8192
	MaxBoundedRepetition = 100
)

// Rule is ordered by slice position. Process matching is exact and
// case-sensitive; an empty title pattern matches every window title.
type Rule struct {
	ExactProcessName string
	TitlePattern     string
	URLTemplate      string
	Enabled          bool
}

type compiledRule struct {
	rule  Rule
	title *regexp.Regexp
}

type Set struct {
	rules []compiledRule
}

// Compile accepts the common non-backtracking subset implemented by both
// Rust's regex crate and Go's RE2-derived engine:
//
//	literals, '.', ^/$/\A/\z anchors, character classes and Unicode classes,
//	grouping/non-capturing grouping, alternation, ?, *, +, and {m,n} where
//	explicit bounds do not exceed 100, plus i/m/s/U scoped flags.
//
// Look-around, backreferences, named captures, octal escapes, \C, \Q...\E,
// class set algebra, and free-spacing mode are rejected even if a runtime
// happens to accept them. This keeps behavior compatible with the Rust client.
func Compile(input []Rule) (*Set, error) {
	if len(input) > MaxRulesPerPreset {
		return nil, errors.New("too many process URL rules")
	}
	result := &Set{rules: make([]compiledRule, 0, len(input))}
	for _, rule := range input {
		if rule.ExactProcessName == "" ||
			len(rule.ExactProcessName) > MaxExactProcessBytes ||
			!utf8.ValidString(rule.ExactProcessName) {
			return nil, errors.New("invalid exact process name")
		}
		if err := validateTemplate(rule.URLTemplate); err != nil {
			return nil, err
		}
		var expression *regexp.Regexp
		if rule.TitlePattern != "" {
			if err := validatePatternText(rule.TitlePattern); err != nil {
				return nil, err
			}
			parsed, err := syntax.Parse(rule.TitlePattern, syntax.Perl)
			if err != nil {
				return nil, errors.New("invalid safe title pattern")
			}
			if err = validateRepetitions(parsed); err != nil {
				return nil, err
			}
			program, err := syntax.Compile(parsed.Simplify())
			if err != nil || len(program.Inst) > MaxCompiledInst {
				return nil, errors.New("safe title pattern is too complex")
			}
			expression, err = regexp.Compile(rule.TitlePattern)
			if err != nil {
				return nil, errors.New("invalid safe title pattern")
			}
		}
		result.rules = append(result.rules, compiledRule{rule: rule, title: expression})
	}
	return result, nil
}

func validatePatternText(pattern string) error {
	if pattern == "" || len(pattern) > MaxPatternBytes || !utf8.ValidString(pattern) {
		return errors.New("invalid safe title pattern length")
	}
	for _, forbidden := range []string{
		"(?=", "(?!", "(?<=", "(?<!", "(?P<", "(?<", `\k`, `\g`,
		`\C`, `\Q`, `\E`, "(?x", "(?x:",
	} {
		if strings.Contains(pattern, forbidden) {
			return errors.New("safe title pattern uses unsupported syntax")
		}
	}
	if containsUnsupportedCharacterClassSyntax(pattern) {
		return errors.New("safe title pattern uses unsupported syntax")
	}
	if hasUnsafeRepeatedOrUnbalancedGroup(pattern) {
		return errors.New("safe title pattern uses unsafe repeated group")
	}
	for index := 0; index+1 < len(pattern); index++ {
		if pattern[index] == '\\' && pattern[index+1] >= '0' && pattern[index+1] <= '9' {
			return errors.New("safe title pattern uses unsupported numeric escape")
		}
	}
	return nil
}

func containsUnsupportedCharacterClassSyntax(pattern string) bool {
	inClass := false
	inPOSIXClass := false
	for index := 0; index < len(pattern); index++ {
		if pattern[index] == '\\' {
			index++
			continue
		}
		if !inClass {
			if pattern[index] == ']' {
				return true
			}
			inClass = pattern[index] == '['
			continue
		}
		if !inPOSIXClass && strings.HasPrefix(pattern[index:], "[:") {
			inPOSIXClass = true
			index++
			continue
		}
		if inPOSIXClass && strings.HasPrefix(pattern[index:], ":]") {
			inPOSIXClass = false
			index++
			continue
		}
		if !inPOSIXClass && pattern[index] == ']' {
			inClass = false
			continue
		}
		if !inPOSIXClass &&
			(strings.HasPrefix(pattern[index:], "&&") ||
				strings.HasPrefix(pattern[index:], "--") ||
				strings.HasPrefix(pattern[index:], "~~")) {
			return true
		}
	}
	// Keep the synchronized subset closed when POSIX-looking text never reaches
	// its ":]" terminator; Go otherwise accepts some of these forms as literals.
	return inPOSIXClass
}

type patternGroupState struct {
	containsAlternation bool
	containsRepetition  bool
}

func repetitionLength(pattern string, index int) int {
	if index >= len(pattern) {
		return 0
	}
	if pattern[index] == '*' || pattern[index] == '+' || pattern[index] == '?' {
		return 1
	}
	if pattern[index] != '{' {
		return 0
	}
	cursor := index + 1
	if cursor >= len(pattern) || pattern[cursor] < '0' || pattern[cursor] > '9' {
		return 0
	}
	if pattern[cursor] == '0' {
		cursor++
	} else {
		for cursor < len(pattern) && pattern[cursor] >= '0' && pattern[cursor] <= '9' {
			cursor++
		}
	}
	if cursor < len(pattern) && pattern[cursor] == ',' {
		cursor++
		if cursor < len(pattern) && pattern[cursor] == '0' {
			cursor++
		} else if cursor < len(pattern) && pattern[cursor] >= '1' && pattern[cursor] <= '9' {
			cursor++
			for cursor < len(pattern) && pattern[cursor] >= '0' && pattern[cursor] <= '9' {
				cursor++
			}
		}
	}
	if cursor >= len(pattern) || pattern[cursor] != '}' {
		return 0
	}
	return cursor - index + 1
}

func inlineFlagPrefixLength(pattern string, index int, terminator byte) int {
	if !strings.HasPrefix(pattern[index:], "(?") {
		return 0
	}
	cursor := index + 2
	for cursor < len(pattern) && strings.ContainsRune("imsU", rune(pattern[cursor])) {
		cursor++
	}
	if cursor < len(pattern) && pattern[cursor] == '-' {
		cursor++
		for cursor < len(pattern) && strings.ContainsRune("imsU", rune(pattern[cursor])) {
			cursor++
		}
	}
	if cursor >= len(pattern) || pattern[cursor] != terminator {
		return 0
	}
	return cursor - index + 1
}

func bracedHexEscapeLength(pattern string, index int) int {
	if !strings.HasPrefix(pattern[index:], `\x{`) {
		return 0
	}
	cursor := index + 3
	firstDigit := cursor
	for cursor < len(pattern) &&
		((pattern[cursor] >= '0' && pattern[cursor] <= '9') ||
			(pattern[cursor] >= 'A' && pattern[cursor] <= 'F') ||
			(pattern[cursor] >= 'a' && pattern[cursor] <= 'f')) {
		cursor++
	}
	if cursor == firstDigit || cursor >= len(pattern) || pattern[cursor] != '}' {
		return 0
	}
	return cursor - index + 1
}

func hasUnsafeRepeatedOrUnbalancedGroup(pattern string) bool {
	groups := make([]patternGroupState, 0)
	inClass := false
	inPOSIXClass := false
	for index := 0; index < len(pattern); index++ {
		token := pattern[index]
		if token == '\\' {
			if escapeLength := bracedHexEscapeLength(pattern, index); escapeLength > 0 {
				index += escapeLength - 1
			} else {
				index++
			}
			continue
		}
		if inClass && !inPOSIXClass && strings.HasPrefix(pattern[index:], "[:") {
			inPOSIXClass = true
			index++
			continue
		}
		if inPOSIXClass && strings.HasPrefix(pattern[index:], ":]") {
			inPOSIXClass = false
			index++
			continue
		}
		if token == '[' {
			inClass = true
			continue
		}
		if token == ']' && inClass && !inPOSIXClass {
			inClass = false
			continue
		}
		if inClass {
			continue
		}
		if token == '(' {
			if prefixLength := inlineFlagPrefixLength(pattern, index, ')'); prefixLength > 0 {
				index += prefixLength - 1
				continue
			}
			groups = append(groups, patternGroupState{})
			if prefixLength := inlineFlagPrefixLength(pattern, index, ':'); prefixLength > 0 {
				index += prefixLength - 1
			}
			continue
		}
		if token == '|' {
			if len(groups) > 0 {
				groups[len(groups)-1].containsAlternation = true
			}
			continue
		}
		if token == ')' {
			if len(groups) == 0 {
				continue
			}
			group := groups[len(groups)-1]
			groups = groups[:len(groups)-1]
			repeatedBy := repetitionLength(pattern, index+1)
			if repeatedBy > 0 &&
				(group.containsAlternation || group.containsRepetition) {
				return true
			}
			if len(groups) > 0 {
				parent := &groups[len(groups)-1]
				parent.containsAlternation = parent.containsAlternation ||
					group.containsAlternation
				parent.containsRepetition = parent.containsRepetition ||
					group.containsRepetition || repeatedBy > 0
			}
			index += repeatedBy
			continue
		}
		if repeatedBy := repetitionLength(pattern, index); repeatedBy > 0 {
			if len(groups) > 0 {
				groups[len(groups)-1].containsRepetition = true
			}
			index += repeatedBy - 1
		}
	}
	return len(groups) > 0
}

func validateRepetitions(expression *syntax.Regexp) error {
	if expression == nil {
		return errors.New("invalid safe title pattern")
	}
	if expression.Op == syntax.OpRepeat &&
		(expression.Min > MaxBoundedRepetition || expression.Max > MaxBoundedRepetition) {
		return errors.New("safe title pattern repetition is too large")
	}
	for _, child := range expression.Sub {
		if err := validateRepetitions(child); err != nil {
			return err
		}
	}
	return nil
}

func validateTemplate(template string) error {
	if template == "" || len(template) > MaxURLTemplateBytes ||
		!utf8.ValidString(template) || strings.Contains(template, `\`) {
		return errors.New("invalid URL template")
	}
	probe := regexp.MustCompile(`\$(?:\{[0-9]+\}|[0-9]+)`).ReplaceAllString(template, "x")
	parsed, err := url.Parse(probe)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil {
		return errors.New("URL template must produce an HTTP or HTTPS URL without credentials")
	}
	return nil
}

// Resolve returns the first enabled matching rule. Captures use Rust/Go-common
// $1 or ${1} expansion. The expanded URL is validated again before return.
func (set *Set) Resolve(processName, windowTitle string) (string, bool) {
	if set == nil {
		return "", false
	}
	for _, candidate := range set.rules {
		if !candidate.rule.Enabled || candidate.rule.ExactProcessName != processName {
			continue
		}
		expanded := candidate.rule.URLTemplate
		if candidate.title != nil {
			match := candidate.title.FindStringSubmatchIndex(windowTitle)
			if match == nil {
				continue
			}
			expanded = string(candidate.title.ExpandString(
				nil, candidate.rule.URLTemplate, windowTitle, match))
		}
		if len(expanded) > MaxExpandedURLBytes || validateResolvedURL(expanded) != nil {
			continue
		}
		return expanded, true
	}
	return "", false
}

func validateResolvedURL(value string) error {
	if strings.Contains(value, `\`) {
		return errors.New("invalid resolved URL")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil {
		return errors.New("invalid resolved URL")
	}
	return nil
}
