package review

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/idestis/recap/internal/session"
)

var t0 = time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

func cmd(min int, text string, exit int) session.Event {
	e := exit
	return session.Event{Time: t0.Add(time.Duration(min) * time.Minute), Kind: "cmd", Text: text, Exit: &e}
}

func TestClassify(t *testing.T) {
	se := &session.Session{Description: "checkout latency spike"}
	tests := []struct {
		name string
		evs  []session.Event
		ctxs []string
		want []Bucket
	}{
		{"failed excluded", []session.Event{cmd(0, "kubectl get pods", 1), cmd(1, "kubectl get pods -n a", 0)}, nil, []Bucket{Excluded, Related}},
		{"noise is maybe", []session.Event{cmd(0, "kubectl get pods -n a", 0), cmd(1, "ls -la", 0), cmd(2, "kubectl get pods -n a", 0)}, nil, []Bucket{Related, Maybe, Related}},
		{"noise but matches description", []session.Event{cmd(0, "kubectl get pods -n a", 0), cmd(1, "echo checkout", 0), cmd(2, "kubectl get pods -n a", 0)}, nil, []Bucket{Related, Related, Related}},
		{"other namespace", []session.Event{cmd(0, "kubectl get pods -n a", 0), cmd(1, "kubectl get pods -n a", 0), cmd(2, "kubectl get pods --namespace=zzz", 0)}, nil, []Bucket{Related, Related, Maybe}},
		{"other context", []session.Event{cmd(0, "kubectl get pods", 0), cmd(1, "kubectl get pods", 0), cmd(2, "kubectl get pods", 0)}, []string{"prod", "prod", "dev"}, []Bucket{Related, Related, Maybe}},
		{"context flag", []session.Event{cmd(0, "kubectl get pods", 0), cmd(1, "kubectl get pods", 0), cmd(2, "kubectl get pods --context dev", 0)}, []string{"prod", "prod", "prod"}, []Bucket{Related, Related, Maybe}},
		{"isolated", []session.Event{cmd(0, "kubectl get pods", 0), cmd(1, "kubectl get svc", 0), cmd(120, "kubectl get nodes", 0)}, nil, []Bucket{Related, Related, Maybe}},
		{"notes always related", []session.Event{{Time: t0, Kind: "note", Text: "hi"}, {Time: t0.Add(5 * time.Hour), Kind: "link", Text: "x"}}, nil, []Bucket{Related, Related}},
		{"transcript intent is maybe", []session.Event{cmd(0, "kubectl get pods", 0), {Time: t0, Kind: "intent", Text: "checkout pods look stuck", Tags: []string{"step"}}}, nil, []Bucket{Related, Maybe}},
		{"single event related", []session.Event{cmd(0, "kubectl get pods", 0)}, nil, []Bucket{Related}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(se, tt.evs, tt.ctxs)
			for i, w := range tt.want {
				if got[i].Bucket != w {
					t.Errorf("event %d (%q): got %v (%s), want %v", i, tt.evs[i].Text, got[i].Bucket, got[i].Reason, w)
				}
			}
		})
	}
}

type fake struct {
	keep []int
	err  error
}

func (f fake) JSON(_ context.Context, _, _ string, v any) error {
	if f.err != nil {
		return f.err
	}
	v.(*struct {
		Keep []int `json:"keep"`
	}).Keep = f.keep
	return nil
}

func TestSortMaybe(t *testing.T) {
	se := &session.Session{}
	evs := []session.Event{cmd(0, "ls", 0), cmd(1, "kubectl get pods", 1)}
	items := Classify(se, evs, nil)
	got, err := SortMaybe(context.Background(), fake{keep: []int{0, 1}}, se, evs, items)
	if err != nil || got[0].Bucket != Related || got[1].Bucket != Excluded {
		t.Fatalf("promote: %v %+v", err, got)
	}
	got, err = SortMaybe(context.Background(), fake{err: errors.New("down")}, se, evs, items)
	if err == nil || got[0].Bucket != Maybe {
		t.Fatalf("fallback: %v %+v", err, got)
	}
}

func TestSortMaybeNeverPromotesIntent(t *testing.T) {
	se := &session.Session{}
	evs := []session.Event{{Time: t0, Kind: "intent", Text: "why"}}
	got, err := SortMaybe(context.Background(), fake{keep: []int{0}}, se, evs, Classify(se, evs, nil))
	if err != nil || got[0].Bucket != Maybe {
		t.Fatalf("intent promoted: %v %+v", err, got)
	}
}

func TestCountsPick(t *testing.T) {
	evs := []session.Event{cmd(0, "a", 0), cmd(1, "b", 1)}
	items := []Item{{0, Related, ""}, {1, Excluded, ""}}
	r, m, x := Counts(items)
	if r != 1 || m != 0 || x != 1 || len(Pick(evs, items, Excluded)) != 1 {
		t.Fatal("counts")
	}
}
