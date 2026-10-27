package recipe

import (
	"errors"
	"strings"
	"time"

	"github.com/idestis/recap/internal/redact"
	"github.com/idestis/recap/internal/risk"
	"github.com/idestis/recap/internal/session"
)

// StepInput is one step proposed by an agent (MCP save_recipe).
type StepInput struct {
	Title string `json:"title,omitempty" jsonschema:"short step title; derived from the command when empty"`
	Cmd   string `json:"command" jsonschema:"the shell command, with {variables} for values that change between runs"`
	Undo  string `json:"undo,omitempty" jsonschema:"command that reverts the step, if any"`
	Risk  string `json:"risk,omitempty" jsonschema:"optional risk hint: safe, risky or destructive, optionally with prod; rules decide and a hint can only raise risk"`
}

// Proposal is a recipe proposed by an agent: either full recipe Markdown or fields.
type Proposal struct {
	Markdown      string      `json:"markdown,omitempty" jsonschema:"a full recipe in recap's Markdown format (front matter, ## Situation, ## Preconditions, ## Steps); use this or the fields"`
	ID            string      `json:"id,omitempty" jsonschema:"recipe id (kebab-case); derived from the title when empty"`
	Title         string      `json:"title,omitempty" jsonschema:"recipe title, e.g. Fix OOMKilled pods"`
	Situation     string      `json:"situation,omitempty" jsonschema:"what was wrong and how it showed up"`
	Preconditions []string    `json:"preconditions,omitempty" jsonschema:"things to check first, e.g. do not roll back if a migration ran"`
	Steps         []StepInput `json:"steps,omitempty" jsonschema:"the steps that fixed it, in order"`
}

// FromProposal builds a recipe from an agent's proposal with the same gates as
// a recorded session (SPEC D-17): everything is redacted before naming and
// before it can reach Git (D-11), risk comes from the rules and a hint may
// only raise it (D-12), values become {variables} and undo is filled where
// recap knows one. Returns the recipe and the number of masked secrets.
func FromProposal(p Proposal, prodContexts []string, now time.Time) (*Recipe, int, error) {
	if strings.TrimSpace(p.Markdown) != "" {
		var err error
		if p, err = parseProposal(p.Markdown); err != nil {
			return nil, 0, err
		}
	}
	masked := 0
	clean := func(s string) string {
		r := redact.Redact(strings.TrimSpace(s))
		masked += r.Count
		return r.Text
	}
	title := clean(p.Title)
	id := clean(p.ID)
	if title == "" && id == "" {
		return nil, 0, errors.New("a recipe needs a title")
	}
	if id == "" {
		id = title
	}
	if title == "" {
		title = strings.ReplaceAll(id, "-", " ")
	}
	r := &Recipe{ID: session.Slug(id), Title: titleCase(title), Situation: clean(p.Situation)}
	if r.Situation == "" {
		r.Situation = r.Title
	}
	for _, pc := range p.Preconditions {
		if pc = clean(pc); pc != "" {
			r.Preconditions = append(r.Preconditions, pc)
		}
	}
	vars := map[string]bool{}
	tools := map[string]bool{}
	ctx := ""
	for _, in := range p.Steps {
		text := clean(in.Cmd)
		if text == "" {
			continue
		}
		// Context comes from recorded switches in the steps, never live state (SPEC D-05).
		if c, ok := risk.ContextSwitch(text); ok {
			ctx = c
		}
		res := risk.Classify(text, ctx, prodContexts)
		res = raise(res, in.Risk)
		if res.Tool != "" {
			tools[res.Tool] = true
		}
		cmd := parametrize(text, vars)
		undo := clean(in.Undo)
		if undo != "" {
			undo = parametrize(undo, vars)
		} else {
			undo = undoFor(cmd)
		}
		for _, m := range varRe.FindAllStringSubmatch(cmd+" "+undo, -1) {
			vars[m[1]] = true
		}
		st := clean(in.Title)
		if st == "" {
			st = stepTitle(text)
		}
		r.Steps = append(r.Steps, Step{Title: st, Cmd: cmd, Risk: res, Undo: undo})
	}
	if len(r.Steps) == 0 {
		return nil, masked, errors.New("a recipe needs at least one step with a command")
	}
	// Nothing ran: no trust and no verified environment until a person runs it.
	r.Unrun = true
	r.Vars = keys(vars)
	r.Tools = keys(tools)
	return r, masked, nil
}

// raise applies a risk hint (a step label such as "risky · prod"). Hints only
// ever raise the rule result, never lower it (SPEC D-12).
func raise(r risk.Result, hint string) risk.Result {
	h := strings.ToLower(hint)
	switch {
	case strings.Contains(h, "destructive") && r.Level < risk.Destructive:
		r.Level = risk.Destructive
		r.Reasons = append(r.Reasons, "marked destructive")
	case strings.Contains(h, "risky") && r.Level < risk.Risky:
		r.Level = risk.Risky
		r.Reasons = append(r.Reasons, "marked risky")
	}
	if strings.Contains(h, "prod") && !r.Prod {
		r.Prod = true
		r.Reasons = append(r.Reasons, "marked prod")
	}
	return r
}

// parseProposal reads recipe Markdown in recap's own format.
func parseProposal(md string) (Proposal, error) {
	fm := frontMatter(md)
	body := md
	if strings.HasPrefix(md, "---\n") {
		if _, rest, ok := strings.Cut(md[4:], "\n---\n"); ok {
			body = rest
		}
	}
	p := Proposal{ID: fm["id"], Title: fm["title"], Situation: section(body, "Situation")}
	if p.Title == "" {
		for _, l := range strings.Split(body, "\n") {
			if strings.HasPrefix(l, "# ") {
				p.Title = strings.TrimSpace(l[2:])
				break
			}
		}
	}
	for _, l := range strings.Split(section(body, "Preconditions"), "\n") {
		if t := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "-*")); t != "" {
			p.Preconditions = append(p.Preconditions, t)
		}
	}
	for _, s := range ParseSteps(body) {
		p.Steps = append(p.Steps, StepInput{Title: s.Title, Cmd: s.Cmd, Undo: s.Undo, Risk: s.Label})
	}
	if len(p.Steps) == 0 {
		return p, errors.New("no steps found: the Markdown needs a \"## Steps\" section with numbered steps like \"1. Title — `safe`\" and a ```sh block")
	}
	return p, nil
}
