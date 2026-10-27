// Package mcp is recap's MCP server over stdio (SPEC D-17). It exposes
// search_recipes, get_recipe, run_recipe, save_recipe and add_context, and
// gives agents the same gates as people: redaction before anything is written
// to Git (D-11), rule-based risk a hint can only raise (D-12), dedup into
// variants (D-14), and no execution: run_recipe returns the preflight and a
// person runs it (D-26, invariant 3).
package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/idestis/recap/internal/capture"
	"github.com/idestis/recap/internal/config"
	"github.com/idestis/recap/internal/recipe"
	"github.com/idestis/recap/internal/redact"
	"github.com/idestis/recap/internal/session"
	"github.com/idestis/recap/internal/store"
	"github.com/idestis/recap/internal/version"
)

// Server holds what the tools need to resolve a space like the CLI does.
type Server struct {
	Space string           // explicit space (--space); empty: RECAP_SPACE, then the working directory
	Dir   string           // working directory used to pick a space by path
	Now   func() time.Time // for tests
}

const instructions = `recap keeps runbook recipes that people fixed once.
Before fixing an incident by hand, call search_recipes with the symptom. Use get_recipe to read one.
run_recipe never executes anything: it returns the preflight (filled commands, risk labels, undo) and the
exact recap run command for the user to run in a terminal. Never run a recipe's risky or destructive
commands yourself.
After a fix, save_recipe proposes a recipe for review (a session folder, never straight into recipes/).
add_context adds a note or link to the session that is recording right now.`

