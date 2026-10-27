package publish

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idestis/recap/internal/config"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
	return strings.TrimSpace(string(out))
}

// setup returns a clone with one commit on main and its bare origin.
func setup(t *testing.T) (repo, origin string) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// Isolate from the user's git config (commit signing, hooks).
	empty := filepath.Join(t.TempDir(), "gitconfig")
	_ = os.WriteFile(empty, nil, 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	root := t.TempDir()
	origin = filepath.Join(root, "origin.git")
	repo = filepath.Join(root, "repo")
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	git(t, root, "clone", "-q", origin, repo)
	git(t, repo, "switch", "-q", "-c", "main")
	_ = os.WriteFile(filepath.Join(repo, "recap.yaml"), []byte("x: 1\n"), 0o644)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "init")
	git(t, repo, "push", "-q", "-u", "origin", "main")
	return repo, origin
}

func writeSession(t *testing.T, repo string) string {
	rel := filepath.Join("sessions", "2026-10-27.1-oom")
	if err := os.MkdirAll(filepath.Join(repo, rel), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(repo, rel, "delta.md"), []byte("## ADDED oom\n"), 0o644)
	return rel
}

func TestPush(t *testing.T) {
	repo, origin := setup(t)
	rel := writeSession(t, repo)
	_ = os.WriteFile(filepath.Join(repo, "unrelated.txt"), []byte("keep me out"), 0o644)
	res, err := New().Publish(context.Background(), Request{
		Space: config.Space{Repo: repo, Publish: config.PublishPush}, Paths: []string{rel}, Title: "recap: added oom", Handle: "2026-10-27.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pushed {
		t.Fatalf("not pushed: %+v", res)
	}
	files := git(t, origin, "ls-tree", "-r", "--name-only", "main")
	if !strings.Contains(files, "delta.md") || strings.Contains(files, "unrelated.txt") {
		t.Fatalf("origin main has: %s", files)
	}
	if msg := git(t, origin, "log", "-1", "--format=%B", "main"); !strings.Contains(msg, "Recap-Session: 2026-10-27.1") {
		t.Fatalf("missing trailer: %s", msg)
	}
}

func TestPRLeavesCheckoutAlone(t *testing.T) {
	repo, origin := setup(t)
	rel := writeSession(t, repo)
	t.Setenv("PATH", filepath.Dir(mustLook(t, "git"))) // no gh: branch only
	res, err := New().Publish(context.Background(), Request{
		Space: config.Space{Repo: repo, Publish: config.PublishPR, Base: "main"}, Paths: []string{rel},
		Branch: "recap/2026-10-27.1-oom", Title: "recap: added oom",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pushed || res.Branch == "" {
		t.Fatalf("branch not pushed: %+v", res)
	}
	if files := git(t, origin, "ls-tree", "-r", "--name-only", "recap/2026-10-27.1-oom"); !strings.Contains(files, "delta.md") {
		t.Fatalf("branch missing session: %s", files)
	}
	if head := git(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); head != "main" {
		t.Fatalf("user checkout moved to %s", head)
	}
	if _, err := os.Stat(filepath.Join(repo, rel, "delta.md")); err != nil {
		t.Fatal("session folder disappeared from the user's checkout")
	}
	if files := git(t, origin, "ls-tree", "-r", "--name-only", "main"); strings.Contains(files, "delta.md") {
		t.Fatal("pr strategy must not touch main")
	}
}

func TestNone(t *testing.T) {
	res, err := New().Publish(context.Background(), Request{Space: config.Space{Publish: config.PublishNone}})
	if err != nil || res.Pushed {
		t.Fatalf("%+v %v", res, err)
	}
}

func mustLook(t *testing.T, name string) string {
	p, err := exec.LookPath(name)
	if err != nil {
		t.Skip(name + " not installed")
	}
	return p
}
