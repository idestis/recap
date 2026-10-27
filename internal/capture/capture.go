// Package capture adds context to a recording session: notes, links and files
// detected from one free-form input (SPEC D-09). `recap ctx`, the overlay and
// the MCP add_context tool share it.
package capture

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/idestis/recap/internal/ctxdetect"
	"github.com/idestis/recap/internal/session"
	"github.com/idestis/recap/internal/store"
)

// ErrNoSession means there is no session recording to add context to.
var ErrNoSession = errors.New("no active session — start one with: recap start \"<what's wrong>\"")

// ActiveHandle returns the session to record into: RECAP_SESSION, then the
// store's active session.
func ActiveHandle(st *store.Store) (string, error) {
	if h := os.Getenv("RECAP_SESSION"); h != "" {
		return h, nil
	}
	if h := st.Active(); h != "" {
		return h, nil
	}
	return "", ErrNoSession
}

// Added is one item recorded by Add.
type Added struct {
	Kind     string `json:"kind"`                // note | link | file
	LinkType string `json:"link_type,omitempty"` // slack | dashboard | pr | call | issue | link
	Text     string `json:"text"`                // for files: the local copy
	Ref      string `json:"ref,omitempty"`
	Decision bool   `json:"decision,omitempty"`
}

// Add detects items in input and appends them to the session's timeline in
// the local store (never Git). A Slack channel link is stored by ID (SPEC D-08);
// files are copied into the session's files dir.
func Add(st *store.Store, handle, input, source string) ([]Added, error) {
	se, err := st.LoadSession(handle)
	if err != nil {
		return nil, err
	}
	items := ctxdetect.Detect(input)
	if len(items) == 0 {
		return nil, errors.New("nothing to add")
	}
	if se.Links == nil {
		se.Links = map[string]string{}
	}
	var out []Added
	changed := false
	for _, it := range items {
		ev := session.Event{Time: time.Now(), Kind: it.Kind, Source: source, Text: it.Text, LinkType: it.LinkType, Ref: it.Ref}
		if it.Decision {
			ev.Tags = []string{"decision"}
		}
		switch {
		case it.Kind == "file":
			dst, err := copyFile(it.Text, st.FilesDir(handle))
			if err != nil {
				return out, err
			}
			ev.Text = dst
		case it.LinkType == "slack":
			se.Links["slack"] = it.Ref
			changed = true
		}
		if err := st.AppendEvent(handle, ev); err != nil {
			return out, err
		}
		out = append(out, Added{Kind: ev.Kind, LinkType: ev.LinkType, Text: ev.Text, Ref: ev.Ref, Decision: it.Decision})
	}
	if changed {
		if err := st.SaveSession(se); err != nil {
			return out, err
		}
	}
	return out, nil
}

// LinkLabel names a link type for people.
func LinkLabel(t string) string {
	switch t {
	case "dashboard":
		return "Dashboard"
	case "pr":
		return "Pull request"
	case "call":
		return "Call"
	case "slack-thread":
		return "Slack thread"
	case "issue":
		return "Incident/issue"
	}
	return "Link"
}

func copyFile(src, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	dst := filepath.Join(dir, time.Now().Format("150405")+"-"+filepath.Base(src))
	out, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("attach %s: %w", src, err)
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return dst, err
}
