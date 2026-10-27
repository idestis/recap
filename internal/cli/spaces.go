package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/idestis/recap/internal/config"
	"github.com/idestis/recap/internal/llm"
	"github.com/idestis/recap/internal/publish"
	"github.com/idestis/recap/internal/recipe"
	"github.com/idestis/recap/internal/version"
)

const defaultRepoYAML = `# recap repo config (https://github.com/idestis/recap)
apply_keeps_sessions: false
archive_retention: forever      # used only by ` + "`recap prune`" + `
incident_channel_pattern: "inc-*"
prod_contexts: [prod, production]
`

// spaceFlags are shared by `init` and `spaces add`.
type spaceFlags struct {
	name, repo, remote, publish, base string
	paths                             []string
}

func (f *spaceFlags) bind(c *cobra.Command) {
	c.Flags().StringVar(&f.repo, "repo", "", "local path of the recipe Git repo (default: current folder)")
	c.Flags().StringVar(&f.remote, "remote", "", "Git URL; cloned into --repo when the folder doesn't exist")
	c.Flags().StringVar(&f.publish, "publish", "", "pr (team repo) | push (private repo) | none")
	c.Flags().StringVar(&f.base, "base", "", "base branch for pr/push (default: the repo's current branch)")
	c.Flags().StringArrayVar(&f.paths, "path", nil, "working directory that selects this space (repeatable)")
}

func initCmd() *cobra.Command {
	var f spaceFlags
	c := &cobra.Command{
		Use:   "init [path]",
		Short: "Make this folder (or path) a recipe repo space; show next steps",
		Long: `Connect a recipe repo as a space. One recap install serves many projects:
each space is a Git repo with its own publish strategy.

  cd ~/runbooks && recap init                                   # this folder, private
  recap init ~/runbooks --publish push                          # a path
  recap init --name team --remote git@github.com:acme/runbooks.git \
             --publish pr --path ~/code/acme                    # clones into ./runbooks`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				if f.repo != "" {
					return errors.New("give the path as an argument or --repo, not both")
				}
				f.repo = args[0]
			}
			sp, err := setupSpace(f)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(sp)
			}
			note("Next:")
			note("  1. Add to ~/.zshrc:  " + bold(`eval "$(recap shell-init zsh)"`))
			note("  2. Model (optional): " + bold("recap models set --provider ollama --model llama3.2"))
			note("  3. Claude Code:      " + bold("recap hooks install"))
			note("  4. Start a session:  " + bold(`recap start "<what's wrong>"`))
			return nil
		},
	}
	c.Flags().StringVar(&f.name, "name", "", "space name (default: repo folder name)")
	f.bind(c)
	return c
}

