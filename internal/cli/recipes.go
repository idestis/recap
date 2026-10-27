package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/idestis/recap/internal/publish"
	"github.com/idestis/recap/internal/recipe"
	"github.com/idestis/recap/internal/risk"
	"github.com/idestis/recap/internal/session"
	"github.com/idestis/recap/internal/store"
)

func findCmd() *cobra.Command {
	return &cobra.Command{
		Use:   `find "<symptom>"`,
		Short: "Search recipes by meaning, with trust and risk",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := openStore()
			if err != nil {
				return err
			}
			if err := needRepo(cfg); err != nil {
				return err
			}
			res, err := recipe.Find(cfg.Repo, strings.Join(args, " "))
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(res)
			}
			if len(res) == 0 {
				fmt.Println(dim("  no matching recipes yet"))
				return nil
			}
			for _, m := range res {
				fmt.Printf("  %-14s %-34s %-22s %s\n", accent(m.ID), m.Title, dim(m.Trust), amber(m.Risk))
			}
			return nil
		},
	}
}

var (
	stepLineRe = regexp.MustCompile(`^\d+\.\s+(.+?)\s+—\s+(.+)$`)
	codeRe     = regexp.MustCompile("`([^`]+)`")
	varRe      = regexp.MustCompile(`\{([a-z_]+)\}`)
)

type runStep struct{ title, label, cmd, undo string }

func parseSteps(doc string) []runStep {
	_, body, ok := strings.Cut(doc, "## Steps\n")
	body = strings.TrimLeft(body, "\n")
	if !ok {
		return nil
	}
	body, _, _ = strings.Cut(body, "\n## ")
	var out []runStep
	inFence := false
	for _, l := range strings.Split(body, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			if len(out) > 0 && out[len(out)-1].cmd == "" && t != "" {
				out[len(out)-1].cmd = t
			}
			continue
		}
		if m := stepLineRe.FindStringSubmatch(t); m != nil {
			out = append(out, runStep{title: m[1], label: strings.Trim(m[2], "`")})
			continue
		}
		if len(out) == 0 {
			continue
		}
		cur := &out[len(out)-1]
		if strings.HasPrefix(t, "Undo:") {
			if m := codeRe.FindStringSubmatch(t); m != nil {
				cur.undo = m[1]
			}
		} else if m := codeRe.FindStringSubmatch(t); m != nil && cur.cmd == "" {
			cur.cmd = m[1] // older recipes: inline `command`
		}
	}
	return out
}

