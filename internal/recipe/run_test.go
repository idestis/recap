package recipe

import (
	"strings"
	"testing"

	"github.com/idestis/recap/internal/risk"
)

func TestRenderedStepsParseBack(t *testing.T) {
	r := &Recipe{ID: "fix-oom", Title: "Fix OOM", Situation: "Pods OOMKilled.", Steps: []Step{
		{Title: "List pods", Cmd: "kubectl get pods -n {namespace}", Risk: risk.Result{Level: risk.Safe}},
		{Title: "Raise limit", Cmd: "kubectl set resources deploy/{deployment} --limits=memory=1Gi", Undo: "kubectl rollout undo deploy/{deployment}", Risk: risk.Result{Level: risk.Risky, Prod: true}},
	}}
	doc := r.Render()
	if !strings.HasPrefix(doc, "---\nid: fix-oom\n") || !strings.Contains(doc, "   ```sh\n   kubectl get pods -n {namespace}\n   ```") {
		t.Fatalf("unexpected render:\n%s", doc)
	}
	got := ParseSteps(doc)
	if len(got) != 2 || got[0].Cmd != "kubectl get pods -n {namespace}" || got[1].Label != "risky · prod" || got[1].Undo != "kubectl rollout undo deploy/{deployment}" {
		t.Fatalf("parsed %+v", got)
	}
}

func TestOldInlineStepsStillParse(t *testing.T) {
	old := "## Steps\n1. List pods — safe\n   `kubectl get pods`\n2. Restart — risky\n   `kubectl rollout restart deploy/x`\n   Undo: `kubectl rollout undo deploy/x`\n"
	got := ParseSteps(old)
	if len(got) != 2 || got[1].Cmd != "kubectl rollout restart deploy/x" || got[1].Undo != "kubectl rollout undo deploy/x" {
		t.Fatalf("parsed %+v", got)
	}
}
