package recipe

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// RunStep is one step parsed back from a recipe's "## Steps" section.
type RunStep struct {
	Title string `json:"title"`
	Label string `json:"risk"` // e.g. "safe", "risky · prod"
	Cmd   string `json:"command"`
	Undo  string `json:"undo,omitempty"`
}

// Safe reports whether the step may run without asking (SPEC D-26).
func (s RunStep) Safe() bool { return s.Label == "safe" }

var (
	stepLineRe = regexp.MustCompile(`^\d+\.\s+(.+?)\s+—\s+(.+)$`)
	codeRe     = regexp.MustCompile("`([^`]+)`")
	varRe      = regexp.MustCompile(`\{([a-z_]+)\}`)
)

// ParseSteps reads the steps of a rendered recipe (fenced or older inline commands).
func ParseSteps(doc string) []RunStep {
	_, body, ok := strings.Cut(doc, "## Steps\n")
	body = strings.TrimLeft(body, "\n")
	if !ok {
		return nil
	}
	body, _, _ = strings.Cut(body, "\n## ")
	var out []RunStep
	inFence := false
	for _, l := range strings.Split(body, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			if len(out) > 0 && out[len(out)-1].Cmd == "" && t != "" {
				out[len(out)-1].Cmd = t
			}
			continue
		}
		if m := stepLineRe.FindStringSubmatch(t); m != nil {
			out = append(out, RunStep{Title: m[1], Label: strings.Trim(m[2], "`")})
			continue
		}
		if len(out) == 0 {
			continue
		}
		cur := &out[len(out)-1]
		if strings.HasPrefix(t, "Undo:") {
			if m := codeRe.FindStringSubmatch(t); m != nil {
				cur.Undo = m[1]
			}
		} else if m := codeRe.FindStringSubmatch(t); m != nil && cur.Cmd == "" {
			cur.Cmd = m[1] // older recipes: inline `command`
		}
	}
	return out
}

// VarRef is one {variable} a step needs, in step order.
type VarRef struct {
	Name string
	Step int // 1-based
	Cmd  string
}

// MissingVars lists {variables} in steps (commands and undo) without a value,
// in first-use order.
func MissingVars(steps []RunStep, values map[string]string) []VarRef {
	var out []VarRef
	seen := map[string]bool{}
	for i, s := range steps {
		for _, m := range varRe.FindAllStringSubmatch(s.Cmd+" "+s.Undo, -1) {
			k := m[1]
			if _, done := values[k]; done || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, VarRef{Name: k, Step: i + 1, Cmd: s.Cmd})
		}
	}
	return out
}

// Fill replaces {variables} with values; unknown ones stay as they are.
func Fill(s string, values map[string]string) string {
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := values[m[1:len(m)-1]]; ok {
			return v
		}
		return m
	})
}

// Preflight is what `recap run` shows before running anything: filled
// commands, risk labels and undo (SPEC D-26). Building it executes nothing.
type Preflight struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Risk    string    `json:"risk"`
	Path    string    `json:"-"`
	Steps   []RunStep `json:"steps"`
	Missing []string  `json:"missing_vars,omitempty"`
}

// Ready reports whether every {variable} has a value.
func (p Preflight) Ready() bool { return len(p.Missing) == 0 }

// Risky reports whether any step needs a person's confirmation.
func (p Preflight) Risky() bool {
	for _, s := range p.Steps {
		if !s.Safe() {
			return true
		}
	}
	return false
}

// RunCommand is the command a person runs to execute the recipe.
func (p Preflight) RunCommand(values map[string]string) string {
	var b strings.Builder
	b.WriteString("recap run " + p.ID)
	names := make([]string, 0, len(values))
	for k := range values {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		b.WriteString(" --var " + shellQuote(k+"="+values[k]))
	}
	for _, k := range p.Missing {
		b.WriteString(" --var " + k + "=…")
	}
	b.WriteString(" --execute")
	return b.String()
}

// BuildPreflight fills a recipe's steps with values. Steps keep the recipe's
// own risk labels; nothing is run.
func BuildPreflight(m Match, doc string, values map[string]string) Preflight {
	steps := ParseSteps(doc)
	p := Preflight{ID: m.ID, Title: m.Title, Risk: m.Risk, Path: m.Path}
	for _, v := range MissingVars(steps, values) {
		p.Missing = append(p.Missing, v.Name)
	}
	for _, s := range steps {
		s.Cmd = Fill(s.Cmd, values)
		s.Undo = Fill(s.Undo, values)
		p.Steps = append(p.Steps, s)
	}
	return p
}

// BumpTrust records one run in the recipe's front matter and returns the new counts.
func BumpTrust(path string, success bool) (okc, runs int, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	doc := string(b)
	m := trustRe.FindStringSubmatch(doc)
	if m == nil {
		return 0, 0, nil
	}
	runs, _ = strconv.Atoi(m[1])
	okc, _ = strconv.Atoi(m[2])
	runs++
	if success {
		okc++
	}
	doc = strings.Replace(doc, m[0], fmt.Sprintf("trust: { runs: %d, ok: %d }", runs, okc), 1)
	return okc, runs, os.WriteFile(path, []byte(doc), 0o644)
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?&;|<>(){}[]#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