func runCmd() *cobra.Command {
	var vars []string
	var execute bool
	c := &cobra.Command{
		Use:   "run <recipe name> [--var k=v]",
		Short: "Preflight, then run; asks for missing values; risky steps ask first",
		Long: `Run a recipe by ID, part of its name or title, or a symptom ("pods out of memory").
Missing {variables} are asked for in a terminal; elsewhere pass --var name=value.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := openStore()
			if err != nil {
				return err
			}
			if err := needRepo(cfg); err != nil {
				return err
			}
			name := strings.Join(args, " ")
			m, err := resolveRecipe(cfg.Repo, name)
			if err != nil {
				return err
			}
			path := m.Path
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			values := map[string]string{}
			for _, kv := range vars {
				k, v, _ := strings.Cut(kv, "=")
				values[k] = v
			}
			steps := parseSteps(string(b))
			if err := askVars(steps, values); err != nil {
				return err
			}
			fill := func(s string) (string, []string) {
				var missing []string
				out := varRe.ReplaceAllStringFunc(s, func(m string) string {
					k := m[1 : len(m)-1]
					if v, ok := values[k]; ok {
						return v
					}
					missing = append(missing, k)
					return m
				})
				return out, missing
			}
			fmt.Println(bold("Preflight") + dim("  "+m.ID))
			var missingAll []string
			for i, s := range steps {
				c, miss := fill(s.cmd)
				missingAll = append(missingAll, miss...)
				lbl := green(s.label)
				if s.label != "safe" {
					lbl = amber(s.label)
				}
				fmt.Printf("  %d  %-32s %s\n     %s\n", i+1, s.title, lbl, dim(c))
				if s.undo != "" {
					u, _ := fill(s.undo)
					fmt.Printf("     %s %s\n", dim("undo"), dim(u))
				}
			}
			if len(missingAll) > 0 {
				return fmt.Errorf("missing values: %s (pass --var name=value)", strings.Join(uniq(missingAll), ", "))
			}
			if !execute || flagDryRun {
				fmt.Println(dim("\n  Preflight only. Add --execute to run (prototype safety default)."))
				return nil
			}
			allOK := true
			for i, s := range steps {
				c, _ := fill(s.cmd)
				if s.label != "safe" && !ask(fmt.Sprintf("%d  %s %s Run it?", i+1, s.title, amber("["+s.label+"]"))) {
					fmt.Println(dim("  skipped; stopping here"))
					allOK = false
					break
				}
				x := exec.Command("bash", "-c", c)
				x.Stdout, x.Stderr, x.Stdin = os.Stdout, os.Stderr, os.Stdin
				if err := x.Run(); err != nil {
					fmt.Println(red("✗ ") + fmt.Sprintf("%d  %s failed: %v", i+1, s.title, err))
					allOK = false
					break
				}
				ok("%d  %s", i+1, s.title)
			}
			return bumpTrust(path, allOK)
		},
	}
	c.Flags().StringArrayVar(&vars, "var", nil, "variable value, e.g. --var namespace=payments")
	c.Flags().BoolVar(&execute, "execute", false, "actually run the steps (default: preflight only)")
	return c
}

// resolveRecipe picks one recipe for a name; asks when several fit.
func resolveRecipe(repo, name string) (recipe.Match, error) {
	found, err := recipe.Resolve(repo, name)
	if err != nil {
		return recipe.Match{}, err
	}
	switch {
	case len(found) == 0:
		return recipe.Match{}, fmt.Errorf("no recipe matches %q — see: recap find %q", name, name)
	case len(found) == 1:
		return found[0], nil
	}
	if interactive() {
		var opts []option
		for _, m := range found {
			opts = append(opts, option{m.ID, fmt.Sprintf("%-32s %s", m.ID, m.Title)})
		}
		id := choose("Which recipe?", opts, found[0].ID)
		for _, m := range found {
			if m.ID == id {
				return m, nil
			}
		}
	}
	var ids []string
	for _, m := range found {
		ids = append(ids, m.ID)
	}
	return recipe.Match{}, fmt.Errorf("%q matches several recipes: %s", name, strings.Join(ids, ", "))
}

// askVars fills missing {variables} in step order: prompts in a terminal,
// otherwise leaves them for the "missing values" error.
func askVars(steps []runStep, values map[string]string) error {
	if !interactive() {
		return nil
	}
	for i, s := range steps {
		for _, m := range varRe.FindAllStringSubmatch(s.cmd+" "+s.undo, -1) {
			k := m[1]
			if _, done := values[k]; done {
				continue
			}
			v, err := input(k, fmt.Sprintf("step %d: %s", i+1, s.cmd))
			if err != nil {
				return err
			}
			values[k] = v
		}
	}
	return nil
}

var trustRe = regexp.MustCompile(`trust: \{ runs: (\d+), ok: (\d+) \}`)

func bumpTrust(path string, success bool) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	doc := string(b)
	m := trustRe.FindStringSubmatch(doc)
	if m == nil {
		return nil
	}
	runs, _ := strconv.Atoi(m[1])
	okc, _ := strconv.Atoi(m[2])
	runs++
	if success {
		okc++
	}
	doc = strings.Replace(doc, m[0], fmt.Sprintf("trust: { runs: %d, ok: %d }", runs, okc), 1)
	ok("Run recorded %s", dim(fmt.Sprintf("· trust %d/%d", okc, runs)))
	return os.WriteFile(path, []byte(doc), 0o644)
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func applyCmd() *cobra.Command {
	var keep, doPublish bool
	c := &cobra.Command{
		Use:   "apply <handle or name>",
		Short: "Merge a session's delta into recipes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := openStore()
			if err != nil {
				return err
			}
			if err := needRepo(cfg); err != nil {
				return err
			}
			k := keep || loadRepoSettings(cfg.Repo).ApplyKeepsSessions
			if flagDryRun {
				dir, err := recipe.FindSessionFolder(cfg.Repo, args[0])
				if err != nil {
					return err
				}
				b, _ := os.ReadFile(filepath.Join(dir, "delta.md"))
				fmt.Print(string(b))
				return nil
			}
			dir, err := recipe.FindSessionFolder(cfg.Repo, args[0])
			if err != nil {
				return err
			}
			id := recipe.DeltaTarget(dir)
			msg, err := recipe.Apply(cfg.Repo, args[0], k)
			if err != nil {
				return err
			}
			ok("%s", msg)
			if k {
				ok("Session kept in sessions/archive/")
			} else {
				ok("Removed session folder %s", dim("(still in Git history)"))
			}
			if !doPublish || cfg.Space.Publish == "" || cfg.Space.Publish == "none" {
				note(dim("Commit when ready: git -C " + cfg.Repo + " add -A && git commit"))
				return nil
			}
			rel, _ := filepath.Rel(cfg.Repo, dir)
			paths := []string{rel, filepath.Join("recipes", id+".md")}
			if k {
				paths = append(paths, filepath.Join("sessions", "archive", filepath.Base(dir)))
			}
			res, err := publish.New().Publish(cmd.Context(), publish.Request{
				Space: cfg.Space, Paths: paths, Branch: "recap/apply-" + filepath.Base(dir),
				Title: "recap: apply " + id, Body: "Applies session `" + args[0] + "` into `recipes/" + id + ".md`.",
				Handle: args[0], DryRun: flagDryRun,
			})
			if err != nil {
				return err
			}
			printPublish(res)
			return nil
		},
	}
	c.Flags().BoolVar(&keep, "keep", false, "move the session folder to sessions/archive instead of removing it")
	c.Flags().BoolVar(&doPublish, "publish", false, "commit the change per the space strategy (pr | push)")
	return c
}

func pruneCmd() *cobra.Command {
	var older string
	c := &cobra.Command{
		Use:   "prune --older-than 30d",
		Short: "Remove archived sessions (only when you run it)",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := openStore()
			if err != nil {
				return err
			}
			if err := needRepo(cfg); err != nil {
				return err
			}
			days, err := strconv.Atoi(strings.TrimSuffix(older, "d"))
			if err != nil || !strings.HasSuffix(older, "d") {
				return fmt.Errorf("--older-than must look like 30d")
			}
			list, err := recipe.Prune(cfg.Repo, time.Duration(days)*24*time.Hour, true)
			if err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Println(dim("  nothing to prune"))
				return nil
			}
			for _, d := range list {
				info("%s %s", red("−"), filepath.Base(d))
			}
			if flagDryRun || !ask(fmt.Sprintf("Remove %d archived session(s)?", len(list))) {
				return nil
			}
			_, err = recipe.Prune(cfg.Repo, time.Duration(days)*24*time.Hour, false)
			if err == nil {
				ok("Pruned %d", len(list))
			}
			return err
		},
	}
	c.Flags().StringVar(&older, "older-than", "", "age, e.g. 30d")
	_ = c.MarkFlagRequired("older-than")
	return c
}

// ---------- hidden hook handlers ----------

func hookCmd() *cobra.Command {
	c := &cobra.Command{Use: "_hook", Hidden: true}

	var sess, dir, tty string
	var exitCode int
	shellHook := &cobra.Command{
		Use:  "cmd -- <command>",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			line := strings.TrimSpace(strings.Join(args, " "))
			if line == "" || strings.HasPrefix(line, "recap") || strings.HasPrefix(line, "command recap") {
				return nil
			}
			st, _, err := openStore()
			if err != nil {
				return nil // hooks must never break the shell
			}
			se, err := st.LoadSession(sess)
			if err != nil || !se.Active() {
				return nil
			}
			ec := exitCode
			_ = st.AppendEvent(sess, session.Event{Time: time.Now(), Kind: "cmd", Source: "terminal", Text: line, Exit: &ec, Dir: dir, TTY: filepath.Base(tty)})
			return nil
		},
	}
	shellHook.Flags().StringVar(&sess, "session", "", "")
	shellHook.Flags().StringVar(&dir, "dir", "", "")
	shellHook.Flags().StringVar(&tty, "tty", "", "")
	shellHook.Flags().IntVar(&exitCode, "exit", 0, "")

	// Claude Code PostToolUse: JSON on stdin with tool_name and tool_input.command.
	claude := &cobra.Command{
		Use: "claude",
		RunE: func(cmd *cobra.Command, args []string) error {
			in := readHookInput()
			h := os.Getenv("RECAP_SESSION")
			if h == "" || in.command() == "" {
				return nil
			}
			st, _, err := openStore()
			if err != nil {
				return nil
			}
			if se, err := st.LoadSession(h); err == nil && se.Active() {
				_ = st.AppendEvent(h, session.Event{Time: time.Now(), Kind: "cmd", Source: "claude", Text: in.command(), Exit: in.exitCode()})
			}
			return nil
		},
	}

	// Claude Code PreToolUse guardrail: exit 2 blocks the tool and shows stderr to Claude.
	claudePre := &cobra.Command{
		Use: "claude-pre",
		RunE: func(cmd *cobra.Command, args []string) error {
			in := readHookInput()
			command := in.command()
			if command == "" {
				return nil
			}
			_, cfg, err := openStore()
			if err != nil || !cfg.Guardrail {
				return nil
			}
			// The guard checks the context the command will actually hit right
			// now; sessions themselves only use recorded switches.
			ctx := store.KubeContext()
			r := risk.Classify(command, ctx, loadRepoSettings(cfg.Repo).ProdContexts)
			if !r.Prod || r.Level == risk.Safe {
				return nil
			}
			var suggestion string
			if cfg.Repo != "" {
				if res, _ := recipe.Find(cfg.Repo, command); len(res) > 0 {
					suggestion = res[0].ID
				}
			}
			if r.Level == risk.Destructive || suggestion != "" {
				msg := "recap: blocked — " + r.Label() + " command (" + strings.Join(r.Reasons, ", ") + ")."
				if suggestion != "" {
					msg += " Verified recipe exists: " + suggestion + ". Use `recap run " + suggestion + "` instead."
				} else {
					msg += " Ask the user to run it or approve it explicitly."
				}
				fmt.Fprintln(os.Stderr, msg)
				return exitError{code: 2}
			}
			return nil
		},
	}

	claudeStop := &cobra.Command{
		Use: "claude-stop",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = readHookInput() // MVP: read transcript_path at session stop for intent (SPEC D-18)
			return nil
		},
	}
	c.AddCommand(shellHook, claude, claudePre, claudeStop)
	return c
}

type hookInput struct {
	ToolName       string          `json:"tool_name"`
	ToolInput      map[string]any  `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
	TranscriptPath string          `json:"transcript_path"`
}

func readHookInput() hookInput {
	var in hookInput
	if isTTY(os.Stdin) {
		return in
	}
	b, _ := io.ReadAll(io.LimitReader(os.Stdin, 8<<20))
	_ = json.Unmarshal(b, &in)
	return in
}

func (h hookInput) command() string {
	if h.ToolName != "" && h.ToolName != "Bash" {
		return ""
	}
	s, _ := h.ToolInput["command"].(string)
	return strings.TrimSpace(s)
}

func (h hookInput) exitCode() *int {
	var r struct {
		ExitCode *int `json:"exit_code"`
	}
	if json.Unmarshal(h.ToolResponse, &r) == nil {
		return r.ExitCode
	}
	return nil
}

func skillCmd() *cobra.Command {
	var user bool
	c := &cobra.Command{
		Use:   "skill <recipe name>",
		Short: "Turn a recipe into a Claude Code skill",
		Long: `Write a recipe as a Claude Code skill. The skill runs steps through recap run,
so preflight, risk prompts and trust tracking still apply.

  recap skill fix-checkout-api-502s           # ./.claude/skills/<id>/SKILL.md (this project)
  recap skill "pods out of memory" --user     # ~/.claude/skills/<id>/SKILL.md (all projects)`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, err := openStore()
			if err != nil {
				return err
			}
			if err := needRepo(cfg); err != nil {
				return err
			}
			m, err := resolveRecipe(cfg.Repo, strings.Join(args, " "))
			if err != nil {
				return err
			}
			b, err := os.ReadFile(m.Path)
			if err != nil {
				return err
			}
			md, err := recipe.Skill(string(b))
			if err != nil {
				return err
			}
			root := ".claude"
			if user {
				h, _ := os.UserHomeDir()
				root = filepath.Join(h, ".claude")
			}
			path := filepath.Join(root, "skills", m.ID, "SKILL.md")
			if flagDryRun {
				fmt.Print(md)
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]string{"skill": m.ID, "path": path})
			}
			ok("Wrote %s", path)
			note(dim("Claude Code can now use it; it runs steps through recap run, so risk gates still apply."))
			return nil
		},
	}
	c.Flags().BoolVar(&user, "user", false, "write to ~/.claude/skills (all projects) instead of ./.claude/skills")
	return c
}
