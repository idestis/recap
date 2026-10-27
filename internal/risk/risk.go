// Package risk classifies commands with deterministic rules (SPEC D-12).
// A model may raise a level later; nothing may lower it.
package risk

import (
	"regexp"
	"strings"
)

type Level int

const (
	Safe Level = iota
	Risky
	Destructive
)

func (l Level) String() string {
	return [...]string{"safe", "risky", "destructive"}[l]
}

// Result of classifying one command.
type Result struct {
	Level   Level
	Prod    bool
	Tool    string
	Reasons []string
}

// Label renders e.g. "risky · prod".
func (r Result) Label() string {
	if r.Level == Safe {
		return "safe"
	}
	if r.Prod {
		return r.Level.String() + " · prod"
	}
	return r.Level.String()
}

var (
	kubectlChange = regexp.MustCompile(`\bkubectl\s+(apply|create|replace|patch|edit|set|scale|annotate|label|cordon|uncordon|taint)\b|\bkubectl\s+rollout\s+(restart|undo|resume)\b`)
	kubectlDelete = regexp.MustCompile(`\bkubectl\s+(delete|drain)\b`)
	tfChange      = regexp.MustCompile(`\bterraform\s+(apply|import|taint|untaint)\b|\bterraform\s+state\s+(mv|push|replace-provider)\b`)
	tfDestroy     = regexp.MustCompile(`\bterraform\s+(destroy|state\s+rm)\b|\bterraform\s+apply\b.*-destroy`)
	helmChange    = regexp.MustCompile(`\bhelm\s+(upgrade|install|rollback)\b`)
	helmDelete    = regexp.MustCompile(`\bhelm\s+(uninstall|delete)\b`)
	cloudChange   = regexp.MustCompile(`\b(aws|gcloud|az)\b.*\b(create|update|put|modify|attach|detach|start|stop|reboot|set|scale|restore|deploy)\b`)
	cloudDelete   = regexp.MustCompile(`\b(aws|gcloud|az)\b.*\b(delete|terminate|remove|purge|destroy)\b`)
	dbChange      = regexp.MustCompile(`(?i)\b(drop|truncate|delete\s+from|alter\s+table|update\s+\w+\s+set)\b`)
	dockerDelete  = regexp.MustCompile(`\bdocker\s+((container|image|volume|network)\s+)?(rm|rmi|prune)\b|\bdocker\s+system\s+prune\b|\bdocker[\s-]compose\b.*\bdown\b.*(\s-v\b|--volumes)`)
	dockerChange  = regexp.MustCompile(`\bdocker\s+(stop|start|restart|kill|run|exec|update|pause|unpause)\b|\bdocker[\s-]compose\b.*\b(up|down|restart|stop|start|run|exec)\b`)
	rmrf          = regexp.MustCompile(`\brm\s+-[a-z]*r[a-z]*f|\brm\s+-[a-z]*f[a-z]*r`)
	force         = regexp.MustCompile(`(^|\s)(--force|-f\b|--grace-period=0|--auto-approve|-auto-approve)`)
	prodWord      = regexp.MustCompile(`(?i)(^|[^a-z])(prod|production|prd)([^a-z]|$)`)
)

// Classify a command. prodContexts lists kube contexts / accounts that count as prod.
func Classify(cmd, currentContext string, prodContexts []string) Result {
	r := Result{Tool: tool(cmd)}
	raise := func(l Level, why string) {
		if l > r.Level {
			r.Level = l
		}
		r.Reasons = append(r.Reasons, why)
	}
	switch {
	case kubectlDelete.MatchString(cmd), tfDestroy.MatchString(cmd), helmDelete.MatchString(cmd),
		cloudDelete.MatchString(cmd), dockerDelete.MatchString(cmd), rmrf.MatchString(cmd):
		raise(Destructive, "deletes or destroys resources")
	case kubectlChange.MatchString(cmd), tfChange.MatchString(cmd), helmChange.MatchString(cmd),
		cloudChange.MatchString(cmd), dockerChange.MatchString(cmd):
		raise(Risky, "changes infrastructure")
	}
	if dbChange.MatchString(cmd) && strings.ContainsAny(cmd, " ") && (strings.Contains(cmd, "psql") || strings.Contains(cmd, "mysql") || strings.Contains(strings.ToLower(cmd), "sql")) {
		raise(Destructive, "modifies database")
	}
	if r.Level > Safe && force.MatchString(cmd) {
		raise(Destructive, "forced or auto-approved")
	}
	ctx := currentContext
	if m := regexp.MustCompile(`--context[= ](\S+)`).FindStringSubmatch(cmd); m != nil {
		ctx = m[1]
	}
	for _, p := range prodContexts {
		if p != "" && ctx == p {
			r.Prod = true
		}
	}
	if prodWord.MatchString(ctx) || prodWord.MatchString(cmd) {
		r.Prod = true
	}
	return r
}

// Changes reports whether the command changes something (worth a recipe step).
func (r Result) Changes() bool { return r.Level > Safe }

func tool(cmd string) string {
	f := strings.Fields(cmd)
	for _, w := range f {
		if strings.Contains(w, "=") && !strings.HasPrefix(w, "-") {
			continue // env assignment
		}
		if w == "sudo" {
			continue
		}
		return w
	}
	return ""
}

var (
	kubectxRe    = regexp.MustCompile(`^\s*kubectx\s+([A-Za-z0-9._:@/-]+)\s*$`)
	useContextRe = regexp.MustCompile(`\bkubectl\s+config\s+use-context\s+([A-Za-z0-9._:@/-]+)`)
)

// ContextSwitch reports the kube context a command switches to. recap never
// reads the live kubeconfig for a session: the context is what was recorded.
func ContextSwitch(cmd string) (string, bool) {
	if m := kubectxRe.FindStringSubmatch(cmd); m != nil && m[1] != "-" {
		return m[1], true
	}
	if m := useContextRe.FindStringSubmatch(cmd); m != nil {
		return m[1], true
	}
	return "", false
}
