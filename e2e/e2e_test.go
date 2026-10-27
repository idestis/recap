//go:build e2e

// End-to-end tests: the real recap binary, throwaway RECAP_HOME/HOME/repos, and
// a local Ollama model drafting the recipes. Run with `task test:e2e`
// (RECAP_E2E_MODEL picks the model, default llama3.1:8b). Skipped when Ollama
// isn't reachable.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "recap-e2e-bin")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "recap")
	build := exec.Command("go", "build", "-o", bin, "../cmd/recap")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build recap: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// world is one isolated machine: home, recap store, kubeconfig and a recipe repo.
type world struct {
	t    *testing.T
	root string
	repo string
	env  []string
}

func newWorld(t *testing.T, kubeContext string) *world {
	t.Helper()
	model := os.Getenv("RECAP_E2E_MODEL")
	if model == "" {
		model = "llama3.1:8b"
	}
	if resp, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://localhost:11434/v1/models"); err != nil {
		t.Skip("Ollama not reachable on localhost:11434")
	} else {
		resp.Body.Close()
	}
	root := t.TempDir()
	w := &world{t: t, root: root, repo: filepath.Join(root, "runbooks")}
	gitcfg := filepath.Join(root, "gitconfig")
	must(t, os.WriteFile(gitcfg, nil, 0o644))
	must(t, os.MkdirAll(filepath.Join(root, "kube"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "kube", "config"), []byte("current-context: "+kubeContext+"\n"), 0o644))
	must(t, os.MkdirAll(w.repo, 0o755))
	w.env = append(os.Environ(),
		"HOME="+root, "RECAP_HOME="+filepath.Join(root, "recap"), "KUBECONFIG="+filepath.Join(root, "kube", "config"),
		"NO_COLOR=1", "RECAP_SPACE=", "RECAP_REPO=", "RECAP_SESSION=", "CI=",
		"GIT_CONFIG_GLOBAL="+gitcfg, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=e2e", "GIT_AUTHOR_EMAIL=e2e@example.com", "GIT_COMMITTER_NAME=e2e", "GIT_COMMITTER_EMAIL=e2e@example.com",
	)
	w.ok(w.repo, "init")
	w.ok(w.repo, "models", "set", "--provider", "ollama", "--model", model, "--timeout", "300")
	return w
}

