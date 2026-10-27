// Package cli wires the recap commands. Business logic lives in other packages
// so the TUI, --json output and the future daemon share one code path.
package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/idestis/recap/internal/config"
	"github.com/idestis/recap/internal/store"
	"github.com/idestis/recap/internal/version"
)

// exitError carries a specific exit code (e.g. 2 for Claude Code hook blocks).
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

var (
	flagJSON   bool
	flagYes    bool
	flagDryRun bool
	flagSpace  string
)

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	cobra.EnableCommandSorting = false // keep workflow order in --help
	root := &cobra.Command{
		Use:           "recap",
		Short:         "recap — fix it once, never figure it out again",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "machine-readable output")
	root.PersistentFlags().BoolVar(&flagYes, "yes", false, "answer yes to prompts (agents, CI)")
	root.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "show what would happen, change nothing")
	root.PersistentFlags().StringVar(&flagSpace, "space", "", "space (recipe repo) to use (env RECAP_SPACE)")

	root.AddGroup(
		&cobra.Group{ID: "setup", Title: "Setup:"},
		&cobra.Group{ID: "session", Title: "Sessions:"},
		&cobra.Group{ID: "recipes", Title: "Recipes:"},
		&cobra.Group{ID: "agents", Title: "Agents and integrations:"},
	)
	add := func(group string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = group
			root.AddCommand(c)
		}
	}
	add("setup", initCmd(), spacesCmd(), shellInitCmd(), hooksCmd(), modelsCmd())
	add("session", startCmd(), joinCmd(), leaveCmd(), statusCmd(), ctxCmd(), timelineCmd(), stopCmd())
	add("recipes", findCmd(), runCmd(), applyCmd(), publishCmd(), pruneCmd())
	add("agents", skillCmd(), mcpCmd(), loginCmd("slack"), loginCmd("github"))
	root.AddCommand(activeCmd(), hookCmd(), versionCmd())

	if err := root.Execute(); err != nil {
		var ee exitError
		if errors.As(err, &ee) {
			return ee.code
		}
		fmt.Fprintln(os.Stderr, red("✗ ")+err.Error())
		return 1
	}
	return 0
}

// ---------- output helpers ----------

var useColor = os.Getenv("NO_COLOR") == "" && isTTY(os.Stdout)

// isTTY is a real terminal check: /dev/null is a character device too.
func isTTY(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

func paint(code, s string) string {
	if !useColor {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}
func red(s string) string    { return paint("31", s) }
func green(s string) string  { return paint("32", s) }
func amber(s string) string  { return paint("33", s) }
func dim(s string) string    { return paint("2", s) }
func bold(s string) string   { return paint("1", s) }
func accent(s string) string { return paint("38;5;209", s) } // Sunrise coral

func ok(format string, a ...any)   { fmt.Println(green("✓ ") + fmt.Sprintf(format, a...)) }
func info(format string, a ...any) { fmt.Println("  " + fmt.Sprintf(format, a...)) }
func note(s string)                { fmt.Println("  " + s) }

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ask prompts y/N. --yes answers yes; no TTY answers no.
func ask(q string) bool {
	if flagYes {
		return true
	}
	if !isTTY(os.Stdin) {
		return false
	}
	fmt.Print(amber("? ") + q + dim(" [y/N] › "))
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

// ---------- shared plumbing ----------

// env is the resolved config for one command: the space decides which recipe
// repo is used and how it publishes (SPEC D-27).
type env struct {
	Conf      *config.Config
	Space     config.Space
	Repo      string
	Guardrail bool
}

func openStore() (*store.Store, env, error) { return openStoreFor("") }

// openStoreFor resolves the space: name (a session's own space) > --space >
// RECAP_SPACE > working directory paths > current.
func openStoreFor(name string) (*store.Store, env, error) {
	st, err := store.Open()
	if err != nil {
		return nil, env{}, err
	}
	c, err := config.Load(st.Home)
	if err != nil {
		return nil, env{}, err
	}
	if name == "" {
		name = flagSpace
	}
	if name == "" {
		name = os.Getenv("RECAP_SPACE")
	}
	if _, ok := c.Space(name); name != "" && !ok && name == os.Getenv("RECAP_SPACE") {
		name = "" // a stale env var should not break every command
	}
	wd, _ := os.Getwd()
	sp, err := c.Resolve(name, wd)
	if err != nil {
		return nil, env{}, err
	}
	return st, env{Conf: c, Space: sp, Repo: sp.Repo, Guardrail: c.GuardrailOn()}, nil
}

func needRepo(cfg env) error {
	if cfg.Repo == "" {
		return errors.New("no recipe repo configured — run: cd <recipe repo> && recap init")
	}
	return nil
}

// repoSettings holds values from <repo>/recap.yaml (tiny line parser for the prototype).
type repoSettings struct {
	ProdContexts       []string
	ApplyKeepsSessions bool
}

var listRe = regexp.MustCompile(`\[(.*)\]`)

func loadRepoSettings(repo string) repoSettings {
	var rs repoSettings
	b, err := os.ReadFile(filepath.Join(repo, "recap.yaml"))
	if err != nil {
		return rs
	}
	for _, l := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "prod_contexts:"):
			if m := listRe.FindStringSubmatch(t); m != nil {
				for _, p := range strings.Split(m[1], ",") {
					if p = strings.Trim(strings.TrimSpace(p), `"'`); p != "" {
						rs.ProdContexts = append(rs.ProdContexts, p)
					}
				}
			}
		case strings.HasPrefix(t, "apply_keeps_sessions:"):
			rs.ApplyKeepsSessions = strings.HasSuffix(t, "true")
		}
	}
	return rs
}

func currentTTY() string {
	if t := os.Getenv("TTY"); t != "" {
		return filepath.Base(t)
	}
	if l, err := os.Readlink("/proc/self/fd/0"); err == nil && strings.HasPrefix(l, "/dev/") {
		return filepath.Base(l)
	}
	return "tab-" + fmt.Sprint(os.Getppid())
}