// New builds the MCP server with the five tools.
func (s *Server) New() *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{Name: "recap", Title: "recap", Version: version.String()}, &sdk.ServerOptions{Instructions: instructions})
	sdk.AddTool(srv, &sdk.Tool{
		Name:        "search_recipes",
		Description: "Search the recipe library by symptom or words (e.g. \"pods out of memory\"). Returns matching recipes with risk and trust, best first.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, s.SearchRecipes)
	sdk.AddTool(srv, &sdk.Tool{
		Name:        "get_recipe",
		Description: "Get a recipe's full Markdown by ID, part of its name or title, or a symptom. When several recipes fit, returns the candidates instead; call again with an exact id.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, s.GetRecipe)
	sdk.AddTool(srv, &sdk.Tool{
		Name: "run_recipe",
		Description: "Prepare a recipe run: returns the preflight (commands filled with vars, risk labels, undo) and the `recap run … --execute` command. " +
			"It executes nothing. Show the preflight to the user and have them run the command in their terminal; risky steps ask them before running.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, s.RunRecipe)
	sdk.AddTool(srv, &sdk.Tool{
		Name: "save_recipe",
		Description: "Propose a recipe after a fix, as recipe Markdown or as fields (title, situation, preconditions, steps). " +
			"recap masks secrets, sets risk with its own rules, checks for a similar recipe (proposed as a variant) and writes a session folder for review. " +
			"It never writes into recipes/ directly; the user reviews and runs recap apply or recap publish.",
		Annotations: &sdk.ToolAnnotations{DestructiveHint: boolPtr(false), IdempotentHint: false},
	}, s.SaveRecipe)
	sdk.AddTool(srv, &sdk.Tool{
		Name:        "add_context",
		Description: "Add a note, decision, link (Slack channel, dashboard, PR, call) or local file path to the session that is recording now, like `recap ctx`. Fails when no session is recording.",
		Annotations: &sdk.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, s.AddContext)
	return srv
}

// Run serves MCP on stdin/stdout until the client disconnects.
func (s *Server) Run(ctx context.Context) error {
	return s.New().Run(ctx, &sdk.StdioTransport{})
}

func boolPtr(b bool) *bool { return &b }

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// env opens the store and resolves the space with the CLI's rules.
func (s *Server) env() (*store.Store, config.Space, error) {
	st, err := store.Open()
	if err != nil {
		return nil, config.Space{}, err
	}
	c, err := config.Load(st.Home)
	if err != nil {
		return nil, config.Space{}, err
	}
	dir := s.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	sp, err := c.ResolveSpace(s.Space, dir)
	if err != nil {
		return nil, config.Space{}, err
	}
	if sp.Repo == "" {
		return nil, sp, errors.New("no recipe repo configured — the user needs to run: cd <recipe repo> && recap init")
	}
	return st, sp, nil
}

// ---------- search_recipes ----------

type SearchIn struct {
	Query string `json:"query" jsonschema:"symptom or words to search for, e.g. checkout 502 after deploy"`
}

type Match struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Risk  string  `json:"risk,omitempty"`
	Trust string  `json:"trust,omitempty"`
	Score float64 `json:"score,omitempty"`
}

type SearchOut struct {
	Space   string  `json:"space,omitempty"`
	Matches []Match `json:"matches,omitempty"`
}

func (s *Server) SearchRecipes(_ context.Context, _ *sdk.CallToolRequest, in SearchIn) (*sdk.CallToolResult, SearchOut, error) {
	if strings.TrimSpace(in.Query) == "" {
		return nil, SearchOut{}, errors.New("query is empty")
	}
	_, sp, err := s.env()
	if err != nil {
		return nil, SearchOut{}, err
	}
	res, err := recipe.Find(sp.Repo, in.Query)
	if err != nil {
		return nil, SearchOut{}, err
	}
	if len(res) > 10 {
		res = res[:10]
	}
	return nil, SearchOut{Space: sp.Name, Matches: matches(res)}, nil
}

func matches(in []recipe.Match) []Match {
	var out []Match
	for _, m := range in {
		out = append(out, Match{ID: m.ID, Title: m.Title, Risk: m.Risk, Trust: m.Trust, Score: m.Score})
	}
	return out
}

// resolve finds one recipe, or the candidates when the name is ambiguous.
func resolve(repo, name string) (*recipe.Match, []Match, error) {
	if strings.TrimSpace(name) == "" {
		return nil, nil, errors.New("name is empty")
	}
	found, err := recipe.Resolve(repo, name)
	if err != nil {
		return nil, nil, err
	}
	switch len(found) {
	case 0:
		return nil, nil, fmt.Errorf("no recipe matches %q — try search_recipes", name)
	case 1:
		return &found[0], nil, nil
	}
	return nil, matches(found), nil
}

// ---------- get_recipe ----------

type GetIn struct {
	Name string `json:"name" jsonschema:"recipe id, part of its name or title, or a symptom"`
}

type GetOut struct {
	ID         string  `json:"id,omitempty"`
	Title      string  `json:"title,omitempty"`
	Markdown   string  `json:"markdown,omitempty"`
	Candidates []Match `json:"candidates,omitempty"`
	Note       string  `json:"note,omitempty"`
}

func (s *Server) GetRecipe(_ context.Context, _ *sdk.CallToolRequest, in GetIn) (*sdk.CallToolResult, GetOut, error) {
	_, sp, err := s.env()
	if err != nil {
		return nil, GetOut{}, err
	}
	m, cands, err := resolve(sp.Repo, in.Name)
	if err != nil {
		return nil, GetOut{}, err
	}
	if m == nil {
		return nil, GetOut{Candidates: cands, Note: "several recipes match; call get_recipe again with one id"}, nil
	}
	b, err := os.ReadFile(m.Path)
	if err != nil {
		return nil, GetOut{}, err
	}
	return nil, GetOut{ID: m.ID, Title: m.Title, Markdown: string(b)}, nil
}

// ---------- run_recipe ----------

type RunIn struct {
	Name string            `json:"name" jsonschema:"recipe id, part of its name or title, or a symptom"`
	Vars map[string]string `json:"vars,omitempty" jsonschema:"values for the recipe's {variables}, e.g. {\"namespace\": \"payments\"}"`
}

type RunOut struct {
	Preflight  *recipe.Preflight `json:"preflight,omitempty"`
	Ready      bool              `json:"ready"`
	Command    string            `json:"command,omitempty"`
	Candidates []Match           `json:"candidates,omitempty"`
	Note       string            `json:"note"`
}

func (s *Server) RunRecipe(_ context.Context, _ *sdk.CallToolRequest, in RunIn) (*sdk.CallToolResult, RunOut, error) {
	_, sp, err := s.env()
	if err != nil {
		return nil, RunOut{}, err
	}
	m, cands, err := resolve(sp.Repo, in.Name)
	if err != nil {
		return nil, RunOut{}, err
	}
	if m == nil {
		return nil, RunOut{Candidates: cands, Note: "several recipes match; call run_recipe again with one id"}, nil
	}
	b, err := os.ReadFile(m.Path)
	if err != nil {
		return nil, RunOut{}, err
	}
	values := in.Vars
	if values == nil {
		values = map[string]string{}
	}
	pf := recipe.BuildPreflight(*m, string(b), values)
	out := RunOut{Preflight: &pf, Ready: pf.Ready(), Command: pf.RunCommand(values)}
	switch {
	case !pf.Ready():
		out.Note = "Missing values: " + strings.Join(pf.Missing, ", ") + ". Ask the user for them and call run_recipe again. Nothing was run."
	case pf.Risky():
		out.Note = "Nothing was run. Show this preflight to the user and ask them to run the command in their terminal; recap asks before each risky step. Do not run these commands yourself."
	default:
		out.Note = "Nothing was run. All steps are safe; ask the user to run the command in their terminal (or run it with their approval)."
	}
	return nil, out, nil
}

// ---------- save_recipe ----------

type SaveOut struct {
	Handle    string `json:"handle"`
	Folder    string `json:"folder"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	Risk      string `json:"risk"`
	Steps     int    `json:"steps"`
	Masked    int    `json:"masked_secrets,omitempty"`
	VariantOf string `json:"variant_of,omitempty"`
	Next      string `json:"next"`
}

// SaveRecipe writes a session-style folder like `recap stop` (SPEC D-16): the
// recipe goes through redaction, risk rules and dedup first, and never lands
// straight in recipes/.
func (s *Server) SaveRecipe(_ context.Context, _ *sdk.CallToolRequest, in recipe.Proposal) (*sdk.CallToolResult, SaveOut, error) {
	st, sp, err := s.env()
	if err != nil {
		return nil, SaveOut{}, err
	}
	now := s.now()
	r, masked, err := recipe.FromProposal(in, config.LoadRepo(sp.Repo).ProdContexts, now)
	if err != nil {
		return nil, SaveOut{}, err
	}
	fixed := true
	se := &session.Session{
		ID:          session.NewULID(now),
		Handle:      st.NextHandle(now),
		Name:        r.ID,
		NameSource:  "mcp",
		Description: r.Title,
		StartedAt:   now,
		StoppedAt:   &now,
		Links:       map[string]string{},
		Space:       sp.Name,
		Fixed:       &fixed,
	}
	// Keep the session in the local store so the handle is taken and
	// `recap publish` finds it, like a stopped session.
	if err := st.SaveSession(se); err != nil {
		return nil, SaveOut{}, err
	}
	match, err := recipe.Dedup(sp.Repo, r)
	if err != nil {
		return nil, SaveOut{}, err
	}
	note := []session.Event{{Time: now, Kind: "note", Source: "mcp", Text: "Saved by an agent through MCP (save_recipe); review before applying."}}
	dir, err := recipe.WriteSession(sp.Repo, se, r, match, recipe.ContextMarkdown(se, note))
	if err != nil {
		return nil, SaveOut{}, err
	}
	_ = st.SetLastStopped(se.Handle)
	rel, _ := filepath.Rel(sp.Repo, dir)
	out := SaveOut{Handle: se.Handle, Folder: rel, ID: r.ID, Title: r.Title, Risk: r.RiskLabel(), Steps: len(r.Steps), Masked: masked}
	if match != nil {
		out.VariantOf = match.ID
	}
	next := "recap apply " + se.Handle
	if p := sp.Publish; p != "" && p != config.PublishNone {
		next = "recap publish " + se.Handle
	}
	out.Next = "Ask the user to review " + rel + "/delta.md, then run: " + next
	return nil, out, nil
}

// ---------- add_context ----------

type ContextIn struct {
	Text string `json:"text" jsonschema:"a note or decision, one or more links, or a local file path"`
}

type ContextOut struct {
	Handle string          `json:"handle"`
	Added  []capture.Added `json:"added,omitempty"`
}

func (s *Server) AddContext(_ context.Context, _ *sdk.CallToolRequest, in ContextIn) (*sdk.CallToolResult, ContextOut, error) {
	if strings.TrimSpace(in.Text) == "" {
		return nil, ContextOut{}, errors.New("text is empty")
	}
	st, err := store.Open()
	if err != nil {
		return nil, ContextOut{}, err
	}
	h, err := capture.ActiveHandle(st)
	if err != nil {
		return nil, ContextOut{}, err
	}
	se, err := st.LoadSession(h)
	if err != nil {
		return nil, ContextOut{}, err
	}
	if !se.Active() {
		return nil, ContextOut{}, fmt.Errorf("session %s has stopped: %w", h, capture.ErrNoSession)
	}
	added, err := capture.Add(st, h, in.Text, "mcp")
	if err != nil {
		return nil, ContextOut{}, err
	}
	// Echo back masked: the reply goes to a model (SPEC D-11).
	for i := range added {
		added[i].Text = redact.Redact(added[i].Text).Text
	}
	return nil, ContextOut{Handle: h, Added: added}, nil
}
