// Package review groups a session's events into related / maybe / excluded
// for the stop review (SPEC D-10). Rules decide first; a model may only
// promote maybe items, never touch the rest (SPEC D-12 spirit: rules stay the floor).
package review

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/idestis/recap/internal/redact"
	"github.com/idestis/recap/internal/session"
)

// Bucket is where an event lands in the review.
type Bucket int

const (
	Related  Bucket = iota // preselected
	Maybe                  // shown, not preselected
	Excluded               // failed commands, shown last
)

// Item is the verdict for one event, by index into the event slice.
type Item struct {
	Index  int
	Bucket Bucket
	Reason string
}

// IsolationGap is how far a command must sit from every other event to count as isolated.
const IsolationGap = 30 * time.Minute

var (
	nsRe   = regexp.MustCompile(`(?:\s-n[ =]|\s--namespace[ =])\s*([a-z0-9][a-z0-9.-]*)`)
	ctxRe  = regexp.MustCompile(`\s--context[ =]\s*([^\s]+)`)
	wordRe = regexp.MustCompile(`[a-z0-9][a-z0-9_-]{2,}`)

	noise = map[string]bool{
		"ls": true, "ll": true, "la": true, "cd": true, "pwd": true, "clear": true, "history": true,
		"exit": true, "logout": true, "whoami": true, "date": true, "true": true, "reset": true,
		"pushd": true, "popd": true, "echo": true, "which": true, "tput": true, "fg": true, "bg": true, "jobs": true,
	}
	stop = map[string]bool{"the": true, "and": true, "for": true, "with": true, "from": true, "that": true, "this": true, "after": true, "into": true, "not": true, "are": true, "was": true, "has": true, "have": true}
)

// Classify sorts events. ctxs is the recorded context at each event (same
// length as evs, e.g. recipe.Contexts); it may be nil.
func Classify(se *session.Session, evs []session.Event, ctxs []string) []Item {
	desc := tokens(se.Description)
	// Dominant contexts and namespaces among successful commands.
	ctxCount, nsCount := map[string]int{}, map[string]int{}
	for i, e := range evs {
		if e.Kind != "cmd" || failed(e) {
			continue
		}
		if c := eventCtx(e, ctxs, i); c != "" {
			ctxCount[c]++
		}
		if n := eventNS(e); n != "" {
			nsCount[n]++
		}
	}
	domCtx, domNS := dominant(ctxCount), dominant(nsCount)

	out := make([]Item, len(evs))
	for i, e := range evs {
		it := Item{Index: i, Bucket: Related}
		switch {
		case e.Kind == "cmd" && failed(e):
			it.Bucket, it.Reason = Excluded, "failed command"
		case e.Kind == "intent":
			// Transcript text reaches Git only when a person ticks it (SPEC D-10, D-16).
			it.Bucket, it.Reason = Maybe, "Claude Code transcript"
		case e.Kind != "cmd":
			it.Reason = "note, link or file"
		case mentions(e.Text, desc):
			it.Reason = "matches the start description"
		case isNoise(e.Text):
			it.Bucket, it.Reason = Maybe, "read-only noise"
		case contextOff(e, ctxs, i, domCtx):
			it.Bucket, it.Reason = Maybe, "different context from the rest"
		case nsOff(e, domNS):
			it.Bucket, it.Reason = Maybe, "different namespace from the rest"
		case isolated(evs, i):
			it.Bucket, it.Reason = Maybe, "far from the main activity"
		default:
			it.Reason = "part of the main activity"
		}
		out[i] = it
	}
	return out
}

// Counts returns how many items fall in each bucket.
func Counts(items []Item) (related, maybe, excluded int) {
	for _, it := range items {
		switch it.Bucket {
		case Related:
			related++
		case Maybe:
			maybe++
		default:
			excluded++
		}
	}
	return
}