// setupSpace creates or updates a space: clones or creates the repo, writes
// recap.yaml, and records the space in config.
func setupSpace(f spaceFlags) (*config.Space, error) {
	st, cfg, err := openStore()
	if err != nil {
		return nil, err
	}
	c := cfg.Conf
	var prev config.Space
	if f.name != "" {
		if p, ok := c.Space(f.name); ok {
			prev = *p
		}
	}
	// Like git: the folder you're in, or ./<name> when cloning a remote.
	repo := firstNonEmpty(f.repo, prev.Repo)
	if repo == "" {
		repo, _ = os.Getwd()
		if f.remote != "" && gitOut(repo, "remote", "get-url", "origin") != f.remote {
			repo = filepath.Join(repo, strings.TrimSuffix(filepath.Base(f.remote), ".git"))
		}
	}
	repo, _ = filepath.Abs(config.Expand(repo))
	name := firstNonEmpty(f.name, prev.Name, filepath.Base(repo))

	if _, err := os.Stat(repo); errors.Is(err, os.ErrNotExist) && f.remote != "" {
		if flagDryRun {
			note(dim("would run: git clone " + f.remote + " " + repo))
		} else if out, err := exec.Command("git", "clone", f.remote, repo).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("git clone: %s", strings.TrimSpace(string(out)))
		}
	}
	if !flagDryRun {
		for _, d := range []string{"recipes", "sessions"} {
			if err := os.MkdirAll(filepath.Join(repo, d), 0o755); err != nil {
				return nil, err
			}
		}
		if _, err := os.Stat(filepath.Join(repo, "recap.yaml")); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(filepath.Join(repo, "recap.yaml"), []byte(defaultRepoYAML), 0o644); err != nil {
				return nil, err
			}
		}
		if _, err := os.Stat(filepath.Join(repo, ".git")); errors.Is(err, os.ErrNotExist) {
			_ = exec.Command("git", "init", "-q", repo).Run()
		}
	}
	remote := firstNonEmpty(f.remote, prev.Remote, gitOut(repo, "remote", "get-url", "origin"))
	pub := firstNonEmpty(f.publish, prev.Publish)
	if pub == "" {
		pub = config.PublishNone
		if remote != "" {
			pub = choose("How should recipes reach "+remote+"?", []option{
				{"pr", "Pull request — team repo, reviewed before merge"},
				{"push", "Push — private repo, commit straight to the branch"},
				{"none", "Nothing — I commit myself"},
			}, config.PublishPR)
		}
	}
	if pub != config.PublishPR && pub != config.PublishPush && pub != config.PublishNone {
		return nil, fmt.Errorf("--publish must be pr, push or none")
	}
	paths := prev.Paths
	for _, p := range f.paths {
		p, _ = filepath.Abs(config.Expand(p))
		if !contains(paths, p) {
			paths = append(paths, p)
		}
	}
	sp := config.Space{Name: name, Repo: repo, Remote: remote, Publish: pub, Base: firstNonEmpty(f.base, prev.Base), Paths: paths, Model: prev.Model}
	c.Upsert(sp)
	if flagDryRun {
		return &sp, printJSON(sp)
	}
	if err := c.Save(); err != nil {
		return nil, err
	}
	if !flagJSON {
		ok("Space %s %s", bold(name), dim("→ "+repo))
		note(dim(fmt.Sprintf("publish: %s · config: %s", pub, c.Path())))
		_ = st
	}
	return &sp, nil
}

func spacesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "spaces",
		Aliases: []string{"space"},
		Short:   "List spaces (one per recipe repo); add, use, remove",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := openStore()
			if err != nil {
				return err
			}
			c := cfg.Conf
			if flagJSON {
				list := c.Spaces
				if list == nil {
					list = []config.Space{}
				}
				return printJSON(map[string]any{"current": c.Current, "resolved": cfg.Space.Name, "spaces": list})
			}
			if len(c.Spaces) == 0 {
				fmt.Println(dim("No spaces yet — run: cd <recipe repo> && recap init"))
				return nil
			}
			for _, s := range c.Spaces {
				mark := "  "
				if s.Name == cfg.Space.Name {
					mark = accent("● ")
				}
				fmt.Printf("%s%-12s %-6s %s\n", mark, bold(s.Name), s.Publish, dim(s.Repo))
				for _, p := range s.Paths {
					note(dim("    when in " + p))
				}
			}
			if cfg.Space.Name != c.Current {
				note(dim("● selected by the working directory; default is " + c.Current))
			}
			return nil
		},
	}
	var f spaceFlags
	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Add or update a space",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f.name = args[0]
			sp, err := setupSpace(f)
			if err == nil && flagJSON {
				return printJSON(sp)
			}
			return err
		},
	}
	f.bind(add)
	use := &cobra.Command{
		Use:   "use <name>",
		Short: "Make a space the default",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := openStore()
			if err != nil {
				return err
			}
			if _, found := cfg.Conf.Space(args[0]); !found {
				return fmt.Errorf("no space %q", args[0])
			}
			cfg.Conf.Current = args[0]
			if err := cfg.Conf.Save(); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]string{"current": args[0]})
			}
			ok("Default space %s", bold(args[0]))
			return nil
		},
	}
	rm := &cobra.Command{
		Use:   "rm <name>",
		Short: "Forget a space (the repo folder is not touched)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := openStore()
			if err != nil {
				return err
			}
			if !cfg.Conf.Remove(args[0]) {
				return fmt.Errorf("no space %q", args[0])
			}
			if err := cfg.Conf.Save(); err != nil {
				return err
			}
			ok("Removed space %s %s", args[0], dim("(repo folder kept)"))
			return nil
		},
	}
	c.AddCommand(add, use, rm)
	return c
}

