// Package recipe drafts recipes from a session, deduplicates them against the
// library, writes session folders (deltas) and applies them into recipes/.
// Recipes are the source of truth; sessions are temporary changes (SPEC D-16).
//
// The prototype drafts with templates. The MVP adds an LLM step behind the
// same Draft function (small JSON-schema tasks, SPEC D-13).
package recipe

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idestis/recap/internal/redact"
	"github.com/idestis/recap/internal/risk"
	"github.com/idestis/recap/internal/session"
)

type Step struct {
	Title string
	Cmd   string
	Risk  risk.Result
	Undo  string
}

type Recipe struct {
	ID            string
	Title         string
	Situation     string
	Preconditions []string
	Steps         []Step
	Vars          []string
	Tools         []string
	Verified      string
}

// RiskLabel is the recipe-level risk: the highest step risk, plus prod.
func (r *Recipe) RiskLabel() string {
	lvl, prod := risk.Safe, false
	for _, s := range r.Steps {
		if s.Risk.Level > lvl {
			lvl = s.Risk.Level
		}
		prod = prod || (s.Risk.Prod && s.Risk.Level > risk.Safe)
	}
	if lvl == risk.Safe {
		return "safe"
	}
	if prod {
		return lvl.String() + "-prod"
	}
	return lvl.String()
}

var trivial = map[string]bool{
	"ls": true, "cd": true, "pwd": true, "clear": true, "echo": true, "cat": true, "less": true,
	"vim": true, "vi": true, "nano": true, "history": true, "exit": true, "which": true, "man": true,
	"recap": true, "code": true, "open": true, "z": true, "j": true,
	"export": true, "unset": true, "set": true, "source": true, "alias": true,
}

var (
	nsRe     = regexp.MustCompile(`(-n|--namespace)([= ])([A-Za-z0-9._-]+)`)
	deployRe = regexp.MustCompile(`\b(deploy|deployment|deployments)/([A-Za-z0-9._-]+)`)
	podRe    = regexp.MustCompile(`\b(pod|pods)\s+([a-z0-9][a-z0-9.-]*-[a-z0-9]+)\b`)
	ctxRe    = regexp.MustCompile(`--context([= ])([A-Za-z0-9._-]+)`)
	// Container IDs change on every run, so they always become a variable.
	dockerRe      = regexp.MustCompile(`^\s*docker\b`)
	containerIDRe = regexp.MustCompile(`\b([0-9a-f]{64}|[0-9a-f]{12})\b`)
)

// Draft builds a recipe from a session's timeline. All text is redacted first.
func Draft(se *session.Session, events []session.Event, prodContexts []string) (*Recipe, int) {
	masked := 0
	r := &Recipe{
		ID:    se.Name,
		Title: titleCase(se.Description),
	}
	var notes []string
	seen := map[string]bool{}
	vars := map[string]bool{}
	tools := map[string]bool{}
	ctxs := Contexts(events)
	lastCtx := ""
	for i, ev := range events {
		red := redact.Redact(ev.Text)
		masked += red.Count
		text := red.Text
		switch ev.Kind {
		case "note":
			if hasTag(ev.Tags, "decision") {
				r.Preconditions = append(r.Preconditions, text)
			} else {
				notes = append(notes, text)
			}
		case "cmd":
			if ev.Exit != nil && *ev.Exit != 0 {
				continue
			}
			f := strings.Fields(text)
			if len(f) == 0 || trivial[f[0]] {
				continue
			}
			p := parametrize(text, vars)
			if seen[p] {
				continue
			}
			seen[p] = true
			res := risk.Classify(text, ctxs[i], prodContexts)
			lastCtx = ctxs[i]
			if res.Tool != "" {
				tools[res.Tool] = true
			}
			r.Steps = append(r.Steps, Step{Title: stepTitle(p), Cmd: p, Risk: res, Undo: undoFor(p)})
		}
	}
	if len(r.Steps) > 12 {
		r.Steps = r.Steps[len(r.Steps)-12:]
	}
	r.Situation = se.Description
	if len(notes) > 0 {
		r.Situation += ". " + strings.Join(notes, ". ")
	}
	r.Verified = verifiedLabel(se, lastCtx)
	r.Vars = keys(vars)
	r.Tools = keys(tools)
	return r, masked
}

