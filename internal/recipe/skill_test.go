package recipe

import (
	"strings"
	"testing"

	"github.com/idestis/recap/internal/risk"
)

func TestSkillFromRecipe(t *testing.T) {
	r := &Recipe{ID: "fix-oom", Title: "Fix OOMKilled pods", Situation: "Pods restart with OOMKilled. Seen after deploys.",
		Preconditions: []string{"do not roll back, migration ran"}, Vars: []string{"deployment", "namespace"},
		Steps: []Step{{Title: "Raise limit", Cmd: "kubectl set resources deploy/{deployment} -n {namespace} --limits=memory=1Gi", Risk: risk.Result{Level: risk.Risky}}}}
	md, err := Skill(r.Render())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"---\nname: fix-oom\ndescription: Fix OOMKilled pods. Pods restart with OOMKilled. Use when",
		"`recap run fix-oom --var deployment=… --var namespace=…`",
		"--execute",
		"### Preconditions",
		"   kubectl set resources deploy/{deployment}",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
	if strings.Contains(md, "\n# Fix OOMKilled pods\n\n## Situation") || strings.Count(md, "# Fix OOMKilled pods") != 1 {
		t.Errorf("title should appear once:\n%s", md)
	}
}