// Pick returns the events in the given bucket, in order.
func Pick(evs []session.Event, items []Item, b Bucket) []session.Event {
	var out []session.Event
	for _, it := range items {
		if it.Bucket == b {
			out = append(out, evs[it.Index])
		}
	}
	return out
}

// JSONer is the slice of llm.Client the sorter needs.
type JSONer interface {
	JSON(ctx context.Context, system, user string, v any) error
}

const sorterPrompt = `You sort terminal commands from an incident session. Given the incident description and numbered commands that look unrelated, return JSON {"keep":[ids]} listing only those that plausibly helped resolve the incident. Reply with JSON only.`

// SortMaybe lets a model promote maybe items to related. Texts are redacted
// before they leave the machine (SPEC D-11). On any error the rule result is
// returned unchanged along with the error.
func SortMaybe(ctx context.Context, c JSONer, se *session.Session, evs []session.Event, items []Item) ([]Item, error) {
	var lines []string
	for _, it := range items {
		if it.Bucket == Maybe && evs[it.Index].Kind == "cmd" {
			lines = append(lines, fmt.Sprintf("%d: %s", it.Index, redact.Redact(evs[it.Index].Text).Text))
		}
	}
	if len(lines) == 0 {
		return items, nil
	}
	user := "Description: " + redact.Redact(se.Description).Text + "\nCommands:\n" + strings.Join(lines, "\n")
	var res struct {
		Keep []int `json:"keep"`
	}
	if err := c.JSON(ctx, sorterPrompt, user, &res); err != nil {
		return items, err
	}
	out := append([]Item(nil), items...)
	for _, k := range res.Keep {
		if k >= 0 && k < len(out) && out[k].Bucket == Maybe && evs[k].Kind == "cmd" {
			out[k].Bucket, out[k].Reason = Related, "kept by the model"
		}
	}
	return out, nil
}

func failed(e session.Event) bool { return e.Exit != nil && *e.Exit != 0 }

func tokens(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range wordRe.FindAllString(strings.ToLower(s), -1) {
		if !stop[w] {
			m[w] = true
		}
	}
	return m
}

func mentions(cmd string, desc map[string]bool) bool {
	if len(desc) == 0 {
		return false
	}
	for w := range tokens(cmd) {
		if desc[w] {
			return true
		}
	}
	return false
}

func isNoise(cmd string) bool {
	f := strings.Fields(cmd)
	return len(f) > 0 && noise[f[0]]
}

func eventCtx(e session.Event, ctxs []string, i int) string {
	if m := ctxRe.FindStringSubmatch(e.Text); m != nil {
		return m[1]
	}
	if i < len(ctxs) {
		return ctxs[i]
	}
	return ""
}

func eventNS(e session.Event) string {
	if m := nsRe.FindStringSubmatch(" " + e.Text); m != nil {
		return m[1]
	}
	return ""
}

// dominant keeps values used more than half as often as the most used one.
func dominant(count map[string]int) map[string]bool {
	max := 0
	for _, n := range count {
		if n > max {
			max = n
		}
	}
	out := map[string]bool{}
	for v, n := range count {
		if n*2 > max {
			out[v] = true
		}
	}
	return out
}

func contextOff(e session.Event, ctxs []string, i int, dom map[string]bool) bool {
	c := eventCtx(e, ctxs, i)
	return c != "" && len(dom) > 0 && !dom[c]
}

func nsOff(e session.Event, dom map[string]bool) bool {
	n := eventNS(e)
	return n != "" && len(dom) > 0 && !dom[n]
}

// isolated: every neighbouring event is further away than IsolationGap.
func isolated(evs []session.Event, i int) bool {
	if len(evs) < 3 {
		return false
	}
	t := evs[i].Time
	near := false
	if i > 0 && t.Sub(evs[i-1].Time) < IsolationGap {
		near = true
	}
	if i < len(evs)-1 && evs[i+1].Time.Sub(t) < IsolationGap {
		near = true
	}
	return !near
}