func parametrize(cmd string, vars map[string]bool) string {
	if nsRe.MatchString(cmd) {
		vars["namespace"] = true
		cmd = nsRe.ReplaceAllString(cmd, "$1$2{namespace}")
	}
	if deployRe.MatchString(cmd) {
		vars["deployment"] = true
		cmd = deployRe.ReplaceAllString(cmd, "$1/{deployment}")
	}
	if podRe.MatchString(cmd) {
		vars["pod"] = true
		cmd = podRe.ReplaceAllString(cmd, "$1 {pod}")
	}
	if dockerRe.MatchString(cmd) && containerIDRe.MatchString(cmd) {
		vars["container"] = true
		cmd = containerIDRe.ReplaceAllString(cmd, "{container}")
	}
	if c, ok := risk.ContextSwitch(cmd); ok {
		vars["context"] = true
		cmd = strings.Replace(cmd, c, "{context}", 1)
	}
	if ctxRe.MatchString(cmd) {
		vars["context"] = true
		cmd = ctxRe.ReplaceAllString(cmd, "--context$1{context}")
	}
	return cmd
}

func stepTitle(cmd string) string {
	var words []string
	for _, w := range strings.Fields(cmd) {
		if strings.HasPrefix(w, "-") || strings.Contains(w, "=") || strings.Contains(w, "/") || strings.Contains(w, "|") {
			break
		}
		words = append(words, w)
		if len(words) == 3 {
			break
		}
	}
	return strings.Join(words, " ")
}

func undoFor(cmd string) string {
	if (deployRe.MatchString(cmd) || strings.Contains(cmd, "/{deployment}")) && regexp.MustCompile(`kubectl\s+(set|rollout restart|patch|scale|apply)`).MatchString(cmd) {
		ns := ""
		if strings.Contains(cmd, "{namespace}") {
			ns = " -n {namespace}"
		}
		return "kubectl rollout undo deploy/{deployment}" + ns
	}
	if m := regexp.MustCompile(`helm\s+upgrade\s+(\S+)`).FindStringSubmatch(cmd); m != nil {
		return "helm rollback " + m[1]
	}
	return ""
}

// Contexts returns the kube context in effect at each event, from recorded
// switches only (kubectx, kubectl config use-context) — never guessed.
func Contexts(events []session.Event) []string {
	out := make([]string, len(events))
	cur := ""
	for i, ev := range events {
		if ev.Kind == "cmd" && (ev.Exit == nil || *ev.Exit == 0) {
			if c, ok := risk.ContextSwitch(ev.Text); ok {
				cur = c
			}
		}
		out[i] = cur
	}
	return out
}

func verifiedLabel(se *session.Session, env string) string {
	if env == "" {
		env = "local"
	}
	return env + " " + se.StartedAt.Format("2006-01-02")
}

