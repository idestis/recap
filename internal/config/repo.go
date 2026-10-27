package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RepoSettings holds values from <repo>/recap.yaml (tiny line parser for the prototype).
type RepoSettings struct {
	ProdContexts       []string
	ApplyKeepsSessions bool
}

var listRe = regexp.MustCompile(`\[(.*)\]`)

// LoadRepo reads <repo>/recap.yaml. A missing or unreadable file gives zero settings.
func LoadRepo(repo string) RepoSettings {
	var rs RepoSettings
	if repo == "" {
		return rs
	}
	b, err := os.ReadFile(filepath.Join(repo, "recap.yaml"))
	if err != nil {
		return rs
	}
	for _, l := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "prod_contexts:"):
			if m := listRe.FindStringSubmatch(t); m != nil {
				for _, p := range strings.Split(m[1], ",") {
					if p = strings.Trim(strings.TrimSpace(p), `"'`); p != "" {
						rs.ProdContexts = append(rs.ProdContexts, p)
					}
				}
			}
		case strings.HasPrefix(t, "apply_keeps_sessions:"):
			rs.ApplyKeepsSessions = strings.HasSuffix(t, "true")
		}
	}
	return rs
}

// ResolveSpace applies the CLI's space rules: explicit name > RECAP_SPACE
// (ignored when stale) > working directory paths > current (SPEC D-27).
func (c *Config) ResolveSpace(name, dir string) (Space, error) {
	if name == "" {
		name = os.Getenv("RECAP_SPACE")
	}
	if _, ok := c.Space(name); name != "" && !ok && name == os.Getenv("RECAP_SPACE") {
		name = "" // a stale env var should not break every command
	}
	return c.Resolve(name, dir)
}
