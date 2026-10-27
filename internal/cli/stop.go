package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/idestis/recap/internal/draft"
	"github.com/idestis/recap/internal/llm"
	"github.com/idestis/recap/internal/publish"
	"github.com/idestis/recap/internal/recipe"
	"github.com/idestis/recap/internal/redact"
	"github.com/idestis/recap/internal/review"
	"github.com/idestis/recap/internal/risk"
	"github.com/idestis/recap/internal/session"
)

// stopResult is the --json output of `recap stop` (the tray app reads it).
type stopResult struct {
	Handle    string          `json:"handle"`
	Space     string          `json:"space,omitempty"`
	Fixed     bool            `json:"fixed"`
	Kept      int             `json:"kept"`
	Maybe     int             `json:"maybe"`
	Excluded  int             `json:"excluded"`
	Masked    int             `json:"masked"`
	Model     string          `json:"model,omitempty"`
	ModelErr  string          `json:"model_error,omitempty"`
	Recipe    *recipeSummary  `json:"recipe,omitempty"`
	Match     string          `json:"variant_of,omitempty"`
	DraftPath string          `json:"draft_path,omitempty"`
	Publish   *publish.Result `json:"publish,omitempty"`
	Note      string          `json:"note,omitempty"`
}

type recipeSummary struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Risk  string `json:"risk"`
	Steps int    `json:"steps"`
}

