package mcp

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/idestis/recap/internal/recipe"
	"github.com/idestis/recap/internal/session"
	"github.com/idestis/recap/internal/store"
)

var fixedNow = time.Date(2026, 10, 28, 14, 0, 0, 0, time.UTC)

// setup points recap at throwaway dirs and returns the server and recipe repo.
func setup(t *testing.T) (*Server, string) {
	t.Helper()
	home, repo := t.TempDir(), t.TempDir()
	t.Setenv("RECAP_HOME", home)
	t.Setenv("RECAP_REPO", repo)
	t.Setenv("RECAP_SPACE", "")
	t.Setenv("RECAP_SESSION", "")
	return &Server{Dir: t.TempDir(), Now: func() time.Time { return fixedNow }}, repo
}

const oomRecipe = `---
id: fix-oomkilled-pods
title: Fix OOMKilled pods
risk: risky-prod
tools: [kubectl]
trust: { runs: 2, ok: 2 }
verified: [prod-eu 2026-10-22]
vars: [deployment, namespace]
---

# Fix OOMKilled pods

## Situation

Pods out of memory after a deploy.

## Steps

1. List pods — ` + "`safe`" + `

   ` + "```sh" + `
   kubectl get pods -n {namespace}
   ` + "```" + `

2. Raise memory limit — ` + "`risky · prod`" + `

   ` + "```sh" + `
   kubectl set resources deploy/{deployment} -n {namespace} --limits=memory=1Gi
   ` + "```" + `

   Undo: ` + "`kubectl rollout undo deploy/{deployment} -n {namespace}`" + `
`

func writeRecipe(t *testing.T, repo, id, doc string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "recipes", id+".md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListsFiveTools(t *testing.T) {
	s, repo := setup(t)
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	ss, err := s.New().Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	sort.Strings(names)
	want := "add_context get_recipe run_recipe save_recipe search_recipes"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("tools = %s, want %s", got, want)
	}

	// A call over the wire returns structured output.
	writeRecipe(t, repo, "fix-oomkilled-pods", oomRecipe)
	call, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "search_recipes", Arguments: map[string]any{"query": "pods out of memory"}})
	if err != nil || call.IsError {
		t.Fatalf("call: %v %+v", err, call)
	}
	if !strings.Contains(call.Content[0].(*sdk.TextContent).Text, "fix-oomkilled-pods") {
		t.Fatalf("search over the wire: %+v", call.Content)
	}
}

func TestSearchAndGet(t *testing.T) {
	s, repo := setup(t)
	writeRecipe(t, repo, "fix-oomkilled-pods", oomRecipe)
	writeRecipe(t, repo, "fix-oomkilled-jobs", strings.NewReplacer("fix-oomkilled-pods", "fix-oomkilled-jobs", "Fix OOMKilled pods", "Fix OOMKilled jobs").Replace(oomRecipe))
	ctx := context.Background()

	_, out, err := s.SearchRecipes(ctx, nil, SearchIn{Query: "pods out of memory"})
	if err != nil || len(out.Matches) == 0 || out.Matches[0].ID != "fix-oomkilled-pods" {
		t.Fatalf("search: %v %+v", err, out)
	}

	_, got, err := s.GetRecipe(ctx, nil, GetIn{Name: "fix-oomkilled-pods"})
	if err != nil || got.ID != "fix-oomkilled-pods" || got.Markdown != oomRecipe {
		t.Fatalf("get: %v %+v", err, got)
	}

	_, amb, err := s.GetRecipe(ctx, nil, GetIn{Name: "fix oomkilled"})
	if err != nil || amb.Markdown != "" || len(amb.Candidates) != 2 {
		t.Fatalf("ambiguous get: %v %+v", err, amb)
	}

	if _, _, err := s.GetRecipe(ctx, nil, GetIn{Name: "zzz-nothing"}); err == nil {
		t.Fatal("want error for an unknown recipe")
	}
}

