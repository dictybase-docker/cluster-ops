// Package recipeslint implements mechanical checks for just recipes,
// mirroring scripts/docs-lint.sh. It parses `just --dump --dump-format json`
// (never scrapes raw justfiles) and checks every recipe body in the root
// justfile and all modules.
package recipeslint

import (
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Rule identifies one lint rule. Fail rules block the run; the warn rule
// only reports.
type Rule string

const (
	RuleMktempNoX         Rule = "mktemp-no-x"
	RuleCreatePipeSwallow Rule = "create-pipe-swallow"
	RuleAmbientGCPProject Rule = "ambient-gcp-project"
	RuleHardcodedStack    Rule = "hardcoded-stack"
	RuleUnquotedInterp    Rule = "unquoted-interp"
	RulePortForwardNoTrap Rule = "port-forward-no-trap" // warn-only
)

// Finding is one rule violation: the recipe namepath, the 1-based body line,
// the rule, and the offending text.
type Finding struct {
	Recipe string
	Line   int
	Rule   Rule
	Detail string
}

// warn reports whether the finding is advisory only. unquoted-interp starts
// warn-only: ~75 pre-existing bare interpolations across the repo are repo
// debt (most are trusted identifiers like {{ namespace }}), and mass-quoting
// without executing the recipes risks breaking them. Promote to fail after
// a dedicated quote() pass clears the inventory.
func (f Finding) warn() bool {
	return f.Rule == RulePortForwardNoTrap || f.Rule == RuleUnquotedInterp
}

// dump mirrors the subset of `just --dump --dump-format json` the linter reads.
type dump struct {
	Recipes map[string]recipe `json:"recipes"`
	Modules map[string]dump   `json:"modules"`
}

// recipe mirrors one recipe entry from the dump.
type recipe struct {
	// Body holds each line as a list of literal strings and interpolation
	// nodes (nested arrays); see flatten.
	Body    [][]any `json:"body"`
	Shebang bool    `json:"shebang"`
}

// interpMarker replaces every {{...}} interpolation node when flattening a
// body line; it cannot appear in shell source, so matching is unambiguous.
// safeInterpMarker marks a quote()-wrapped call — safe in bare shell context.
const (
	interpMarker     = "\x00"
	safeInterpMarker = "\x01"
)

// flatten renders a dump-json body line, collapsing interpolation nodes to
// the marker: quote() calls become safeInterpMarker, everything else
// interpMarker.
func flatten(parts []any) string {
	var b strings.Builder
	for _, p := range parts {
		if s, ok := p.(string); ok {
			b.WriteString(s)
			continue
		}
		b.WriteString(flattenedInterp(p))
	}
	return b.String()
}

// flattenedInterp picks the marker for one interpolation node: a call to
// quote() is safe outside quotes; anything else must be double-quoted.
func flattenedInterp(node any) string {
	if callsQuote(node) {
		return safeInterpMarker
	}
	return interpMarker
}

// callsQuote reports whether the interpolation node (a nested list like
// [["call","quote",...]] per the just dump JSON) invokes quote().
func callsQuote(node any) bool {
	list, ok := node.([]any)
	if !ok {
		return false
	}
	for _, el := range list {
		if head, ok := el.(string); ok && head == "quote" {
			return true
		}
		if callsQuote(el) {
			return true
		}
	}
	return false
}

// codePart strips a trailing shell comment: a '#' outside both single and
// double quotes starts a comment that runs to end of line.
func codePart(line string) string {
	if idx := scanComment(line); idx >= 0 {
		return line[:idx]
	}
	return line
}

// quoteState tracks single/double-quote context while scanning a line.
type quoteState struct{ inDQ, inSQ bool }

// next advances the state over one byte.
func (q *quoteState) next(c byte) {
	switch c {
	case '"':
		if !q.inSQ {
			q.inDQ = !q.inDQ
		}
	case '\'':
		if !q.inDQ {
			q.inSQ = !q.inSQ
		}
	}
}

// quoted reports whether the current byte sits inside either quote type.
func (q quoteState) quoted() bool { return q.inDQ || q.inSQ }

// atCommentStart reports whether the '#' at index i opens a shell comment.
func atCommentStart(line string, i int) bool {
	return i == 0 || line[i-1] == ' ' || line[i-1] == '\t'
}

// scanComment returns the byte index where an unquoted ' #' starts a shell
// comment, or -1 when the line has no comment.
func scanComment(line string) int {
	var q quoteState
	for i := 0; i < len(line); i++ {
		if line[i] == '#' {
			if !q.quoted() && atCommentStart(line, i) {
				return i
			}
			continue
		}
		q.next(line[i])
	}
	return -1
}

// unsafeInterp reports whether the interpolation at line[i:] is unsafe at
// the given double-quote depth: unsafe markers and literal {{...}} count;
// safeInterpMarker (quote()-wrapped) never does.
func unsafeInterp(line string, i, depth int) (unsafe bool, skip int) {
	switch {
	case line[i] == safeInterpMarker[0]:
		return false, 0
	case line[i] == interpMarker[0]:
		return depth == 0, 0
	case strings.HasPrefix(line[i:], "{{"):
		if depth == 0 {
			return true, 0
		}
		if end := strings.Index(line[i:], "}}"); end >= 0 {
			return false, end + 1 // resume after the closing braces
		}
		return false, 0
	}
	return false, 0
}

// interpOutsideQuotes reports whether an interpolation occurs while
// double-quote depth is zero. It recognizes the flattened markers and
// literal `{{...}}` text, but never flags safeInterpMarker (quote()-wrapped
// calls are safe bare). Interpolation inside single quotes still evaluates,
// so it is flagged too.
func interpOutsideQuotes(line string) bool {
	depth := 0
	for i := 0; i < len(line); i++ {
		if line[i] == '"' && (i == 0 || line[i-1] != '\\') {
			depth = 1 - depth
			continue
		}
		if unsafe, skip := unsafeInterp(line, i, depth); unsafe {
			return true
		} else if skip > 0 {
			i += skip
		}
	}
	return false
}

var allowRe = regexp.MustCompile(`lint-recipes:allow-([a-z-]+)`)

// allows extracts inline suppression rule ids from a body line.
func allows(line string) map[Rule]bool {
	out := map[Rule]bool{}
	for _, m := range allowRe.FindAllStringSubmatch(line, -1) {
		out[Rule(m[1])] = true
	}
	return out
}

var (
	mktempRe     = regexp.MustCompile(`\bmktemp\s+(.*)`)
	tokenRe      = regexp.MustCompile(`"[^"]*"|\S+`)
	xSuffixRe    = regexp.MustCompile(`X{4}`)
	createPipeRe = regexp.MustCompile(`\bgcloud\b.*\bcreate\b.*\|\|\s*(echo|true)\b`)
	gcpProjectRe = regexp.MustCompile(`\$\{?GOOGLE_CLOUD_PROJECT\}?\b`)
	stackRe      = regexp.MustCompile(`--stack\s+['"]?(prod|dev|staging)\b`)
	trapRe       = regexp.MustCompile(`\btrap\b`)
)

// checkMktemp returns the offending template when a mktemp invocation uses an
// explicit template lacking an X{4} suffix (macOS/BSD mktemp fails on it).
// Bare mktemp / mktemp -d is safe. Command-substitution parens and shell
// quotes around the template are stripped before the X check.
func checkMktemp(code string) (string, bool) {
	m := mktempRe.FindStringSubmatch(code)
	if m == nil {
		return "", false
	}
	var toks []string
	for _, t := range tokenRe.FindAllString(m[1], -1) {
		if strings.HasPrefix(t, "-") {
			continue
		}
		t = strings.Trim(t, "\"'")
		t = strings.TrimSuffix(t, ")")
		if t != "" {
			toks = append(toks, t)
		}
	}
	if len(toks) == 0 {
		return "", false
	}
	if slices.ContainsFunc(toks, xSuffixRe.MatchString) {
		return "", false
	}
	return toks[len(toks)-1], true
}

// lineRule checks one rule against one body line. code has comments
// stripped; raw keeps the full line (suppressions live in comments).
// The unquoted-interp rule needs the recipe's shebang flag, so it is
// gated separately in checkRecipe.
type lineRule struct {
	rule   Rule
	hit    func(code, raw string) bool
	detail func(code, raw string) string
}

// lineRules holds the rules evaluated per line.
var lineRules = []lineRule{
	{
		rule: RuleMktempNoX,
		hit:  func(code, _ string) bool { _, ok := checkMktemp(code); return ok },
		detail: func(code, _ string) string {
			t, _ := checkMktemp(code)
			return t
		},
	},
	{
		rule: RuleCreatePipeSwallow,
		hit: func(code, _ string) bool {
			return createPipeRe.MatchString(code) && !strings.Contains(code, "describe")
		},
		detail: func(_, raw string) string { return raw },
	},
	{
		rule:   RuleAmbientGCPProject,
		hit:    func(code, _ string) bool { return gcpProjectRe.MatchString(code) },
		detail: func(_, raw string) string { return raw },
	},
	{
		rule:   RuleHardcodedStack,
		hit:    func(code, _ string) bool { return stackRe.MatchString(code) },
		detail: func(_, raw string) string { return raw },
	},
	{
		rule:   RuleUnquotedInterp,
		hit:    func(code, _ string) bool { return interpOutsideQuotes(code) },
		detail: func(_, raw string) string { return raw },
	},
}

// checkRecipe runs every rule over one recipe body. Line numbers are
// 1-based over the body (shebang included).
func checkRecipe(namepath string, r recipe) []Finding {
	var findings []Finding
	hasPF, hasTrap := false, false
	for i, parts := range r.Body {
		raw := flatten(parts)
		code := codePart(raw)
		if strings.Contains(code, "port-forward") {
			hasPF = true
		}
		if trapRe.MatchString(code) {
			hasTrap = true
		}
		findings = append(findings, checkLine(namepath, i+1, r, raw, code)...)
	}
	if hasPF && !hasTrap {
		findings = append(findings, Finding{
			Recipe: namepath, Line: 1, Rule: RulePortForwardNoTrap,
			Detail: "recipe uses kubectl port-forward but has no trap/cleanup",
		})
	}
	return findings
}

// checkLine applies every line rule to one body line.
func checkLine(namepath string, line int, r recipe, raw, code string) []Finding {
	alw := allows(raw)
	var findings []Finding
	for _, lr := range lineRules {
		if alw[lr.rule] || !lr.hit(code, raw) {
			continue
		}
		if lr.rule == RuleUnquotedInterp && !r.Shebang {
			// Non-shebang recipe lines pass interpolations to the
			// recipe's argument context, not to a shell.
			continue
		}
		findings = append(findings, Finding{
			Recipe: namepath, Line: line, Rule: lr.rule, Detail: lr.detail(code, raw),
		})
	}
	return findings
}

// Walk collects findings across the root recipes and every module, sorted by
// recipe namepath, line, and rule for deterministic output (dump maps are
// unordered).
func Walk(d dump) []Finding {
	var findings []Finding
	var walk func(prefix string, d dump)
	walk = func(prefix string, d dump) {
		for name, r := range d.Recipes {
			findings = append(findings, checkRecipe(prefix+name, r)...)
		}
		for modName, mod := range d.Modules {
			walk(prefix+modName+":", mod)
		}
	}
	walk("", d)
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Recipe != b.Recipe {
			return a.Recipe < b.Recipe
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Rule < b.Rule
	})
	return findings
}
