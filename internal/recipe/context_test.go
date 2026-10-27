package recipe

import (
	"testing"
	"time"

	"github.com/idestis/recap/internal/session"
)

func TestContextsComeFromRecordedSwitches(t *testing.T) {
	fail, okc := 1, 0
	ev := func(text string, exit *int) session.Event {
		return session.Event{Time: time.Now(), Kind: "cmd", Text: text, Exit: exit}
	}
	evs := []session.Event{
		ev("kubectl get pods", &okc),
		ev("kubectx staging", &okc),
		ev("kubectl get pods", &okc),
		ev("kubectx prod-eu", &fail), // failed switch doesn't count
		ev("kubectl get pods", &okc),
		ev("kubectl config use-context prod-eu", &okc),
		ev("kubectl delete pod x", &okc),
	}
	got := Contexts(evs)
	want := []string{"", "staging", "staging", "staging", "staging", "prod-eu", "prod-eu"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d: context %q, want %q (all: %q)", i, got[i], want[i], got)
		}
	}

	se := &session.Session{Name: "x", Description: "x", StartedAt: time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)}
	r, _ := Draft(se, evs, []string{"prod-eu"})
	last := r.Steps[len(r.Steps)-1]
	if !last.Risk.Prod || r.Verified != "prod-eu 2026-10-27" {
		t.Fatalf("last step should be prod and verified on prod-eu: %+v %q", last.Risk, r.Verified)
	}
	if r.Steps[0].Risk.Prod {
		t.Fatal("a step before any recorded switch must not be assumed prod")
	}
}
