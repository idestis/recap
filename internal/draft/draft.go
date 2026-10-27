// Package draft improves a template-drafted recipe with a model (SPEC D-13).
// Input is the already redacted recipe (SPEC D-11). The model only rewrites
// text; commands stay as captured, and risk can only go up (SPEC D-12).
package draft

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/idestis/recap/internal/llm"
	"github.com/idestis/recap/internal/recipe"
	"github.com/idestis/recap/internal/redact"
	"github.com/idestis/recap/internal/risk"
	"github.com/idestis/recap/internal/session"
)

//go:embed prompts/draft.md
var systemPrompt string

type input struct {
	Description string   `json:"description"`
	Notes       []string `json:"notes,omitempty"`
	Decisions   []string `json:"decisions,omitempty"`
	Steps       []step   `json:"steps"`
}

type step struct {
	N    int    `json:"n"`
	Cmd  string `json:"command"`
	Risk string `json:"risk"`
}

type output struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Situation     string   `json:"situation"`
	Preconditions []string `json:"preconditions"`
	Steps         []struct {
		N     int    `json:"n"`
		Title string `json:"title"`
	} `json:"steps"`
	Risk []struct {
		N     int    `json:"n"`
		Level string `json:"level"`
		Why   string `json:"why"`
	} `json:"risk"`
}

// Enhance asks the model for better titles and a situation summary and merges
// the answer into r. On any error r is left unchanged.
func Enhance(ctx context.Context, c *llm.Client, se *session.Session, evs []session.Event, r *recipe.Recipe) error {
	in := input{Description: redact.Redact(se.Description).Text}
	for _, e := range evs {
		if e.Kind != "note" {
			continue
		}
		t := redact.Redact(e.Text).Text
		if contains(e.Tags, "decision") {
			in.Decisions = append(in.Decisions, t)
		} else {
			in.Notes = append(in.Notes, t)
		}
	}
	for i, s := range r.Steps {
		in.Steps = append(in.Steps, step{N: i + 1, Cmd: s.Cmd, Risk: s.Risk.Label()})
	}
	b, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return err
	}
	var out output
	if err := c.JSON(ctx, systemPrompt, string(b), &out); err != nil {
		return err
	}
	return merge(r, out)
}

func merge(r *recipe.Recipe, out output) error {
	if len(out.Steps) > 0 && len(out.Steps) != len(r.Steps) {
		return fmt.Errorf("model returned %d steps for %d", len(out.Steps), len(r.Steps))
	}
	// Model text is redacted again in case it echoed something unmasked.
	clean := func(s string) string { return strings.TrimSpace(redact.Redact(s).Text) }
	if id := session.Slug(out.ID); id != "" {
		r.ID = id
	}
	if t := clean(out.Title); t != "" {
		r.Title = t
	}
	if s := clean(out.Situation); s != "" {
		r.Situation = s
	}
	// Skip preconditions already covered, including fragments of existing ones.
	covered := func(p string) bool {
		lp := strings.ToLower(p)
		for _, q := range r.Preconditions {
			lq := strings.ToLower(q)
			if strings.Contains(lq, lp) || strings.Contains(lp, lq) {
				return true
			}
		}
		return false
	}
	for _, p := range out.Preconditions {
		if p = clean(p); p != "" && !covered(p) {
			r.Preconditions = append(r.Preconditions, p)
		}
	}
	for _, s := range out.Steps {
		if s.N >= 1 && s.N <= len(r.Steps) {
			if t := clean(s.Title); t != "" {
				r.Steps[s.N-1].Title = t
			}
		}
	}
	for _, x := range out.Risk {
		if x.N < 1 || x.N > len(r.Steps) {
			continue
		}
		lvl, ok := map[string]risk.Level{"safe": risk.Safe, "risky": risk.Risky, "destructive": risk.Destructive}[x.Level]
		cur := &r.Steps[x.N-1].Risk
		if ok && lvl > cur.Level { // raise only, never lower (SPEC D-12)
			cur.Level = lvl
			cur.Reasons = append(cur.Reasons, "model: "+clean(x.Why))
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