// run executes recap in dir with stdin closed (not a TTY: the tray-app/agent path).
func (w *world) run(dir string, stdin string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = w.env
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (w *world) ok(dir string, args ...string) string {
	w.t.Helper()
	out, err := w.run(dir, "", args...)
	if err != nil {
		w.t.Fatalf("recap %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func (w *world) start(desc string) string {
	w.ok(w.repo, "start", desc)
	return strings.TrimSpace(w.ok(w.repo, "_active"))
}

// cmd records a terminal command exactly like the zsh/bash hook does.
func (w *world) cmd(handle string, exit int, line string) {
	w.ok(w.repo, "_hook", "cmd", "--session", handle, "--exit", fmt.Sprint(exit), "--dir", w.repo, "--tty", "ttys001", "--", line)
}

func (w *world) ctx(text string) {
	w.t.Helper()
	if out, err := w.run(w.repo, text, "ctx", "--source", "overlay", "-"); err != nil {
		w.t.Fatalf("ctx: %v\n%s", err, out)
	}
}

type stopResult struct {
	Handle    string `json:"handle"`
	Fixed     bool   `json:"fixed"`
	Excluded  int    `json:"excluded"`
	Masked    int    `json:"masked"`
	Model     string `json:"model"`
	ModelErr  string `json:"model_error"`
	VariantOf string `json:"variant_of"`
	DraftPath string `json:"draft_path"`
	Recipe    *struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Risk  string `json:"risk"`
		Steps int    `json:"steps"`
	} `json:"recipe"`
}

func (w *world) stop() stopResult {
	w.t.Helper()
	out := w.ok(w.repo, "stop", "--fixed", "--json")
	var r stopResult
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		w.t.Fatalf("stop --json: %v\n%s", err, out)
	}
	if r.Model == "" || r.ModelErr != "" {
		w.t.Fatalf("model drafting did not run cleanly: model=%q err=%q", r.Model, r.ModelErr)
	}
	if r.Recipe == nil {
		w.t.Fatalf("no recipe drafted:\n%s", out)
	}
	return r
}

func (w *world) read(rel string) string {
	w.t.Helper()
	b, err := os.ReadFile(filepath.Join(w.repo, rel))
	if err != nil {
		w.t.Fatal(err)
	}
	return string(b)
}

// assertOrder checks every needle appears in doc, in this order.
func assertOrder(t *testing.T, doc string, needles ...string) {
	t.Helper()
	pos := -1
	for _, n := range needles {
		i := strings.Index(doc, n)
		if i < 0 {
			t.Fatalf("missing %q in:\n%s", n, doc)
		}
		if i < pos {
			t.Fatalf("%q is out of order in:\n%s", n, doc)
		}
		pos = i
	}
}

func frontMatter(doc, key string) string {
	m := regexp.MustCompile(`(?m)^` + key + `: (.*)$`).FindStringSubmatch(doc)
	if m == nil {
		return ""
	}
	return m[1]
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Kubernetes incident: noise, a failed command, a decision note, a dashboard link
// and a pasted secret. Then a second, similar incident becomes a variant.
func TestKubernetesOOMRecipe(t *testing.T) {
	w := newWorld(t, "prod-eu")
	h := w.start("payments api pods OOMKilled after deploy")

	// The context comes from what was recorded, never from the kubeconfig.
	w.cmd(h, 0, "kubectx prod-eu")
	w.cmd(h, 0, "kubectl get pods -n payments")
	w.cmd(h, 0, "ls -la")
	w.cmd(h, 0, "kubectl describe pod payments-api-7d9f8-x2k4p -n payments")
	w.cmd(h, 1, "kubectl logs payments-api-7d9f8-x2k4p -n payments --previus")
	w.cmd(h, 0, "kubectl top pod -n payments")
	w.cmd(h, 0, "kubectl set resources deploy/payments-api -n payments --limits=memory=1Gi")
	w.cmd(h, 0, "kubectl rollout status deploy/payments-api -n payments")
	w.ctx("memory leak suspected in v2.3; do not roll back, migration 0142 already ran")
	w.ctx("https://grafana.example.com/d/payments-memory")
	w.ctx("pasted from CI: GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789")
	// A screenshot is kept as a file in the local store; drafting uses text only (see README).
	png := filepath.Join(w.root, "panel.png")
	must(t, os.WriteFile(png, []byte("\x89PNG\r\n\x1a\n"), 0o644))
	w.ok(w.repo, "ctx", png)

	r := w.stop()
	if r.Excluded != 1 {
		t.Errorf("failed command should be excluded, got %d", r.Excluded)
	}
	if r.Masked == 0 {
		t.Error("the pasted token should be masked")
	}
	if r.Recipe.Steps != 6 {
		t.Errorf("want 6 steps (noise and failed command dropped), got %d", r.Recipe.Steps)
	}
	if r.Recipe.Risk != "risky-prod" {
		t.Errorf("risk %q, want risky-prod (model may not lower it)", r.Recipe.Risk)
	}

	folder := filepath.Dir(r.DraftPath)
	doc, err := os.ReadFile(filepath.Join(folder, "recipe.md"))
	must(t, err)
	recipe := string(doc)
	t.Logf("drafted recipe:\n%s", recipe)
	if strings.Contains(recipe, "ghp_") {
		t.Fatal("secret leaked into the recipe")
	}
	if !strings.HasPrefix(recipe, "---\nid: ") || frontMatter(recipe, "title") == "" {
		t.Fatalf("front matter missing:\n%s", recipe)
	}
	for _, v := range []string{"context", "namespace", "deployment", "pod"} {
		if !strings.Contains(frontMatter(recipe, "vars"), v) {
			t.Errorf("vars %q should include %s", frontMatter(recipe, "vars"), v)
		}
	}
	// Steps keep the order they were run in; the model only rewords them.
	if !strings.Contains(recipe, "verified: [prod-eu ") {
		t.Error("verified should name the recorded context")
	}
	assertOrder(t, recipe,
		"kubectx {context}",
		"kubectl get pods -n {namespace}",
		"kubectl describe pod {pod} -n {namespace}",
		"kubectl top pod -n {namespace}",
		"kubectl set resources deploy/{deployment} -n {namespace} --limits=memory=1Gi",
		"kubectl rollout status deploy/{deployment} -n {namespace}",
	)
	if !strings.Contains(strings.ToLower(recipe), "roll") {
		t.Error("the 'do not roll back' decision should become a precondition")
	}
	if strings.Count(recipe, "```sh") != 6 {
		t.Error("each step should have its own sh block")
	}

	// Apply by session name, not handle.
	name := strings.TrimPrefix(filepath.Base(folder), h+"-")
	w.ok(w.repo, "apply", name)
	id := r.Recipe.ID
	applied := w.read(filepath.Join("recipes", id+".md"))
	if applied != recipe {
		t.Error("applied recipe differs from the reviewed recipe.md")
	}

	// Run by a symptom: no TTY, so missing values are listed, not asked.
	out, err := w.run(w.repo, "", "run", "payments pods OOMKilled")
	if err == nil || !strings.Contains(out, "missing values") {
		t.Fatalf("run without vars should list missing values:\n%s", out)
	}
	for _, v := range []string{"context", "namespace", "pod", "deployment"} {
		if !strings.Contains(out, v) {
			t.Errorf("missing-values message should name %s:\n%s", v, out)
		}
	}
	out = w.ok(w.repo, "run", id, "--var", "context=prod-us", "--var", "namespace=billing", "--var", "pod=billing-api-1", "--var", "deployment=billing-api")
	assertOrder(t, out,
		"kubectx prod-us",
		"kubectl get pods -n billing",
		"kubectl describe pod billing-api-1 -n billing",
		"kubectl top pod -n billing",
		"kubectl set resources deploy/billing-api -n billing --limits=memory=1Gi",
		"undo",
		"kubectl rollout status deploy/billing-api -n billing",
	)

	// The recipe becomes a Claude Code skill that runs through recap.
	skill := w.ok(w.repo, "skill", id, "--dry-run")
	if !strings.Contains(skill, "name: "+id+"\n") || !strings.Contains(skill, "recap run "+id+" --var") {
		t.Fatalf("skill should name the recipe and run it through recap:\n%s", skill)
	}

	// A second, similar incident becomes a variant of the same recipe.
	h2 := w.start("checkout api pods OOMKilled")
	w.cmd(h2, 0, "kubectx prod-eu")
	w.cmd(h2, 0, "kubectl get pods -n checkout")
	w.cmd(h2, 0, "kubectl set resources deploy/checkout-api -n checkout --limits=memory=2Gi")
	w.cmd(h2, 0, "kubectl rollout restart deploy/checkout-api -n checkout")
	r2 := w.stop()
	if r2.VariantOf != id {
		t.Fatalf("second incident should be a variant of %s, got %q", id, r2.VariantOf)
	}
	w.ok(w.repo, "apply", h2)
	merged := w.read(filepath.Join("recipes", id+".md"))
	if !strings.Contains(merged, "## Variants") || !strings.Contains(merged, "--limits=memory=2Gi") {
		t.Fatalf("variant not merged:\n%s", merged)
	}
	// Preflight-only runs don't count; the variant adds one verified run.
	if frontMatter(merged, "trust") != "{ runs: 2, ok: 2 }" {
		t.Errorf("trust after one variant: %q", frontMatter(merged, "trust"))
	}
}

// Local Docker database reset: container IDs become a variable, rm/volume rm are
// destructive, and the order from stop to start is kept.
func TestDockerLocalDBReset(t *testing.T) {
	w := newWorld(t, "dev-local")
	h := w.start("local postgres container corrupted, reset it")
	w.cmd(h, 0, "docker ps -a")
	w.cmd(h, 0, "docker logs 51aa5ed949d0")
	w.cmd(h, 0, "docker stop 51aa5ed949d0")
	w.cmd(h, 0, "docker rm 51aa5ed949d0")
	w.cmd(h, 0, "docker volume rm localdb_data")
	w.cmd(h, 0, "docker compose up -d localdb")
	w.ctx("data is disposable; seed runs automatically on start")

	r := w.stop()
	if !strings.Contains(r.DraftPath, "local-postgres") {
		t.Errorf("unexpected session folder %s", r.DraftPath)
	}
	if r.Recipe.Risk != "destructive" {
		t.Errorf("risk %q, want destructive", r.Recipe.Risk)
	}
	doc, err := os.ReadFile(filepath.Join(filepath.Dir(r.DraftPath), "recipe.md"))
	must(t, err)
	recipe := string(doc)
	t.Logf("drafted recipe:\n%s", recipe)
	if strings.Contains(recipe, "51aa5ed949d0") {
		t.Error("container ID should be a {container} variable")
	}
	assertOrder(t, recipe,
		"docker ps -a",
		"docker logs {container}",
		"docker stop {container}",
		"docker rm {container}",
		"docker volume rm localdb_data",
		"docker compose up -d localdb",
	)
	assertOrder(t, recipe, "docker rm {container}\n", "docker volume rm") // sanity: blocks intact
	if !regexp.MustCompile("docker rm \\{container\\}[\\s\\S]*?`destructive`|`destructive`[\\s\\S]*?docker rm \\{container\\}").MatchString(recipe) {
		t.Error("docker rm should be labeled destructive")
	}

	w.ok(w.repo, "apply", h)
	out := w.ok(w.repo, "run", "postgres reset", "--var", "container=abc123")
	assertOrder(t, out, "docker stop abc123", "docker rm abc123", "docker compose up -d localdb")
}
