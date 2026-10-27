package session

import "time"

// Session is stored as session.json in the local store (never in Git).
type Session struct {
	ID          string            `json:"id"`
	Handle      string            `json:"handle"`
	Name        string            `json:"name"`
	NameSource  string            `json:"name_source"`
	Description string            `json:"description"`
	StartedAt   time.Time         `json:"started_at"`
	StoppedAt   *time.Time        `json:"stopped_at,omitempty"`
	KubeContext string            `json:"kube_context,omitempty"`
	Links       map[string]string `json:"links,omitempty"`
	Tabs        []string          `json:"tabs,omitempty"`
	Space       string            `json:"space,omitempty"` // config space whose repo gets the recipe
	Fixed       *bool             `json:"fixed,omitempty"`
}

// Active reports whether the session is still recording.
func (s *Session) Active() bool { return s.StoppedAt == nil }

// Event is one line in events.jsonl.
type Event struct {
	Time     time.Time `json:"time"`
	Kind     string    `json:"kind"`   // cmd | note | link | file
	Source   string    `json:"source"` // terminal | claude | ctx | overlay
	Text     string    `json:"text"`
	Exit     *int      `json:"exit,omitempty"`
	Dir      string    `json:"dir,omitempty"`
	TTY      string    `json:"tty,omitempty"`
	LinkType string    `json:"link_type,omitempty"` // slack | dashboard | pr | call | link
	Ref      string    `json:"ref,omitempty"`       // e.g. Slack channel ID
	Tags     []string  `json:"tags,omitempty"`      // e.g. decision
}