func TestRunRecipeIsPreflightOnly(t *testing.T) {
	s, repo := setup(t)
	marker := filepath.Join(t.TempDir(), "ran")
	doc := strings.Replace(oomRecipe, "kubectl get pods -n {namespace}", "touch "+marker+" {namespace}", 1)
	writeRecipe(t, repo, "fix-oomkilled-pods", doc)
	ctx := context.Background()

	_, out, err := s.RunRecipe(ctx, nil, RunIn{Name: "fix-oomkilled-pods", Vars: map[string]string{"namespace": "payments"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Ready || len(out.Preflight.Missing) != 1 || out.Preflight.Missing[0] != "deployment" {
		t.Fatalf("missing vars: %+v", out)
	}

	_, out, err = s.RunRecipe(ctx, nil, RunIn{Name: "fix-oomkilled-pods", Vars: map[string]string{"namespace": "payments", "deployment": "checkout"}})
	if err != nil {
		t.Fatal(err)
	}
	pf := out.Preflight
	if !out.Ready || len(pf.Steps) != 2 {
		t.Fatalf("preflight: %+v", out)
	}
	if pf.Steps[1].Cmd != "kubectl set resources deploy/checkout -n payments --limits=memory=1Gi" ||
		pf.Steps[1].Label != "risky · prod" || pf.Steps[1].Undo != "kubectl rollout undo deploy/checkout -n payments" {
		t.Fatalf("filled step: %+v", pf.Steps[1])
	}
	if out.Command != "recap run fix-oomkilled-pods --var deployment=checkout --var namespace=payments --execute" {
		t.Fatalf("command: %q", out.Command)
	}
	if !strings.Contains(out.Note, "Nothing was run") {
		t.Fatalf("note: %q", out.Note)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("run_recipe executed a step")
	}
	b, _ := os.ReadFile(filepath.Join(repo, "recipes", "fix-oomkilled-pods.md"))
	if string(b) != doc {
		t.Fatal("run_recipe changed the recipe (trust must only move on a real run)")
	}
}

func TestSaveRecipeGates(t *testing.T) {
	s, repo := setup(t)
	ctx := context.Background()
	in := recipe.Proposal{
		Title:     "Rotate leaked deploy token",
		Situation: "Token AKIAIOSFODNN7EXAMPLE leaked in CI logs",
		Steps: []recipe.StepInput{
			{Cmd: "kubectx prod-eu", Risk: "safe"},
			{Title: "Delete the old secret", Cmd: "kubectl delete secret deploy-token -n ci", Risk: "safe"},
			{Cmd: "curl -H 'Authorization: Bearer abcdefghijklmnopqrstuvwxyz' https://ci.example.com/rotate"},
		},
	}
	_, out, err := s.SaveRecipe(ctx, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Masked < 2 || out.VariantOf != "" || out.Steps != 3 || out.ID != "rotate-leaked-deploy-token" {
		t.Fatalf("save: %+v", out)
	}
	// A "safe" hint never lowers the rules: delete in a prod context stays destructive (SPEC D-12).
	if out.Risk != "destructive-prod" {
		t.Fatalf("risk = %s", out.Risk)
	}
	if _, err := os.Stat(filepath.Join(repo, "recipes")); err == nil {
		t.Fatal("save_recipe wrote into recipes/")
	}
	dir := filepath.Join(repo, out.Folder)
	md, err := os.ReadFile(filepath.Join(dir, "recipe.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"AKIAIOSFODNN7EXAMPLE", "abcdefghijklmnopqrstuvwxyz"} {
		if strings.Contains(string(md), secret) {
			t.Fatalf("secret %q reached recipe.md:\n%s", secret, md)
		}
	}
	if !strings.Contains(string(md), "kubectl delete secret deploy-token -n {namespace}") || !strings.Contains(string(md), "`destructive · prod`") {
		t.Fatalf("recipe.md:\n%s", md)
	}
	for _, f := range []string{"delta.md", "session.yaml", "context.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	if !strings.HasPrefix(out.Next, "Ask the user to review") || !strings.Contains(out.Next, "recap apply "+out.Handle) {
		t.Fatalf("next: %q", out.Next)
	}
	// The session is kept locally (stopped) so the handle is taken.
	st, _ := store.Open()
	se, err := st.LoadSession(out.Handle)
	if err != nil || se.Active() || st.Active() != "" {
		t.Fatalf("local session: %v %+v active=%q", err, se, st.Active())
	}
	_, out2, err := s.SaveRecipe(ctx, nil, recipe.Proposal{Title: "Other", Steps: []recipe.StepInput{{Cmd: "make test"}}})
	if err != nil || out2.Handle == out.Handle {
		t.Fatalf("second save: %v %+v", err, out2)
	}
}

func TestSaveRecipeDedupsIntoVariant(t *testing.T) {
	s, repo := setup(t)
	writeRecipe(t, repo, "fix-oomkilled-pods", oomRecipe)
	md := "---\nid: oom-pods-again\ntitle: Fix OOMKilled pods in checkout\n---\n\n## Situation\n\nPods OOMKilled after deploy, out of memory.\n\n## Preconditions\n\n- Check no migration is running\n\n## Steps\n\n" +
		"1. List pods — `safe`\n\n   ```sh\n   kubectl get pods -n prod-payments\n   ```\n\n" +
		"2. Raise memory limit — `risky · prod`\n\n   ```sh\n   kubectl set resources deploy/checkout -n prod-payments --limits=memory=2Gi\n   ```\n"
	_, out, err := s.SaveRecipe(context.Background(), nil, recipe.Proposal{Markdown: md})
	if err != nil {
		t.Fatal(err)
	}
	if out.VariantOf != "fix-oomkilled-pods" {
		t.Fatalf("want a variant of fix-oomkilled-pods: %+v", out)
	}
	delta, _ := os.ReadFile(filepath.Join(repo, out.Folder, "delta.md"))
	if !strings.HasPrefix(string(delta), "## MODIFIED fix-oomkilled-pods\n") || !strings.Contains(string(delta), "+ precondition: Check no migration is running") {
		t.Fatalf("delta.md:\n%s", delta)
	}
	if _, err := os.Stat(filepath.Join(repo, out.Folder, "recipe.md")); err == nil {
		t.Fatal("a variant must not carry a full recipe.md")
	}
}

func TestSaveRecipeNeedsSteps(t *testing.T) {
	s, _ := setup(t)
	if _, _, err := s.SaveRecipe(context.Background(), nil, recipe.Proposal{Title: "Nothing"}); err == nil {
		t.Fatal("want error without steps")
	}
	if _, _, err := s.SaveRecipe(context.Background(), nil, recipe.Proposal{Markdown: "# Just a title\n"}); err == nil {
		t.Fatal("want error for Markdown without steps")
	}
}

func TestAddContext(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	if _, _, err := s.AddContext(ctx, nil, ContextIn{Text: "a note"}); err == nil {
		t.Fatal("want error without an active session")
	}

	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	se := &session.Session{ID: session.NewULID(fixedNow), Handle: st.NextHandle(fixedNow), Name: "checkout-502s", Description: "checkout 502s", StartedAt: fixedNow, Links: map[string]string{}}
	if err := st.SaveSession(se); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(se.Handle); err != nil {
		t.Fatal(err)
	}

	_, out, err := s.AddContext(ctx, nil, ContextIn{Text: "Decided not to roll back because a migration ran, token ghp_abcdefghijklmnopqrstuvwxyz0123456789 https://acme.slack.com/archives/C07ABCDEF12"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Handle != se.Handle || len(out.Added) != 2 || !out.Added[0].Decision || out.Added[1].LinkType != "slack" {
		t.Fatalf("added: %+v", out)
	}
	if strings.Contains(out.Added[0].Text, "ghp_") {
		t.Fatalf("secret echoed back: %q", out.Added[0].Text)
	}
	evs, _ := st.Events(se.Handle)
	if len(evs) != 2 || evs[0].Source != "mcp" || evs[0].Tags[0] != "decision" {
		t.Fatalf("events: %+v", evs)
	}
	se2, _ := st.LoadSession(se.Handle)
	if se2.Links["slack"] != "C07ABCDEF12" {
		t.Fatalf("slack link: %+v", se2.Links)
	}

	// A stopped session takes no more context.
	se2.StoppedAt = &fixedNow
	_ = st.SaveSession(se2)
	if _, _, err := s.AddContext(ctx, nil, ContextIn{Text: "late note"}); err == nil {
		t.Fatal("want error for a stopped session")
	}
}