// Render produces deterministic Markdown with front matter.
func (r *Recipe) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nid: %s\ntitle: %s\nrisk: %s\ntools: [%s]\ntrust: { runs: 1, ok: 1 }\nverified: [%s]\nvars: [%s]\n---\n\n",
		r.ID, r.Title, r.RiskLabel(), strings.Join(r.Tools, ", "), r.Verified, strings.Join(r.Vars, ", "))
	fmt.Fprintf(&b, "# %s\n\n## Situation\n\n%s\n\n", r.Title, r.Situation)
	if len(r.Preconditions) > 0 {
		b.WriteString("## Preconditions\n\n")
		for _, p := range r.Preconditions {
			fmt.Fprintf(&b, "- %s\n", p)
		}
		b.WriteString("\n")
	}
	b.WriteString("## Steps\n\n")
	b.WriteString(r.stepsMarkdown())
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// stepsMarkdown renders each command in its own fenced block so it reads (and
// copies) cleanly on GitHub; `recap run` parses the same shape.
func (r *Recipe) stepsMarkdown() string {
	var b strings.Builder
	for i, s := range r.Steps {
		fmt.Fprintf(&b, "%d. %s — `%s`\n\n   ```sh\n   %s\n   ```\n", i+1, s.Title, s.Risk.Label(), s.Cmd)
		if s.Undo != "" && s.Risk.Changes() {
			fmt.Fprintf(&b, "\n   Undo: `%s`\n", s.Undo)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ---------- library: dedup and find ----------

type Match struct {
	ID    string
	Title string
	Risk  string
	Trust string
	Score float64
	Path  string
}

var wordRe = regexp.MustCompile(`[a-z][a-z0-9]{2,}`)
var stop = map[string]bool{"the": true, "and": true, "for": true, "with": true, "from": true, "after": true, "kubectl": false}

func tokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range wordRe.FindAllString(strings.ToLower(s), -1) {
		if !stop[w] {
			out[strings.TrimSuffix(w, "s")] = true
		}
	}
	return out
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// Library lists recipes in repo/recipes.
func Library(repo string) ([]Match, error) {
	files, err := filepath.Glob(filepath.Join(repo, "recipes", "*.md"))
	if err != nil {
		return nil, err
	}
	var out []Match
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		fm := frontMatter(string(b))
		trust := fm["trust"]
		if m := trustNums.FindStringSubmatch(trust); m != nil {
			trust = m[2] + "/" + m[1] + " ok"
		}
		out = append(out, Match{ID: fm["id"], Title: fm["title"], Risk: fm["risk"], Trust: trust, Path: f})
	}
	return out, nil
}

// Dedup finds the closest recipe with the same structured key (tools + prod class)
// and a similar meaning, by token overlap (SPEC D-14).
func Dedup(repo string, r *Recipe) (*Match, error) { return DedupWith(repo, r, DedupOptions{}) }

// DedupWith is Dedup with optional embeddings. The structured key always gates
// first (same tool, never prod with non-prod); embeddings only rank what passes.
// Any embedding problem falls back silently to token overlap.
func DedupWith(repo string, r *Recipe, opt DedupOptions) (*Match, error) {
	lib, err := Library(repo)
	if err != nil {
		return nil, err
	}
	draftText := r.Title + " " + r.Situation + " " + strings.Join(r.Tools, " ") + " " + r.stepsMarkdown()
	draftTokens := tokens(draftText)
	draftProd := strings.HasSuffix(r.RiskLabel(), "-prod")
	var gated []cand
	for i := range lib {
		m := lib[i]
		b, _ := os.ReadFile(m.Path)
		if strings.HasSuffix(m.Risk, "-prod") != draftProd {
			continue // never merge staging fixes into prod recipes, or vice versa
		}
		if !sharesTool(string(b), r.Tools) {
			continue
		}
		gated = append(gated, cand{m: m, text: string(b)})
	}
	if opt.Embedder != nil {
		if best, ok := embedDedup(opt, draftText, gated); ok {
			return best, nil
		}
	}
	var best *Match
	for _, c := range gated {
		m := c.m
		m.Score = jaccard(draftTokens, tokens(c.text))
		if m.Score >= 0.3 && (best == nil || m.Score > best.Score) {
			mm := m
			best = &mm
		}
	}
	return best, nil
}

func sharesTool(doc string, tools []string) bool {
	if len(tools) == 0 {
		return true
	}
	for _, t := range tools {
		if strings.Contains(doc, t) {
			return true
		}
	}
	return false
}

// Find ranks recipes for a free-text symptom.
func Find(repo, query string) ([]Match, error) {
	lib, err := Library(repo)
	if err != nil {
		return nil, err
	}
	q := tokens(query)
	var out []Match
	for _, m := range lib {
		b, _ := os.ReadFile(m.Path)
		m.Score = jaccard(q, tokens(string(b)))
		for k := range q {
			if strings.Contains(strings.ToLower(m.Title), k) {
				m.Score += 0.1
			}
		}
		if m.Score > 0 {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

// Resolve finds recipes by name: exact ID, then ID or title containing the
// words, then the best search matches. One result means it's unambiguous.
func Resolve(repo, name string) ([]Match, error) {
	lib, err := Library(repo)
	if err != nil {
		return nil, err
	}
	slug := session.Slug(name)
	for _, m := range lib {
		if m.ID == name || m.ID == slug {
			return []Match{m}, nil
		}
	}
	var near []Match
	for _, m := range lib {
		if slug != "" && (strings.Contains(m.ID, slug) || strings.Contains(session.Slug(m.Title), slug)) {
			near = append(near, m)
		}
	}
	if len(near) > 0 {
		return near, nil
	}
	found, err := Find(repo, name)
	if err != nil || len(found) == 0 {
		return nil, err
	}
	if len(found) == 1 || found[0].Score >= 2*found[1].Score {
		return found[:1], nil
	}
	if len(found) > 5 {
		found = found[:5]
	}
	return found, nil
}

// ---------- session folders and apply ----------

// SessionFolder is repo/sessions/<handle>-<name>.
func SessionFolder(repo string, se *session.Session) string {
	return filepath.Join(repo, "sessions", se.Handle+"-"+se.Name)
}

// WriteSession writes session.yaml, delta.md and context.md into the repo.
// Raw timelines stay in the local store (invariant 5).
func WriteSession(repo string, se *session.Session, r *Recipe, match *Match, contextMD string) (string, error) {
	dir := SessionFolder(repo, se)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var y strings.Builder
	fmt.Fprintf(&y, "id: %s\nhandle: %s\nname: %s\nstarted: %s\n", se.ID, se.Handle, se.Name, se.StartedAt.Format(time.RFC3339))
	if se.StoppedAt != nil {
		fmt.Fprintf(&y, "stopped: %s\n", se.StoppedAt.Format(time.RFC3339))
	}
	if se.Fixed != nil {
		fmt.Fprintf(&y, "fixed: %v\n", *se.Fixed)
	}
	if len(se.Links) > 0 {
		y.WriteString("links:\n")
		for _, k := range sortedKeys(se.Links) {
			fmt.Fprintf(&y, "  %s: %s\n", k, se.Links[k])
		}
	}
	var d strings.Builder
	if match != nil {
		fmt.Fprintf(&d, "## MODIFIED %s\n", match.ID)
		fmt.Fprintf(&d, "+ variant: %s\n", r.Title)
		for _, p := range r.Preconditions {
			fmt.Fprintf(&d, "+ precondition: %s\n", p)
		}
		fmt.Fprintf(&d, "+ verified: %s\n\n### Variant steps\n%s", r.Verified, r.stepsMarkdown())
	} else {
		// The full recipe goes in recipe.md so its front matter sits at the top
		// of a file and GitHub renders it; delta.md stays a short summary.
		fmt.Fprintf(&d, "## ADDED %s\n\n**%s** · `%s` · %d steps · verified %s\n\nNew recipe `recipes/%s.md`, in full in [recipe.md](recipe.md). Context: [context.md](context.md).\n",
			r.ID, r.Title, r.RiskLabel(), len(r.Steps), r.Verified, r.ID)
	}
	files := map[string]string{"session.yaml": y.String(), "delta.md": d.String(), "context.md": contextMD}
	if match == nil {
		files["recipe.md"] = r.Render()
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// FindSessionFolder locates repo/sessions/<handle>-*.
// FindSessionFolder locates repo/sessions/<handle>-<name> by handle, then by name.
func FindSessionFolder(repo, ref string) (string, error) {
	for _, pat := range []string{ref + "-*", "*-" + ref, "*" + ref + "*"} {
		m, _ := filepath.Glob(filepath.Join(repo, "sessions", pat))
		if len(m) == 1 {
			return m[0], nil
		}
		if len(m) > 1 {
			var names []string
			for _, p := range m {
				names = append(names, filepath.Base(p))
			}
			return "", fmt.Errorf("%q matches several sessions: %s", ref, strings.Join(names, ", "))
		}
	}
	return "", fmt.Errorf("no session folder for %s in %s/sessions", ref, repo)
}

// DeltaTarget returns the recipe ID named in a session folder's delta.md header.
func DeltaTarget(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "delta.md"))
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(string(b), "\n")
	if f := strings.Fields(first); len(f) == 3 {
		return f[2]
	}
	return ""
}

// Apply merges a session's delta into recipes/. With keep, the folder moves
// to sessions/archive; otherwise it is removed (it stays in Git history).
func Apply(repo, handle string, keep bool) (string, error) {
	dir, err := FindSessionFolder(repo, handle)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(dir, "delta.md"))
	if err != nil {
		return "", err
	}
	delta := string(b)
	first, rest, _ := strings.Cut(delta, "\n")
	f := strings.Fields(first)
	if len(f) != 3 || f[0] != "##" {
		return "", fmt.Errorf("delta.md: unexpected header %q", first)
	}
	op, id := f[1], f[2]
	if err := os.MkdirAll(filepath.Join(repo, "recipes"), 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(repo, "recipes", id+".md")
	var msg string
	switch op {
	case "ADDED":
		if _, err := os.Stat(target); err == nil {
			return "", fmt.Errorf("recipes/%s.md already exists; edit delta.md to MODIFIED", id)
		}
		body := strings.TrimLeft(rest, "\n")
		if b, err := os.ReadFile(filepath.Join(dir, "recipe.md")); err == nil {
			body = string(b) // current layout; older sessions carry the recipe in delta.md
		}
		if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
			return "", err
		}
		msg = "Added recipes/" + id + ".md"
	case "MODIFIED":
		cur, err := os.ReadFile(target)
		if err != nil {
			return "", err
		}
		out, err := mergeVariant(string(cur), rest)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(target, []byte(out), 0o644); err != nil {
			return "", err
		}
		msg = "Merged variant into recipes/" + id + ".md"
	default:
		return "", fmt.Errorf("delta.md: unknown operation %q", op)
	}
	if keep {
		arch := filepath.Join(repo, "sessions", "archive")
		if err := os.MkdirAll(arch, 0o755); err != nil {
			return "", err
		}
		err = os.Rename(dir, filepath.Join(arch, filepath.Base(dir)))
	} else {
		err = os.RemoveAll(dir)
	}
	return msg, err
}

var (
	trustRe   = regexp.MustCompile(`trust: \{ runs: (\d+), ok: (\d+) \}`)
	trustNums = regexp.MustCompile(`runs: (\d+), ok: (\d+)`)
)

func mergeVariant(doc, delta string) (string, error) {
	var variant, verified string
	var pre []string
	lines := strings.Split(delta, "\n")
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "+ variant: "):
			variant = strings.TrimPrefix(l, "+ variant: ")
		case strings.HasPrefix(l, "+ precondition: "):
			pre = append(pre, strings.TrimPrefix(l, "+ precondition: "))
		case strings.HasPrefix(l, "+ verified: "):
			verified = strings.TrimPrefix(l, "+ verified: ")
		}
	}
	_, steps, _ := strings.Cut(delta, "### Variant steps\n")
	if m := trustRe.FindStringSubmatch(doc); m != nil {
		runs, _ := strconv.Atoi(m[1])
		ok, _ := strconv.Atoi(m[2])
		doc = strings.Replace(doc, m[0], fmt.Sprintf("trust: { runs: %d, ok: %d }", runs+1, ok+1), 1)
	}
	if verified != "" {
		doc = regexp.MustCompile(`verified: \[(.*)\]`).ReplaceAllStringFunc(doc, func(s string) string {
			inner := strings.TrimSuffix(strings.TrimPrefix(s, "verified: ["), "]")
			if strings.Contains(inner, verified) {
				return s
			}
			if inner == "" {
				return "verified: [" + verified + "]"
			}
			return "verified: [" + verified + ", " + inner + "]"
		})
	}
	for _, p := range pre {
		if strings.Contains(doc, p) {
			continue
		}
		switch {
		case strings.Contains(doc, "## Preconditions\n\n"):
			doc = strings.Replace(doc, "## Preconditions\n\n", "## Preconditions\n\n- "+p+"\n", 1)
		case strings.Contains(doc, "## Preconditions\n"):
			doc = strings.Replace(doc, "## Preconditions\n", "## Preconditions\n- "+p+"\n", 1)
		default:
			doc = strings.Replace(doc, "## Steps\n", "## Preconditions\n\n- "+p+"\n\n## Steps\n", 1)
		}
	}
	if strings.Contains(doc, strings.TrimSpace(steps)) {
		return doc, nil // same steps: only trust, verification and preconditions change
	}
	if !strings.Contains(doc, "## Variants\n") {
		doc = strings.TrimRight(doc, "\n") + "\n\n## Variants\n"
	}
	doc = strings.TrimRight(doc, "\n") + fmt.Sprintf("\n\n### %s\n%s", variant, steps)
	return doc, nil
}

// Prune removes archived sessions older than age. Only ever run by a person (invariant 4).
func Prune(repo string, age time.Duration, dryRun bool) ([]string, error) {
	dirs, _ := filepath.Glob(filepath.Join(repo, "sessions", "archive", "*"))
	cutoff := time.Now().Add(-age)
	var out []string
	for _, d := range dirs {
		base := filepath.Base(d)
		if len(base) < 10 {
			continue
		}
		t, err := time.Parse("2006-01-02", base[:10])
		if err != nil || !t.Before(cutoff) {
			continue
		}
		out = append(out, d)
		if !dryRun {
			if err := os.RemoveAll(d); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

// ---------- helpers ----------

func frontMatter(doc string) map[string]string {
	out := map[string]string{}
	if !strings.HasPrefix(doc, "---\n") {
		return out
	}
	body, _, _ := strings.Cut(doc[4:], "\n---")
	for _, l := range strings.Split(body, "\n") {
		k, v, ok := strings.Cut(l, ":")
		if ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

func titleCase(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "Untitled recipe"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func hasTag(tags []string, t string) bool {
	for _, x := range tags {
		if x == t {
			return true
		}
	}
	return false
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
