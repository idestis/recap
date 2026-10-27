package draft

import (
	"encoding/json"
	"testing"

	"github.com/idestis/recap/internal/recipe"
	"github.com/idestis/recap/internal/risk"
)

func TestMergeRaisesNeverLowers(t *testing.T) {
	r := &recipe.Recipe{ID: "x", Title: "X", Preconditions: []string{"rollback ruled out, migration 0142 already ran"}, Steps: []recipe.Step{
		{Title: "a", Cmd: "kubectl get pods", Risk: risk.Result{Level: risk.Safe}},
		{Title: "b", Cmd: "kubectl delete pod p", Risk: risk.Result{Level: risk.Destructive}},
	}}
	var out output
	_ = json.Unmarshal([]byte(`{
		"id": "Fix OOM pods", "preconditions": ["Migration 0142 already ran", "check alerts first"], "title": "Fix OOMKilled pods", "situation": "token=abcd1234abcd1234abcd1234",
		"steps": [{"n":1,"title":"List pods"},{"n":2,"title":"Delete pod"}],
		"risk": [{"n":1,"level":"risky","why":"touches prod"},{"n":2,"level":"safe","why":"lower"}]
	}`), &out)
	if err := merge(r, out); err != nil {
		t.Fatal(err)
	}
	if r.ID != "fix-oom-pods" || r.Steps[0].Title != "List pods" {
		t.Fatalf("text not merged: %+v", r)
	}
	if len(r.Preconditions) != 2 || r.Preconditions[1] != "check alerts first" {
		t.Errorf("preconditions %q", r.Preconditions)
	}
	if r.Steps[0].Risk.Level != risk.Risky {
		t.Error("model should be able to raise risk")
	}
	if r.Steps[1].Risk.Level != risk.Destructive {
		t.Error("model must never lower risk")
	}
	if r.Situation == "token=abcd1234abcd1234abcd1234" {
		t.Error("model output must be redacted")
	}
	if r.Steps[1].Cmd != "kubectl delete pod p" {
		t.Error("commands must not change")
	}
}

func TestMergeRejectsStepCountMismatch(t *testing.T) {
	r := &recipe.Recipe{Steps: []recipe.Step{{Title: "a"}}}
	var out output
	_ = json.Unmarshal([]byte(`{"steps":[{"n":1,"title":"x"},{"n":2,"title":"y"}]}`), &out)
	if merge(r, out) == nil {
		t.Fatal("expected error")
	}
}
