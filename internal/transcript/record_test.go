package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/idestis/recap/internal/session"
	"github.com/idestis/recap/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st := &store.Store{Home: t.TempDir()}
	se := &session.Session{Handle: "h1", Name: "n", StartedAt: time.Now()}
	if err := st.SaveSession(se); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRecordDedupAcrossCalls(t *testing.T) {
	st := newStore(t)
	n, err := Record(st, "h1", "testdata/sample.jsonl")
	if err != nil || n != 4 {
		t.Fatalf("first: n=%d err=%v", n, err)
	}
	// The hook fires every turn: same transcript again adds nothing.
	n, err = Record(st, "h1", "testdata/sample.jsonl")
	if err != nil || n != 0 {
		t.Fatalf("second: n=%d err=%v", n, err)
	}
	evs, _ := st.Events("h1")
	if len(evs) != 4 || evs[0].Kind != "intent" || evs[0].Source != "claude" {
		t.Fatalf("events: %+v", evs)
	}
}

func TestRecordPicksUpAppendedLines(t *testing.T) {
	st := newStore(t)
	p := filepath.Join(t.TempDir(), "t.jsonl")
	l1 := `{"type":"user","message":{"role":"user","content":"first"}}` + "\n"
	l2 := `{"type":"user","message":{"role":"user","content":"second"}}` + "\n"
	_ = os.WriteFile(p, []byte(l1), 0o600)
	if n, _ := Record(st, "h1", p); n != 1 {
		t.Fatalf("n=%d", n)
	}
	_ = os.WriteFile(p, []byte(l1+l2), 0o600)
	if n, _ := Record(st, "h1", p); n != 1 {
		t.Fatalf("n=%d", n)
	}
}

func TestRecordSilentFailures(t *testing.T) {
	st := newStore(t)
	// Missing transcript and unknown session return errors for the caller
	// to ignore; they must not panic or write anything.
	if _, err := Record(st, "h1", "testdata/missing.jsonl"); err == nil {
		t.Fatal("want error")
	}
	if n, _ := Record(st, "nope", "testdata/sample.jsonl"); n != 0 {
		t.Fatal("recorded for unknown session")
	}
	bad := filepath.Join(t.TempDir(), "bad.jsonl")
	_ = os.WriteFile(bad, []byte("not json\n{\n"), 0o600)
	if n, err := Record(st, "h1", bad); n != 0 || err != nil {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
