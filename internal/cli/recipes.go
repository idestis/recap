package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/idestis/recap/internal/publish"
	"github.com/idestis/recap/internal/recipe"
	"github.com/idestis/recap/internal/risk"
	"github.com/idestis/recap/internal/session"
	"github.com/idestis/recap/internal/store"
	"github.com/idestis/recap/internal/transcript"
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
			b, err := os.ReadFile(m.Path)
			if err != nil {
				return err
			}
			values := map[string]string{}
			for _, kv := range vars {
				k, v, _ := strings.Cut(kv, "=")
				values[k] = v
			}
			if err := askVars(recipe.ParseSteps(string(b)), values); err != nil {
				return err
			}
			pf := recipe.BuildPreflight(m, string(b), values)
			fmt.Println(bold("Preflight") + dim("  "+m.ID))
			for i, s := range pf.Steps {
				lbl := green(s.Label)
				if !s.Safe() {
					lbl = amber(s.Label)
				}
				fmt.Printf("  %d  %-32s %s\n     %s\n", i+1, s.Title, lbl, dim(s.Cmd))
				if s.Undo != "" {
					fmt.Printf("     %s %s\n", dim("undo"), dim(s.Undo))
				}
			}
			if !pf.Ready() {
				return fmt.Errorf("missing values: %s (pass --var name=value)", strings.Join(pf.Missing, ", "))
			}
			if !execute || flagDryRun {
				fmt.Println(dim("\n  Preflight only. Add --execute to run (prototype safety default)."))
				return nil
			}
			allOK := true
			for i, s := range pf.Steps {
				if !s.Safe() && !ask(fmt.Sprintf("%d  %s %s Run it?", i+1, s.Title, amber("["+s.Label+"]"))) {
					fmt.Println(dim("  skipped; stopping here"))
					allOK = false
					break
				}
				x := exec.Command("bash", "-c", s.Cmd)
				x.Stdout, x.Stderr, x.Stdin = os.Stdout, os.Stderr, os.Stdin
				if err := x.Run(); err != nil {
					fmt.Println(red("✗ ") + fmt.Sprintf("%d  %s failed: %v", i+1, s.Title, err))
					allOK = false
					break
				}
				ok("%d  %s", i+1, s.Title)
			}
			okc, runs, err := recipe.BumpTrust(m.Path, allOK)
			if err == nil && runs > 0 {
				ok("Run recorded %s", dim(fmt.Sprintf("· trust %d/%d", okc, runs)))
			}
			return err
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
func askVars(steps []recipe.RunStep, values map[string]string) error {
	if !interactive() {
		return nil
	}
	for _, v := range recipe.MissingVars(steps, values) {
		val, err := input(v.Name, fmt.Sprintf("step %d: %s", v.Step, v.Cmd))
		if err != nil {
			return err
		}
		values[v.Name] = val
	}
	return nil
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
			// Records intent from the transcript (SPEC D-18). Any failure is
			// silent: hooks must never break Claude Code.
			in := readHookInput()
			h := os.Getenv("RECAP_SESSION")
			if h == "" || in.TranscriptPath == "" {
				return nil
			}
			if st, _, err := openStore(); err == nil {
				_, _ = transcript.Record(st, h, in.TranscriptPath)
			}
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
