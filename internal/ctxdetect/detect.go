// Package ctxdetect turns whatever the user drops into `recap ctx` (or the
// overlay) into typed timeline items: one input, recap figures out the rest
// (SPEC D-09).
package ctxdetect

import (
	"net/url"
	"os"
	"regexp"
	"strings"
)

// Item is one detected piece of context.
type Item struct {
	Kind     string // note | link | file
	LinkType string // slack | dashboard | pr | call | issue | link
	Text     string
	Ref      string // e.g. Slack channel ID, PR number
	Decision bool
}

var (
	urlRe      = regexp.MustCompile(`https?://[^\s<>"']+`)
	slackChan  = regexp.MustCompile(`slack\.com/archives/([CGD][A-Z0-9]{6,})(?:/p(\d+))?`)
	githubPR   = regexp.MustCompile(`github\.com/[^/]+/[^/]+/pull/(\d+)`)
	decisionRe = regexp.MustCompile(`(?i)\b(ruled out|decided|decision|don't|do not|won't|must not|no rollback|instead of|because)\b`)
)

// Detect splits input into a note (text without URLs) plus one item per URL,
// or a single file item when the input is an existing path.
func Detect(input string) []Item {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}
	if !strings.ContainsAny(input, " \n") {
		if st, err := os.Stat(input); err == nil && !st.IsDir() {
			return []Item{{Kind: "file", Text: input}}
		}
	}
	var items []Item
	note := strings.TrimSpace(urlRe.ReplaceAllString(input, ""))
	note = strings.Join(strings.Fields(note), " ")
	if note != "" {
		items = append(items, Item{Kind: "note", Text: note, Decision: decisionRe.MatchString(note)})
	}
	for _, u := range urlRe.FindAllString(input, -1) {
		items = append(items, classifyURL(strings.TrimRight(u, ".,);")))
	}
	return items
}

func classifyURL(raw string) Item {
	it := Item{Kind: "link", LinkType: "link", Text: raw}
	if m := slackChan.FindStringSubmatch(raw); m != nil {
		it.LinkType, it.Ref = "slack", m[1]
		if m[2] != "" {
			it.LinkType = "slack-thread"
		}
		return it
	}
	if m := githubPR.FindStringSubmatch(raw); m != nil {
		it.LinkType, it.Ref = "pr", m[1]
		return it
	}
	u, err := url.Parse(raw)
	if err != nil {
		return it
	}
	host := strings.ToLower(u.Host)
	switch {
	case strings.Contains(host, "grafana"), strings.Contains(host, "datadoghq"),
		strings.Contains(host, "kibana"), strings.Contains(host, "newrelic"),
		strings.Contains(host, "honeycomb"), strings.Contains(host, "sentry"):
		it.LinkType = "dashboard"
	case strings.Contains(host, "zoom.us"), strings.Contains(host, "meet.google.com"),
		strings.Contains(host, "teams.microsoft.com"), strings.Contains(host, "wisprflow"):
		it.LinkType = "call"
	case strings.Contains(host, "atlassian.net"), strings.Contains(host, "linear.app"),
		strings.Contains(host, "incident.io"), strings.Contains(host, "pagerduty"):
		it.LinkType = "issue"
	}
	return it
}
