package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/idestis/recap/internal/mcp"
	"github.com/idestis/recap/internal/shell"
)

func shellInitCmd() *cobra.Command {
	var install, status bool
	c := &cobra.Command{
		Use:   "shell-init [zsh|bash]",
		Short: "Print shell integration, or --install it into your rc file",
		Long: `Without the shell integration recap can't record commands: ` + "`recap`" + ` must be a shell
function so start/join can attach the tab (SPEC D-06).

  eval "$(recap shell-init zsh)"     # what your ~/.zshrc runs
  recap shell-init --install         # add that line to ~/.zshrc, ~/.bashrc or fish config once
  recap shell-init --status --json   # is it installed / loaded here?`,
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"zsh", "bash", "fish"},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := shell.Detect()
			if len(args) == 1 {
				name = args[0]
			}
			switch {
			case status:
				st := map[string]any{"shell": name, "rc": shell.RCPath(name), "installed": shell.Installed(name), "loaded": shellLoaded()}
				if flagJSON {
					return printJSON(st)
				}
				if shell.Installed(name) {
					ok("%s loads recap %s", shell.RCPath(name), dim("(this shell: "+map[bool]string{true: "loaded", false: "not loaded — open a new tab"}[shellLoaded()]+")"))
				} else {
					fmt.Println(amber("! ") + "not installed — run: " + bold("recap shell-init --install"))
				}
				return nil
			case install:
				if flagDryRun {
					note(dim("would append to " + shell.RCPath(name) + ": " + shell.Line(name)))
					return nil
				}
				path, added, err := shell.Install(name)
				if err != nil {
					return err
				}
				if flagJSON {
					return printJSON(map[string]any{"shell": name, "rc": path, "added": added})
				}
				if added {
					ok("Added to %s %s", path, dim(shell.Line(name)))
				} else {
					ok("Already in %s", path)
				}
				note(dim("Open a new tab or run ") + bold("exec "+name) + dim(" to load it."))
				return nil
			}
			if len(args) == 0 {
				return errors.New("which shell? recap shell-init zsh|bash|fish (or --install)")
			}
			s, err := shell.Script(name)
			if err != nil {
				return err
			}
			fmt.Print(s)
			return nil
		},
	}
	c.Flags().BoolVar(&install, "install", false, "add the eval line to your shell rc file (once)")
	c.Flags().BoolVar(&status, "status", false, "report whether the integration is installed and loaded")
	return c
}

// shellLoaded is true when this call came through the recap shell function.
func shellLoaded() bool { return os.Getenv("RECAP_WRAPPED") != "" }

// requireShell stops start/join in a terminal that can't record: without the
// shell function nothing gets captured. Offers to install it. The tray app and
// agents (no TTY) pass, as does --no-shell.
func requireShell(skip bool) error {
	if skip || shellLoaded() || !isTTY(os.Stdin) {
		return nil
	}
	name := shell.Detect()
	if !shell.Installed(name) {
		fmt.Println(amber("! ") + "recap can't record commands in this shell yet: the shell integration isn't set up.")
		add := flagYes
		if !add && interactive() {
			var err error
			if add, err = confirm("Add "+shell.Line(name)+" to "+shell.RCPath(name)+"?", true); err != nil {
				return err
			}
		}
		if !add {
			return errors.New("set it up with: recap shell-init --install (or start anyway with --no-shell)")
		}
		path, _, err := shell.Install(name)
		if err != nil {
			return err
		}
		ok("Added to %s", path)
	}
	return fmt.Errorf("this shell hasn't loaded recap yet — run %s (or open a new tab), then run this again", bold("exec "+name))
}

func hooksCmd() *cobra.Command {
	c := &cobra.Command{Use: "hooks", Short: "Manage Claude Code hooks"}
	var project bool
	install := &cobra.Command{
		Use:   "install",
		Short: "Add PreToolUse, PostToolUse and Stop hooks for Claude Code",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")
			if project {
				path = filepath.Join(".claude", "settings.json")
			}
			settings := map[string]any{}
			if b, err := os.ReadFile(path); err == nil {
				if err := json.Unmarshal(b, &settings); err != nil {
					return fmt.Errorf("%s is not valid JSON: %w", path, err)
				}
			}
			hooks, _ := settings["hooks"].(map[string]any)
			if hooks == nil {
				hooks = map[string]any{}
			}
			want := map[string][2]string{
				"PreToolUse":  {"Bash", "recap _hook claude-pre"},
				"PostToolUse": {"Bash", "recap _hook claude"},
				"Stop":        {"", "recap _hook claude-stop"},
			}
			for event, mc := range want {
				list, _ := hooks[event].([]any)
				if hasHookCommand(list, mc[1]) {
					continue
				}
				entry := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": mc[1]}}}
				if mc[0] != "" {
					entry["matcher"] = mc[0]
				}
				hooks[event] = append(list, entry)
			}
			settings["hooks"] = hooks
			if flagDryRun {
				return printJSON(settings)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			b, _ := json.MarshalIndent(settings, "", "  ")
			if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
				return err
			}
			ok("Claude Code hooks added %s", dim("PreToolUse · PostToolUse · Stop → "+path))
			return nil
		},
	}
	install.Flags().BoolVar(&project, "project", false, "write ./.claude/settings.json instead of the user settings")
	c.AddCommand(install)
	return c
}

func hasHookCommand(list []any, command string) bool {
	for _, e := range list {
		m, _ := e.(map[string]any)
		hs, _ := m["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if hm["command"] == command {
				return true
			}
		}
	}
	return false
}

func mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve recipes to AI agents over MCP (stdio)",
		Long: `Run the MCP server on stdin/stdout (SPEC D-17). Tools: search_recipes, get_recipe,
run_recipe (preflight only, never executes), save_recipe (redaction, risk rules and
dedup, written as a session folder for review), add_context (active session).

  claude mcp add recap -- recap mcp                 # register with Claude Code
  claude mcp add recap -- recap mcp --space team    # pin a space`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if isTTY(os.Stdin) {
				fmt.Fprintln(os.Stderr, dim("recap mcp speaks MCP on stdin/stdout; register it with: claude mcp add recap -- recap mcp"))
			}
			wd, _ := os.Getwd()
			s := &mcp.Server{Space: flagSpace, Dir: wd}
			return s.Run(cmd.Context())
		},
	}
}

func loginCmd(name string) *cobra.Command {
	phase := map[string]string{"slack": "v0.2 (SPEC D-19)", "github": "v0.2"}[name]
	c := &cobra.Command{Use: name, Short: "Connect " + name + " (" + phase + ")"}
	for _, sub := range []string{"login", "status", "logout"} {
		sub := sub
		c.AddCommand(&cobra.Command{
			Use:   sub,
			Short: sub + " " + name,
			RunE: func(cmd *cobra.Command, args []string) error {
				fmt.Printf("%s recap %s %s is planned for %s.\n", amber("◌"), name, sub, phase)
				if name == "slack" {
					note("Until then, link a channel with: " + bold("recap ctx https://<ws>.slack.com/archives/C…"))
				}
				return nil
			},
		})
	}
	return c
}
