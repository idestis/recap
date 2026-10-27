package recipe

import (
	"fmt"
	"strings"
	"time"

	"github.com/idestis/recap/internal/redact"
	"github.com/idestis/recap/internal/session"
)

// ContextMarkdown is the short "why" kept with the session folder: decisions,
// notes and links, redacted. Raw commands and messages stay in the local store
// (SPEC D-16).
func ContextMarkdown(se *session.Session, evs []session.Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s · %s\n\n%s\n\n", se.Handle, se.Name, redact.Redact(se.Description).Text)
	end := time.Now()
	if se.StoppedAt != nil {
		end = *se.StoppedAt
	}
	fmt.Fprintf(&b, "Window: %s – %s\n\n", se.StartedAt.Format("2006-01-02 15:04"), end.Format("15:04"))
	section := func(title string, pick func(session.Event) bool) {
		var lines []string
		for _, e := range evs {
			if pick(e) {
				lines = append(lines, fmt.Sprintf("- %s %s", e.Time.Format("15:04"), redact.Redact(e.Text).Text))
			}
		}
		if len(lines) > 0 {
			fmt.Fprintf(&b, "## %s\n%s\n\n", title, strings.Join(lines, "\n"))
		}
	}
	section("Decisions", func(e session.Event) bool { return e.Kind == "note" && hasTag(e.Tags, "decision") })
	section("Notes", func(e session.Event) bool { return e.Kind == "note" && !hasTag(e.Tags, "decision") })
	section("Intent (Claude Code)", func(e session.Event) bool { return e.Kind == "intent" && hasTag(e.Tags, "step") })
	section("Links", func(e session.Event) bool { return e.Kind == "link" })
	return b.String()
}
