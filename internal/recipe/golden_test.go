package recipe

import (
	"bufio"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/idestis/recap/internal/session"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/*/golden")

// golden compares got with testdata/<dir>/golden/<name>, or rewrites it with -update.
func golden(t *testing.T, dir, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", dir, "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/recipe -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from golden\n--- want\n%s\n--- got\n%s", p, want, got)
	}
}

// load reads session.json and events.jsonl from a testdata dir.
func load(t *testing.T, dir string) (*session.Session, []session.Event) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", dir, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var se session.Session
	if err := json.Unmarshal(b, &se); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join("testdata", dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var evs []session.Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var ev session.Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatal(err)
		}
		evs = append(evs, ev)
	}
	return &se, evs
}

// writeFrom drafts a session from testdata and writes its folder into repo.
func writeFrom(t *testing.T, repo, dir string, match *Match, ctxMD string) (*Recipe, string) {
	t.Helper()
	se, evs := load(t, dir)
	r, _ := Draft(se, evs, []string{"prod-eu"})
	folder, err := WriteSession(repo, se, r, match, ctxMD)
	if err != nil {
		t.Fatal(err)
	}
	return r, folder
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func tree(t *testing.T, root string) string {
	t.Helper()
	var out []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return strings.Join(out, "\n") + "\n"
}

// twice runs fn in two fresh repos: output must be byte-identical both times.
func twice(t *testing.T, fn func(t *testing.T)) {
	t.Run("run1", fn)
	t.Run("run2", fn)
}

func TestGoldenDraftRender(t *testing.T) {
	twice(t, func(t *testing.T) {
		se, evs := load(t, "added")
		r, masked := Draft(se, evs, []string{"prod-eu"})
		if masked == 0 {
			t.Error("expected the bearer token to be redacted")
		}
		golden(t, "added", "recipe.md", r.Render())
	})
}

func TestGoldenWriteSessionAddedAndApply(t *testing.T) {
	twice(t, func(t *testing.T) {
		repo := t.TempDir()
		r, dir := writeFrom(t, repo, "added", nil, read(t, "testdata/added/context.md"))
		for _, f := range []string{"session.yaml", "delta.md", "context.md", "recipe.md"} {
			golden(t, "added", "session/"+f, read(t, filepath.Join(dir, f)))
		}
		if got := DeltaTarget(dir); got != r.ID {
			t.Errorf("DeltaTarget = %q, want %q", got, r.ID)
		}
		msg, err := Apply(repo, "2026.03-1", false)
		if err != nil {
			t.Fatal(err)
		}
		golden(t, "added", "apply_msg.txt", msg+"\n")
		golden(t, "added", "applied.md", read(t, filepath.Join(repo, "recipes", r.ID+".md")))
		golden(t, "added", "tree_after_apply.txt", tree(t, repo))
	})
}

func TestGoldenApplyKeepArchives(t *testing.T) {
	twice(t, func(t *testing.T) {
		repo := t.TempDir()
		writeFrom(t, repo, "added", nil, "ctx\n")
		if _, err := Apply(repo, "2026.03-1", true); err != nil {
			t.Fatal(err)
		}
		golden(t, "added", "tree_after_keep.txt", tree(t, repo))
	})
}

// mergeScenario applies base as ADDED, then writes and applies next as a
// MODIFIED delta against it.
func mergeScenario(t *testing.T, c, nextHandle string) {
	repo := t.TempDir()
	base, _ := writeFrom(t, repo, c+"/base", nil, "ctx\n")
	if _, err := Apply(repo, "2026.03-1", false); err != nil {
		t.Fatal(err)
	}
	m := &Match{ID: base.ID, Path: filepath.Join(repo, "recipes", base.ID+".md")}
	_, dir := writeFrom(t, repo, c+"/next", m, "ctx\n")
	for _, f := range []string{"session.yaml", "delta.md"} {
		golden(t, c, "session/"+f, read(t, filepath.Join(dir, f)))
	}
	if _, err := os.Stat(filepath.Join(dir, "recipe.md")); err == nil {
		t.Error("MODIFIED session must not write recipe.md")
	}
	if got := DeltaTarget(dir); got != base.ID {
		t.Errorf("DeltaTarget = %q, want %q", got, base.ID)
	}
	msg, err := Apply(repo, nextHandle, false)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, c, "apply_msg.txt", msg+"\n")
	golden(t, c, "applied.md", read(t, filepath.Join(repo, "recipes", base.ID+".md")))
}

func TestGoldenModifiedMergesVariant(t *testing.T) {
	twice(t, func(t *testing.T) { mergeScenario(t, "modified", "2026.04-1") })
}

func TestGoldenIdenticalStepsOnlyBumpsTrust(t *testing.T) {
	twice(t, func(t *testing.T) {
		mergeScenario(t, "identical", "2026.05-1")
		got := read(t, filepath.Join("testdata", "identical", "golden", "applied.md"))
		if !*update && strings.Contains(got, "## Variants") {
			t.Error("identical steps must not add a variant")
		}
	})
}
