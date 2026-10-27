// Package publish moves reviewed recap files into Git per the space's strategy
// (SPEC D-27): "pr" opens a branch and pull request for a team repo, "push"
// commits straight to the base branch of a private repo, "none" leaves files
// for the user to commit. Only the given repo-relative paths are staged, so
// raw timelines can never be committed by accident (invariant 5).
package publish

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/idestis/recap/internal/config"
)

type Request struct {
	Space  config.Space
	Paths  []string // repo-relative, e.g. sessions/2026-10-27.1-x, recipes/x.md
	Branch string   // used by "pr"
	Title  string   // commit subject and PR title
	Body   string   // PR body
	Handle string   // adds a Recap-Session trailer
	DryRun bool
}

type Result struct {
	Strategy string   `json:"strategy"`
	Commit   string   `json:"commit,omitempty"`
	Branch   string   `json:"branch,omitempty"`
	URL      string   `json:"url,omitempty"`
	Pushed   bool     `json:"pushed"`
	Plan     []string `json:"plan,omitempty"` // commands, for --dry-run
	Note     string   `json:"note,omitempty"`
}

// Runner executes a command in dir; replaced in tests.
type Runner func(ctx context.Context, dir, name string, args ...string) (string, error)

func execRunner(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return "", fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(out.String()), nil
}

type Publisher struct{ Run Runner }

// New returns a publisher that runs git and gh. Apps launched from Finder get a
// minimal PATH, so the usual Homebrew locations are appended when missing.
func New() *Publisher {
	path := os.Getenv("PATH")
	for _, d := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		if !strings.Contains(":"+path+":", ":"+d+":") {
			path += ":" + d
		}
	}
	_ = os.Setenv("PATH", path)
	return &Publisher{Run: execRunner}
}

func (p *Publisher) Publish(ctx context.Context, r Request) (*Result, error) {
	strategy := r.Space.Publish
	if strategy == "" {
		strategy = config.PublishNone
	}
	res := &Result{Strategy: strategy}
	if strategy == config.PublishNone {
		res.Note = "space publishes nothing; commit when ready"
		return res, nil
	}
	if len(r.Paths) == 0 {
		return nil, errors.New("nothing to publish")
	}
	repo := r.Space.Repo
	git := func(args ...string) (string, error) {
		if r.DryRun {
			res.Plan = append(res.Plan, "git "+strings.Join(args, " "))
			return "", nil
		}
		return p.Run(ctx, repo, "git", args...)
	}
	if _, err := p.Run(ctx, repo, "git", "rev-parse", "--is-inside-work-tree"); err != nil {
		return nil, fmt.Errorf("%s is not a Git repo (git init, or recap spaces add --remote …)", repo)
	}
	msg := r.Title
	if r.Handle != "" {
		msg += "\n\nRecap-Session: " + r.Handle
	}
	base := r.Space.Base
	if base == "" {
		base, _ = p.Run(ctx, repo, "git", "rev-parse", "--abbrev-ref", "HEAD")
	}
	hasRemote := false
	if out, _ := p.Run(ctx, repo, "git", "remote"); strings.Contains(out, "origin") {
		hasRemote = true
	}

	switch strategy {
	case config.PublishPush:
		if err := commit(git, wtPaths(ctx, p, repo, r.Paths, r.DryRun), msg); err != nil {
			return nil, err
		}
		res.Commit, _ = p.Run(ctx, repo, "git", "rev-parse", "--short", "HEAD")
		if !hasRemote {
			res.Note = "no origin remote; committed locally"
			return res, nil
		}
		if _, err := git("push", "origin", "HEAD"); err != nil {
			return res, err
		}
		res.Pushed = true
		return res, nil

	case config.PublishPR:
		if !hasRemote {
			return nil, errors.New("pr strategy needs an origin remote")
		}
		res.Branch = r.Branch
		// Commit in a throwaway worktree on a branch from origin/<base>, so the
		// user's checkout and uncommitted work are never touched.
		wt, err := os.MkdirTemp("", "recap-publish-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(wt)
		if _, err := git("fetch", "origin", base); err != nil {
			return nil, err
		}
		if _, err := git("worktree", "add", "-b", r.Branch, wt, "origin/"+base); err != nil {
			return nil, err
		}
		defer func() { _, _ = git("worktree", "remove", "--force", wt) }()
		if !r.DryRun {
			for _, rel := range r.Paths {
				if err := mirror(filepath.Join(repo, rel), filepath.Join(wt, rel)); err != nil {
					return nil, err
				}
			}
		}
		wgit := func(args ...string) (string, error) {
			if r.DryRun {
				res.Plan = append(res.Plan, "git -C <worktree> "+strings.Join(args, " "))
				return "", nil
			}
			return p.Run(ctx, wt, "git", args...)
		}
		if err := commit(wgit, wtPaths(ctx, p, wt, r.Paths, r.DryRun), msg); err != nil {
			return nil, err
		}
		if !r.DryRun {
			res.Commit, _ = p.Run(ctx, wt, "git", "rev-parse", "--short", "HEAD")
		}
		if _, err := wgit("push", "-u", "origin", r.Branch); err != nil {
			return res, err
		}
		res.Pushed = !r.DryRun
		if r.DryRun {
			res.Plan = append(res.Plan, fmt.Sprintf("gh pr create --base %s --head %s --title %q", base, r.Branch, r.Title))
			return res, nil
		}
		if _, err := exec.LookPath("gh"); err != nil {
			res.Note = "branch pushed; install the GitHub CLI (gh) to open the PR automatically"
			return res, nil
		}
		url, err := p.Run(ctx, repo, "gh", "pr", "create", "--base", base, "--head", r.Branch, "--title", r.Title, "--body", r.Body)
		if err != nil {
			res.Note = "branch pushed; opening the PR failed: " + err.Error()
			return res, nil
		}
		res.URL = lastLine(url)
		return res, nil
	}
	return nil, fmt.Errorf("unknown publish strategy %q (pr | push | none)", strategy)
}

// wtPaths keeps paths that exist on disk or are tracked (removals), because
// git rejects a pathspec that matches nothing.
func wtPaths(ctx context.Context, p *Publisher, dir string, paths []string, dry bool) []string {
	if dry {
		return paths
	}
	var keep []string
	for _, rel := range paths {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			keep = append(keep, rel)
		} else if out, _ := p.Run(ctx, dir, "git", "ls-files", "--", rel); out != "" {
			keep = append(keep, rel)
		}
	}
	return keep
}

// mirror makes dst match src: copies a file or directory tree, or removes dst
// when src no longer exists (an applied session folder).
func mirror(src, dst string) error {
	fi, err := os.Stat(src)
	if errors.Is(err, os.ErrNotExist) {
		return os.RemoveAll(dst)
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return copyFile(src, dst)
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		return copyFile(path, filepath.Join(dst, rel))
	})
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// commit stages only the given paths (adds and removals) and commits them.
func commit(git func(...string) (string, error), paths []string, msg string) error {
	if len(paths) == 0 {
		return errors.New("nothing to publish")
	}
	if _, err := git(append([]string{"add", "-A", "--"}, paths...)...); err != nil {
		return err
	}
	_, err := git(append([]string{"commit", "-m", msg, "--"}, paths...)...)
	return err
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
