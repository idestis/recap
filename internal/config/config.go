// Package config is the user config at RECAP_HOME/config.yaml (default ~/.recap).
// One recap install serves many projects through spaces: each space is a recipe
// repo with its own publish strategy (team PR, direct push, local only).
// Secrets never go here (SPEC D-15).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Publish strategies for a space (SPEC D-27).
const (
	PublishPR   = "pr"   // branch + pull request (team repo)
	PublishPush = "push" // commit + push to the base branch (private repo)
	PublishNone = "none" // write files only
)

// Model selects an OpenAI-compatible endpoint (SPEC D-13). Ollama exposes the
// same API under /v1, so one client covers local and hosted models.
type Model struct {
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty"` // ollama | openai | none
	BaseURL  string `yaml:"base_url,omitempty" json:"base_url,omitempty"`
	Name     string `yaml:"model,omitempty" json:"model,omitempty"`
	KeyEnv   string `yaml:"api_key_env,omitempty" json:"api_key_env,omitempty"` // env var read before the Keychain
	TimeoutS int    `yaml:"timeout_seconds,omitempty" json:"timeout_seconds,omitempty"`
}

// Enabled reports whether drafting should call a model.
func (m Model) Enabled() bool { return m.Provider != "" && m.Provider != "none" && m.Name != "" }

type Space struct {
	Name    string   `yaml:"name" json:"name"`
	Repo    string   `yaml:"repo" json:"repo"`
	Remote  string   `yaml:"remote,omitempty" json:"remote,omitempty"`
	Publish string   `yaml:"publish,omitempty" json:"publish"`
	Base    string   `yaml:"base,omitempty" json:"base,omitempty"`
	Paths   []string `yaml:"paths,omitempty" json:"paths,omitempty"` // working dirs that select this space
	Model   *Model   `yaml:"model,omitempty" json:"model,omitempty"` // overrides the global model
}

type Config struct {
	Current   string  `yaml:"current,omitempty"`
	Guardrail *bool   `yaml:"guardrail,omitempty"`
	Model     Model   `yaml:"model,omitempty"`
	Embedding Model   `yaml:"embedding,omitempty"` // embedding model for dedup (SPEC D-14)
	Spaces    []Space `yaml:"spaces,omitempty"`

	path string
}

// Home resolves RECAP_HOME (default ~/.recap).
func Home() (string, error) {
	if h := os.Getenv("RECAP_HOME"); h != "" {
		return h, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".recap"), nil
}

func Load(home string) (*Config, error) {
	c := &Config{path: filepath.Join(home, "config.yaml")}
	b, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("%s: %w", c.path, err)
	}
	return c, nil
}

func (c *Config) Save() error {
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

func (c *Config) Path() string { return c.path }

// GuardrailOn defaults to true.
func (c *Config) GuardrailOn() bool { return c.Guardrail == nil || *c.Guardrail }

func (c *Config) Space(name string) (*Space, bool) {
	for i := range c.Spaces {
		if c.Spaces[i].Name == name {
			return &c.Spaces[i], true
		}
	}
	return nil, false
}

// Upsert adds or replaces a space by name, keeping list order stable.
func (c *Config) Upsert(s Space) {
	if s.Publish == "" {
		s.Publish = PublishNone
	}
	if cur, ok := c.Space(s.Name); ok {
		*cur = s
	} else {
		c.Spaces = append(c.Spaces, s)
	}
	if c.Current == "" {
		c.Current = s.Name
	}
}

func (c *Config) Remove(name string) bool {
	for i := range c.Spaces {
		if c.Spaces[i].Name == name {
			c.Spaces = append(c.Spaces[:i], c.Spaces[i+1:]...)
			if c.Current == name {
				c.Current = ""
				if len(c.Spaces) > 0 {
					c.Current = c.Spaces[0].Name
				}
			}
			return true
		}
	}
	return false
}

// Resolve picks the space in priority order: explicit name (flag, RECAP_SPACE,
// the session's own space) > longest matching path for dir > current.
// RECAP_REPO overrides the repo for throwaway testing.
func (c *Config) Resolve(name, dir string) (Space, error) {
	var s Space
	switch {
	case name != "":
		p, ok := c.Space(name)
		if !ok {
			return s, fmt.Errorf("no space %q — see: recap spaces", name)
		}
		s = *p
	default:
		if p := c.byPath(dir); p != nil {
			s = *p
		} else if p, ok := c.Space(c.Current); ok {
			s = *p
		} else if len(c.Spaces) > 0 {
			s = c.Spaces[0]
		}
	}
	if env := os.Getenv("RECAP_REPO"); env != "" {
		s.Repo = env
		if s.Name == "" {
			s.Name = "env"
		}
	}
	s.Repo = Expand(s.Repo)
	return s, nil
}

func (c *Config) byPath(dir string) *Space {
	if dir == "" {
		return nil
	}
	dir = filepath.Clean(dir)
	var best *Space
	bestLen := -1
	for i := range c.Spaces {
		for _, p := range c.Spaces[i].Paths {
			p = filepath.Clean(Expand(p))
			if (dir == p || strings.HasPrefix(dir, p+string(filepath.Separator))) && len(p) > bestLen {
				best, bestLen = &c.Spaces[i], len(p)
			}
		}
	}
	return best
}

// ModelFor returns the effective model for a space.
func (c *Config) ModelFor(s Space) Model {
	if s.Model != nil && s.Model.Provider != "" {
		return *s.Model
	}
	return c.Model
}

// Names returns space names sorted for stable output.
func (c *Config) Names() []string {
	var n []string
	for _, s := range c.Spaces {
		n = append(n, s.Name)
	}
	sort.Strings(n)
	return n
}

// Expand resolves a leading ~.
func Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