func stopCmd() *cobra.Command {
	var fixed, notFixed, doPublish, noModel bool
	c := &cobra.Command{
		Use:   "stop",
		Short: "Review, confirm the fix, draft the recipe, check for duplicates",
		Long: `Stop recording. In a terminal you pick what to keep and answer "Did this fix it?".
Non-interactive: --fixed / --not-fixed (or --yes = fixed), --publish, --json.`,
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
			if !se.Active() {
				return fmt.Errorf("session %s already stopped", h)
			}
			// The session's own space wins, so a stop from another folder still
			// lands in the right repo.
			_, cfg, err := openStoreFor(firstNonEmpty(flagSpace, se.Space))
			if err != nil {
				return err
			}
			if err := needRepo(cfg); err != nil {
				return err
			}
			evs, err := st.Events(h)
			if err != nil {
				return err
			}
			rs := loadRepoSettings(cfg.Repo)
			res := stopResult{Handle: h, Space: cfg.Space.Name}

			// Review: related preselected, maybe shown unticked, failed commands
			// excluded (SPEC D-10). Non-interactive runs keep related only.
			var sorter *llmUse
			if m := cfg.Conf.ModelFor(cfg.Space); m.Enabled() && !noModel {
				if cl, err := llm.New(m); err == nil {
					sorter = &llmUse{client: cl}
				}
			}
			items := reviewItems(cmd.Context(), se, evs, sorter)
			_, nMaybe, nExcluded := review.Counts(items)
			kept := review.Pick(evs, items, review.Related)
			maybe, excluded := nMaybe, nExcluded
			isFixed := fixed || (flagYes && !notFixed)
			if interactive() && !fixed && !notFixed {
				kept, maybe, excluded = reviewInteractive(evs, items, rs.ProdContexts)
				if isFixed, err = confirm("Did this fix it?", true); err != nil {
					return err
				}
			} else if !flagJSON {
				printReviewSummary(kept, maybe, excluded)
			}
			res.Kept, res.Maybe, res.Excluded, res.Fixed = len(kept), maybe, excluded, isFixed

			now := time.Now()
			se.StoppedAt = &now
			se.Fixed = &isFixed
			if flagDryRun {
				if flagJSON {
					return printJSON(res)
				}
				fmt.Println(dim("  --dry-run: nothing saved"))
				return nil
			}
			if err := st.SaveSession(se); err != nil {
				return err
			}
			if st.Active() == h {
				if err := st.SetActive(""); err != nil {
					return err
				}
			}
			_ = st.SetLastStopped(h)
			done := func() error {
				if flagJSON {
					return printJSON(res)
				}
				return nil
			}
			if !isFixed {
				res.Note = "not fixed: timeline kept locally, no recipe drafted"
				if !flagJSON {
					ok("Stopped %s %s", se.Handle, dim("· "+res.Note))
				}
				return done()
			}

			r, masked := recipe.Draft(se, kept, rs.ProdContexts)
			res.Masked = masked
			if len(r.Steps) == 0 {
				cmds := 0
				for _, e := range evs {
					if e.Kind == "cmd" {
						cmds++
					}
				}
				res.Note = "no commands to turn into steps, nothing to draft"
				if cmds == 0 {
					res.Note = "no terminal commands were recorded, nothing to draft"
				}
				if !flagJSON {
					ok("Stopped %s %s", se.Handle, dim("· "+res.Note))
					if cmds == 0 {
						note(dim("Notes and links are kept locally. To record commands, add to ~/.zshrc:"))
						note("  " + bold(`eval "$(recap shell-init zsh)"`) + dim("   then: recap start (or recap join in other tabs)"))
					}
				}
				return done()
			}
			if m := cfg.Conf.ModelFor(cfg.Space); m.Enabled() && !noModel {
				res.Model = m.Name
				if err := enhance(cmd.Context(), m.Name, se, kept, r, cfg); err != nil {
					res.ModelErr = err.Error()
					if !flagJSON {
						fmt.Fprintln(os.Stderr, amber("! ")+"model drafting failed, using template: "+err.Error())
					}
				}
			}
			match, err := recipe.DedupWith(cfg.Repo, r, recipe.EmbeddingOptions(cfg.Conf.Embedding, st.Home))
			if err != nil {
				return err
			}
			dir, err := recipe.WriteSession(cfg.Repo, se, r, match, recipe.ContextMarkdown(se, kept))
			if err != nil {
				return err
			}
			res.DraftPath = filepath.Join(dir, "delta.md")
			res.Recipe = &recipeSummary{ID: r.ID, Title: r.Title, Risk: r.RiskLabel(), Steps: len(r.Steps)}
			if match != nil {
				res.Match = match.ID
			}
			if !flagJSON {
				if masked > 0 {
					ok("Masked %d secret(s) before drafting", masked)
				}
				ok("Draft: %q %s", r.Title, dim("· "+r.RiskLabel()+" · "+fmt.Sprint(len(r.Steps))+" steps"))
				if match != nil {
					info("%s match with %s → proposed as variant", amber(fmt.Sprintf("%.0f%%", match.Score*100)), bold(match.ID))
				}
				rel, _ := filepath.Rel(cfg.Repo, dir)
				ok("Wrote %s %s", rel+"/", dim("in "+cfg.Space.Name))
			}

			// Publish per the space strategy: asked in a terminal, --publish otherwise.
			strategy := firstNonEmpty(cfg.Space.Publish, "none")
			if strategy != "none" && !doPublish && interactive() {
				q := map[string]string{"pr": "Open a pull request to " + cfg.Space.Name + "?", "push": "Commit and push to " + cfg.Space.Name + "?"}[strategy]
				doPublish, _ = confirm(q, true)
			}
			if doPublish && strategy != "none" {
				pr, err := publishSession(cmd.Context(), cfg, h)
				if err != nil {
					res.Note = "publish failed: " + err.Error()
					if !flagJSON {
						fmt.Fprintln(os.Stderr, red("✗ ")+res.Note)
						note(dim("Retry with: ") + bold("recap publish "+h))
					}
					return done()
				}
				res.Publish = pr
				if !flagJSON {
					printPublish(pr)
				}
			} else if !flagJSON {
				next := "recap apply " + se.Handle
				if strategy != "none" {
					next = "recap publish " + se.Handle
				}
				note(dim("Review delta.md, then: ") + bold(next))
			}
			return done()
		},
	}
	c.Flags().BoolVar(&fixed, "fixed", false, "mark as fixed without asking")
	c.Flags().BoolVar(&notFixed, "not-fixed", false, "mark as not fixed (no recipe)")
	c.Flags().BoolVar(&doPublish, "publish", false, "publish per the space strategy without asking")
	c.Flags().BoolVar(&noModel, "no-model", false, "draft with templates only")
	return c
}

