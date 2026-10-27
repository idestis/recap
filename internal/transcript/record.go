package transcript

import (
	"time"

	"github.com/idestis/recap/internal/session"
	"github.com/idestis/recap/internal/store"
)

// Record appends new intents from the transcript at path to the session
// timeline as Kind "intent" events (Tags: prompt | step) and advances the
// per-session cursor, so repeated Stop calls never duplicate. Nothing is
// recorded for an inactive session.
func Record(st *store.Store, handle, path string) (int, error) {
	se, err := st.LoadSession(handle)
	if err != nil || !se.Active() {
		return 0, err
	}
	from := st.Cursor(handle, path)
	intents, next, err := Parse(path, from)
	if err != nil {
		return 0, err
	}
	if next < from { // transcript was truncated or replaced
		intents, next, err = Parse(path, 0)
		if err != nil {
			return 0, err
		}
	}
	n := 0
	for _, in := range intents {
		t := in.Time
		if t.IsZero() {
			t = time.Now()
		}
		ev := session.Event{Time: t, Kind: "intent", Source: "claude", Text: in.Text, Tags: []string{in.Kind}}
		if err := st.AppendEvent(handle, ev); err != nil {
			return n, err
		}
		n++
	}
	return n, st.SetCursor(handle, path, next)
}