// embedCmd configures the embedding model used for dedup (SPEC D-14).
func embedCmd() *cobra.Command {
	c := &cobra.Command{Use: "embedding", Short: "Embedding model for dedup (optional; falls back to token overlap)"}
	var m config.Model
	set := &cobra.Command{
		Use:   "set",
		Short: "Choose the embedding provider and model",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := openStore()
			if err != nil {
				return err
			}
			if m.Provider != "ollama" && m.Provider != "openai" {
				return errors.New("--provider must be ollama or openai (any compatible API)")
			}
			if m.Name == "" {
				return errors.New("--model is required, e.g. nomic-embed-text")
			}
			cfg.Conf.Embedding = m
			if err := cfg.Conf.Save(); err != nil {
				return err
			}
			ok("Embedding model %s %s", m.Provider, bold(m.Name))
			if m.Provider == "openai" && m.KeyEnv == "" {
				note(dim("Store the API key in the Keychain: ") + bold("recap models key set --embedding"))
			}
			return nil
		},
	}
	set.Flags().StringVar(&m.Provider, "provider", "ollama", "ollama | openai (any OpenAI-compatible API)")
	set.Flags().StringVar(&m.Name, "model", "", "embedding model, e.g. nomic-embed-text or text-embedding-3-small")
	set.Flags().StringVar(&m.BaseURL, "base-url", "", "API base URL (default per provider)")
	set.Flags().StringVar(&m.KeyEnv, "key-env", "", "read the API key from this env var instead of the Keychain")
	set.Flags().IntVar(&m.TimeoutS, "timeout", 0, "request timeout in seconds (default 120)")
	off := &cobra.Command{
		Use:   "off",
		Short: "Turn embedding dedup off (token overlap is used)",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := openStore()
			if err != nil {
				return err
			}
			cfg.Conf.Embedding = config.Model{}
			if err := cfg.Conf.Save(); err != nil {
				return err
			}
			ok("Embedding dedup off")
			return nil
		},
	}
	test := &cobra.Command{
		Use:   "test",
		Short: "Check the endpoint returns an embedding",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := openStore()
			if err != nil {
				return err
			}
			cl, err := llm.New(cfg.Conf.Embedding)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), cl.HTTP.Timeout)
			defer cancel()
			start := time.Now()
			v, err := cl.Embed(ctx, []string{"ping"})
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": true, "model": cl.Model, "dimensions": len(v[0]), "ms": time.Since(start).Milliseconds()})
			}
			ok("%s returned %d dimensions in %s", bold(cl.Model), len(v[0]), time.Since(start).Round(time.Millisecond))
			return nil
		},
	}
	c.AddCommand(set, off, test)
	return c
}

func modelsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "models",
		Short: "Model for drafting: Ollama or any OpenAI-compatible API",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := openStore()
			if err != nil {
				return err
			}
			m := cfg.Conf.ModelFor(cfg.Space)
			if flagJSON {
				return printJSON(map[string]any{"space": cfg.Space.Name, "model": m, "enabled": m.Enabled(), "embedding": cfg.Conf.Embedding, "embedding_enabled": cfg.Conf.Embedding.Enabled()})
			}
			if e := cfg.Conf.Embedding; e.Enabled() {
				defer ok("embeddings %s %s %s", e.Provider, bold(e.Name), dim(firstNonEmpty(e.BaseURL, llm.DefaultBaseURL(e.Provider))))
			} else {
				defer note(dim("dedup uses token overlap; for embeddings: ") + bold("recap models embedding set --provider ollama --model nomic-embed-text"))
			}
			if !m.Enabled() {
				fmt.Println(dim("○ no model — recipes are drafted with templates"))
				note("Local:  " + bold("recap models set --provider ollama --model llama3.2"))
				note("Hosted: " + bold("recap models set --provider openai --model gpt-4.1-mini && recap models key set"))
				return nil
			}
			base := firstNonEmpty(m.BaseURL, llm.DefaultBaseURL(m.Provider))
			ok("%s %s %s", m.Provider, bold(m.Name), dim(base))
			if cfg.Space.Model != nil {
				note(dim("override for space " + cfg.Space.Name))
			}
			return nil
		},
	}
	var m config.Model
	var forSpace bool
	set := &cobra.Command{
		Use:   "set",
		Short: "Choose provider and model (globally, or for the current space with --for-space)",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := openStore()
			if err != nil {
				return err
			}
			if m.Provider != "ollama" && m.Provider != "openai" && m.Provider != "none" {
				return errors.New("--provider must be ollama, openai (any compatible API) or none")
			}
			if forSpace {
				sp, ok := cfg.Conf.Space(cfg.Space.Name)
				if !ok {
					return errors.New("no space to attach the model to")
				}
				mm := m
				sp.Model = &mm
			} else {
				cfg.Conf.Model = m
			}
			if err := cfg.Conf.Save(); err != nil {
				return err
			}
			ok("Model %s %s", m.Provider, bold(m.Name))
			if m.Provider == "openai" && m.KeyEnv == "" {
				note(dim("Store the API key in the Keychain: ") + bold("recap models key set"))
			}
			return nil
		},
	}
	set.Flags().StringVar(&m.Provider, "provider", "ollama", "ollama | openai (any OpenAI-compatible API) | none")
	set.Flags().StringVar(&m.Name, "model", "", "model name, e.g. llama3.2 or gpt-4.1-mini")
	set.Flags().StringVar(&m.BaseURL, "base-url", "", "API base URL (default per provider; e.g. https://openrouter.ai/api/v1)")
	set.Flags().StringVar(&m.KeyEnv, "key-env", "", "read the API key from this env var instead of the Keychain")
	set.Flags().IntVar(&m.TimeoutS, "timeout", 0, "request timeout in seconds (default 120)")
	set.Flags().BoolVar(&forSpace, "for-space", false, "set for the current space only")

	key := &cobra.Command{Use: "key", Short: "Store or remove the API key in the Keychain"}
	var keyEmbedding bool
	key.PersistentFlags().BoolVar(&keyEmbedding, "embedding", false, "use the embedding model's endpoint instead of the drafting model's")
	key.AddCommand(&cobra.Command{
		Use:   "set",
		Short: "Read the API key from stdin or a hidden prompt into the Keychain",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := openStore()
			if err != nil {
				return err
			}
			m := cfg.Conf.ModelFor(cfg.Space)
			if keyEmbedding {
				m = cfg.Conf.Embedding
			}
			var k []byte
			if term.IsTerminal(int(os.Stdin.Fd())) {
				fmt.Print(amber("? ") + "API key for " + llm.KeyAccount(m) + dim(" (hidden) › "))
				k, err = term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Println()
			} else {
				k, err = io.ReadAll(os.Stdin)
			}
			if err != nil {
				return err
			}
			if len(strings.TrimSpace(string(k))) == 0 {
				return errors.New("empty key")
			}
			if err := llm.SetKey(llm.KeyAccount(m), strings.TrimSpace(string(k))); err != nil {
				return err
			}
			ok("Key saved to the Keychain %s", dim("("+llm.KeyAccount(m)+")"))
			return nil
		},
	}, &cobra.Command{
		Use:   "delete",
		Short: "Remove the API key from the Keychain",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := openStore()
			if err != nil {
				return err
			}
			km := cfg.Conf.ModelFor(cfg.Space)
			if keyEmbedding {
				km = cfg.Conf.Embedding
			}
			acct := llm.KeyAccount(km)
			if err := llm.DeleteKey(acct); err != nil {
				return err
			}
			ok("Key removed %s", dim("("+acct+")"))
			return nil
		},
	})
	test := &cobra.Command{
		Use:   "test",
		Short: "Check the model answers with JSON",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := openStore()
			if err != nil {
				return err
			}
			cl, err := llm.New(cfg.Conf.ModelFor(cfg.Space))
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), cl.HTTP.Timeout)
			defer cancel()
			start := time.Now()
			var out struct {
				OK bool `json:"ok"`
			}
			if err := cl.JSON(ctx, `Reply with {"ok": true}`, "ping", &out); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"ok": out.OK, "model": cl.Model, "ms": time.Since(start).Milliseconds()})
			}
			ok("%s answered in %s", bold(cl.Model), time.Since(start).Round(time.Millisecond))
			return nil
		},
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List models the endpoint offers",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := openStore()
			if err != nil {
				return err
			}
			m := cfg.Conf.ModelFor(cfg.Space)
			if m.Name == "" {
				m.Name = "-" // listing needs only the endpoint
			}
			if m.Provider == "" {
				m.Provider = "ollama"
			}
			cl, err := llm.New(m)
			if err != nil {
				return err
			}
			ids, err := cl.Models(cmd.Context())
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(ids)
			}
			for _, id := range ids {
				fmt.Println(id)
			}
			return nil
		},
	}
	c.AddCommand(set, key, test, list, embedCmd())
	return c
}

func publishCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "publish [handle]",
		Short: "Commit a session's recipe files per the space (PR, push, or none)",
		Long: `Publish the session folder (and any recipes it changed) to the space's repo:
  pr    branch recap/<handle>-<name> from the base branch, push, open a PR (gh)
  push  commit to the current branch and push
  none  nothing; commit yourself`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, cfg, err := openStore()
			if err != nil {
				return err
			}
			handle := ""
			if len(args) > 0 {
				handle = args[0]
			} else if last := st.LastStopped(); last != "" {
				handle = last
			} else {
				return errors.New("which session? recap publish <handle>")
			}
			if se, err := st.LoadSession(handle); err == nil && se.Space != "" && flagSpace == "" {
				if _, cfg, err = openStoreFor(se.Space); err != nil {
					return err
				}
			}
			if err := needRepo(cfg); err != nil {
				return err
			}
			res, err := publishSession(cmd.Context(), cfg, handle)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(res)
			}
			printPublish(res)
			return nil
		},
	}
}

// publishSession publishes repo/sessions/<handle>-* plus recipes it touches.
func publishSession(ctx context.Context, cfg env, handle string) (*publish.Result, error) {
	dir, err := recipe.FindSessionFolder(cfg.Repo, handle)
	if err != nil {
		return nil, err
	}
	rel, _ := filepath.Rel(cfg.Repo, dir)
	paths := []string{rel}
	title := "recap: " + strings.TrimPrefix(filepath.Base(dir), handle+"-")
	if b, err := os.ReadFile(filepath.Join(dir, "delta.md")); err == nil {
		first, _, _ := strings.Cut(string(b), "\n")
		if f := strings.Fields(first); len(f) == 3 {
			title = fmt.Sprintf("recap: %s %s", strings.ToLower(f[1]), f[2])
		}
	}
	body := fmt.Sprintf("Session `%s` drafted by recap.\n\nReview `%s/delta.md`. After merge, run `recap apply %s` to fold it into `recipes/`.\n", handle, rel, handle)
	return publish.New().Publish(ctx, publish.Request{
		Space:  cfg.Space,
		Paths:  paths,
		Branch: "recap/" + filepath.Base(dir),
		Title:  title,
		Body:   body,
		Handle: handle,
		DryRun: flagDryRun,
	})
}

func printPublish(res *publish.Result) {
	for _, p := range res.Plan {
		note(dim(p))
	}
	switch {
	case res.URL != "":
		ok("Pull request %s", bold(res.URL))
	case res.Pushed && res.Branch != "":
		ok("Pushed branch %s", bold(res.Branch))
	case res.Pushed:
		ok("Pushed %s", dim(res.Commit))
	case res.Commit != "":
		ok("Committed %s", dim(res.Commit))
	}
	if res.Note != "" {
		note(dim(res.Note))
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagJSON {
				return printJSON(map[string]string{"version": version.Version, "commit": version.Commit})
			}
			fmt.Println("recap " + version.String())
			return nil
		},
	}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func gitOut(dir string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
