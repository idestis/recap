package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/idestis/recap/internal/capture"
	"github.com/idestis/recap/internal/ctxdetect"
	"github.com/idestis/recap/internal/recipe"
	"github.com/idestis/recap/internal/redact"
	"github.com/idestis/recap/internal/risk"
	"github.com/idestis/recap/internal/session"
	"github.com/idestis/recap/internal/store"
)

var slackIDRe = regexp.MustCompile(`^[CG][A-Z0-9]{6,}$`)

func startCmd() *cobra.Command {
	var slack, name, extID string
	var noShell bool
	c := &cobra.Command{
		Use:   `start "<what's wrong>"`,
		Short: "Start recording; this tab joins automatically",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireShell(noShell); err != nil {
				return err
			}
			st, cfg, err := openStore()
			if err != nil {
				return err
			}
			if h := st.Active(); h != "" {
				return fmt.Errorf("session %s is already recording — use `recap join` or `recap stop`", h)
			}
			now := time.Now()
			desc := strings.Join(args, " ")
			se := &session.Session{
				ID:          session.NewULID(now),
				Handle:      st.NextHandle(now),
				Name:        session.Slug(desc),
				NameSource:  "start",
				Description: desc,
				StartedAt:   now,
				Links:       map[string]string{},
				Tabs:        []string{currentTTY()},
				Space:       cfg.Space.Name,
			}
			if slack != "" {
				applySlack(se, slack)
			}
			if extID != "" {
				se.Links["incident"] = extID
			}
			if name != "" {
				se.Name, se.NameSource = session.Slug(name), "user"
			}
			if err := st.SaveSession(se); err != nil {
				return err
			}
			if err := st.SetActive(se.Handle); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(se)
			}
			fmt.Printf("%s Recording  %s  %s\n", red("●"), bold(se.Handle), se.Name)
			var ctxs []string
			if se.Space != "" {
				ctxs = append(ctxs, "space "+se.Space)
			}
			for _, k := range []string{"slack", "slack_name", "incident"} {
				if v := se.Links[k]; v != "" {
					ctxs = append(ctxs, k+" "+v)
				}
			}
			if len(ctxs) > 0 {
				note(dim(strings.Join(ctxs, " · ")))
			}
			note(dim("New tab? run: recap join   ·   add context: recap ctx \"…\""))
			return nil
		},
	}
	c.Flags().StringVar(&slack, "slack", "", "incident Slack channel: ID (C07…), #name or URL")
	c.Flags().StringVar(&name, "name", "", "session name (wins over every other source)")
	c.Flags().StringVar(&extID, "id", "", "external incident ID, e.g. INC-482")
	c.Flags().BoolVar(&noShell, "no-shell", false, "start without shell integration (notes and links only)")
	return c
}

// applySlack links a channel by ID, name or URL. IDs are stored, never names
// alone, because channels get renamed (SPEC D-08).
func applySlack(se *session.Session, v string) {
	if items := ctxdetect.Detect(v); len(items) == 1 && items[0].Ref != "" {
		v = items[0].Ref
	}
	if slackIDRe.MatchString(v) {
		se.Links["slack"] = v
		return
	}
	se.Links["slack_name"] = strings.TrimPrefix(v, "#")
	name, inc := session.SlackChannelName(v)
	if inc != "" && se.Links["incident"] == "" {
		se.Links["incident"] = inc
	}
	if name != "" && session.CanRename(se.NameSource, "slack") {
		se.Name, se.NameSource = name, "slack"
	}
}

func resolveHandle(st *store.Store, args []string) (string, error) {
	if len(args) > 0 && args[0] != "" {
		return args[0], nil
	}
	return capture.ActiveHandle(st)
}

func joinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "join [handle]",
		Short: "Attach this tab to the active (or named) session",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireShell(false); err != nil {
				return err
			}
			st, _, err := openStore()
			if err != nil {
				return err
			}
			h := st.Active()
			if len(args) == 1 {
				h = args[0]
			}
			if h == "" {
				return errors.New("no active session to join")
			}
			se, err := st.LoadSession(h)
			if err != nil {
				return err
			}
			if !se.Active() {
				return fmt.Errorf("session %s has stopped", h)
			}
			tty := currentTTY()
			if !contains(se.Tabs, tty) {
				se.Tabs = append(se.Tabs, tty)
				if err := st.SaveSession(se); err != nil {
					return err
				}
			}
			fmt.Printf("%s Joined %s %s %s\n", red("●"), bold(se.Handle), se.Name, dim("("+tty+")"))
			return nil
		},
	}
}

func leaveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "leave",
		Short: "Detach this tab; recording continues elsewhere",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, _, err := openStore()
			if err != nil {
				return err
			}
			h, err := resolveHandle(st, nil)
			if err != nil {
				return err
			}
			se, err := st.LoadSession(h)
			if err != nil {
				return err
			}
			tty := currentTTY()
			var keep []string
			for _, t := range se.Tabs {
				if t != tty {
					keep = append(keep, t)
				}
			}
			se.Tabs = keep
			if err := st.SaveSession(se); err != nil {
				return err
			}
			ok("Left %s %s", se.Handle, dim("(this tab no longer records)"))
			return nil
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the active session, attached tabs and sources",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, cfg, err := openStore()
			if err != nil {
				return err
			}
			h := st.Active()
			if h == "" {
				if flagJSON {
					return printJSON(map[string]any{"active": false, "space": cfg.Space.Name})
				}
				fmt.Println(dim("○ idle — no session recording"))
				return nil
			}
			se, err := st.LoadSession(h)
			if err != nil {
				return err
			}
			evs, _ := st.Events(h)
			counts := map[string]int{}
			for _, e := range evs {
				counts[e.Source]++
			}
			if flagJSON {
				return printJSON(map[string]any{"active": true, "session": se, "counts": counts, "elapsed": time.Since(se.StartedAt).Round(time.Second).String(), "space": se.Space})
			}
			fmt.Printf("%s %s  %s  %s\n", red("●"), bold(se.Handle), se.Name, dim(clock(time.Since(se.StartedAt))))
			if se.Space != "" {
				info("space    %s", se.Space)
			}
			info("tabs     %s", strings.Join(se.Tabs, " · "))
			info("captured terminal %d · claude %d · ctx %d", counts["terminal"], counts["claude"], counts["ctx"]+counts["overlay"])
			if len(se.Links) > 0 {
				var l []string
				for k, v := range se.Links {
					l = append(l, k+" "+v)
				}
				info("links    %s", strings.Join(l, " · "))
			}
			return nil
		},
	}
}

func clock(d time.Duration) string {
	d = d.Round(time.Second)
	return fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

func ctxCmd() *cobra.Command {
	var source string
	c := &cobra.Command{
		Use:   "ctx <text | link | file>",
		Short: "Add anything: a note, a link, a file (also reads stdin)",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, _, err := openStore()
			if err != nil {
				return err
			}
			h, err := resolveHandle(st, nil)
			if err != nil {
				return err
			}
			input := strings.Join(args, " ")
			if input == "" || input == "-" {
				b, err := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
				if err != nil {
					return err
				}
				input = string(b)
			}
			added, err := capture.Add(st, h, input, source)
			for _, it := range added {
				switch {
				case it.Kind == "file":
					ok("File attached %s", dim(filepath.Base(it.Text)+" · kept locally, never in Git"))
				case it.LinkType == "slack":
					ok("Slack channel linked %s", dim(it.Ref+" · messages pulled for the whole session at stop"))
				case it.Kind == "link":
					ok("%s linked %s", capture.LinkLabel(it.LinkType), dim(it.Text))
				case it.Decision:
					ok("Note added %s", amber("· decision"))
				default:
					ok("Note added")
				}
			}
			return err
		},
	}
	c.Flags().StringVar(&source, "source", "ctx", "who added it: ctx | overlay | mcp")
	return c
}

func timelineCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "timeline [handle]",
		Short: "Everything captured, merged by time (secrets masked)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, cfg, err := openStore()
			if err != nil {
				return err
			}
			h, err := resolveHandle(st, args)
			if err != nil {
				return err
			}
			if _, err := st.LoadSession(h); err != nil {
				return err
			}
			evs, err := st.Events(h)
			if err != nil {
				return err
			}
			for i := range evs {
				evs[i].Text = redact.Redact(evs[i].Text).Text
			}
			if flagJSON {
				return printJSON(evs)
			}
			prod := loadRepoSettings(cfg.Repo).ProdContexts
			ctxs := recipe.Contexts(evs)
			for i, e := range evs {
				src := fmt.Sprintf("%-9s", sourceLabel(e))
				line := e.Text
				tag := ""
				if e.Kind == "cmd" {
					r := risk.Classify(e.Text, ctxs[i], prod)
					if r.Changes() {
						tag = "  " + amber(r.Label())
					}
					if e.Exit != nil && *e.Exit != 0 {
						tag += "  " + red(fmt.Sprintf("exit %d", *e.Exit))
					}
				}
				if hasTag(e.Tags, "decision") {
					tag = "  " + amber("decision")
				}
				fmt.Printf("  %s  %s %s%s\n", dim(e.Time.Format("15:04")), dim(src), line, tag)
			}
			if len(evs) == 0 {
				fmt.Println(dim("  nothing captured yet"))
			}
			return nil
		},
	}
}

func sourceLabel(e session.Event) string {
	switch {
	case e.Kind == "cmd" && e.Source == "claude":
		return "Claude"
	case e.Kind == "cmd":
		return "Terminal"
	case e.Kind == "link":
		return capture.LinkLabel(e.LinkType)
	case e.Kind == "file":
		return "File"
	}
	return "Note"
}

func activeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "_active",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open()
			if err != nil {
				return err
			}
			if h := st.Active(); h != "" {
				fmt.Println(h)
			}
			return nil
		},
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func hasTag(tags []string, t string) bool { return contains(tags, t) }