// reviewItems sorts events into related / maybe / excluded with the rules in
// internal/review; a configured model may then promote maybe items (SPEC D-10).
// Any model error falls back to the rules.
func reviewItems(ctx context.Context, se *session.Session, evs []session.Event, m *llmUse) []review.Item {
	items := review.Classify(se, evs, recipe.Contexts(evs))
	if m == nil {
		return items
	}
	sorted, err := review.SortMaybe(ctx, m.client, se, evs, items)
	if err != nil {
		if !flagJSON {
			fmt.Fprintln(os.Stderr, amber("! ")+"model sorting failed, using rules: "+err.Error())
		}
		return items
	}
	return sorted
}

// llmUse carries a ready client for the review sorter.
type llmUse struct{ client *llm.Client }

// reviewInteractive shows related (ticked), maybe (unticked) and excluded
// (unticked, last). Labels are redacted: secrets never show up on screen.
// It returns the kept events and how many maybe / excluded items were left out.
func reviewInteractive(evs []session.Event, items []review.Item, prod []string) (kept []session.Event, maybe, excluded int) {
	if len(evs) == 0 {
		return nil, 0, 0
	}
	ctxs := recipe.Contexts(evs)
	var opts []option
	var selected []string
	for _, b := range []review.Bucket{review.Related, review.Maybe, review.Excluded} {
		for _, it := range items {
			if it.Bucket != b {
				continue
			}
			e := evs[it.Index]
			id := strconv.Itoa(it.Index)
			text := redact.Redact(e.Text).Text
			if len(text) > 90 {
				text = text[:87] + "…"
			}
			var tag string
			switch {
			case b == review.Excluded:
				tag = "failed"
			case b == review.Maybe:
				tag = "maybe"
			case e.Kind == "cmd":
				tag = "cmd"
				if r := risk.Classify(e.Text, ctxs[it.Index], prod); r.Changes() {
					tag = r.Label()
				}
			default:
				tag = e.Kind
			}
			label := (tag + "        ")[:8] + " " + text
			if b == review.Maybe {
				label += "  " + dim("("+it.Reason+")")
			}
			opts = append(opts, option{id, label})
			if b == review.Related {
				selected = append(selected, id)
			}
		}
	}
	chosen, okk := pick("Keep for the recipe", "space toggles · enter confirms · maybe and failed items start unticked", opts, selected)
	if !okk {
		chosen = selected
	}
	set := map[string]bool{}
	for _, c := range chosen {
		set[c] = true
	}
	for i, e := range evs {
		switch {
		case set[strconv.Itoa(i)]:
			kept = append(kept, e)
		case items[i].Bucket == review.Excluded:
			excluded++
		default:
			maybe++
		}
	}
	return kept, maybe, excluded
}

func printReviewSummary(kept []session.Event, maybe, excluded int) {
	var cmds, notes, links int
	for _, e := range kept {
		switch e.Kind {
		case "cmd":
			cmds++
		case "note":
			notes++
		case "link", "file":
			links++
		}
	}
	fmt.Printf("  %s %s\n", bold(fmt.Sprintf("Related   %2d", len(kept))), dim(fmt.Sprintf("%d commands · %d notes · %d links/files", cmds, notes, links)))
	fmt.Printf("  %s %s\n", dim(fmt.Sprintf("Maybe     %2d", maybe)), dim("left out: read-only noise, other context or namespace, far from the rest"))
	fmt.Printf("  %s %s\n", dim(fmt.Sprintf("Excluded  %2d", excluded)), dim("failed commands"))
}

func enhance(ctx context.Context, name string, se *session.Session, evs []session.Event, r *recipe.Recipe, cfg env) error {
	cl, err := llm.New(cfg.Conf.ModelFor(cfg.Space))
	if err != nil {
		return err
	}
	if !flagJSON && isTTY(os.Stderr) {
		fmt.Fprint(os.Stderr, dim("  drafting with "+name+"…"))
		defer fmt.Fprint(os.Stderr, "\r\033[K")
	}
	ctx, cancel := context.WithTimeout(ctx, cl.HTTP.Timeout)
	defer cancel()
	return draft.Enhance(ctx, cl, se, evs, r)
}
