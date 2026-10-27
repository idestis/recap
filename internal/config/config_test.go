package config

import (
	"path/filepath"
	"testing"
)

func TestResolve(t *testing.T) {
	t.Setenv("RECAP_REPO", "")
	c := &Config{Current: "me", Spaces: []Space{
		{Name: "me", Repo: "/r/me"},
		{Name: "team", Repo: "/r/team", Paths: []string{"/code/acme"}},
		{Name: "pay", Repo: "/r/pay", Paths: []string{"/code/acme/payments"}},
	}}
	cases := []struct{ name, dir, want string }{
		{"", "/elsewhere", "me"},
		{"", "/code/acme", "team"},
		{"", "/code/acme/api", "team"},
		{"", "/code/acme/payments/svc", "pay"},
		{"", "/code/acmex", "me"},
		{"team", "/code/acme/payments", "team"},
	}
	for _, tc := range cases {
		s, err := c.Resolve(tc.name, tc.dir)
		if err != nil || s.Name != tc.want {
			t.Errorf("Resolve(%q,%q) = %q, %v; want %q", tc.name, tc.dir, s.Name, err, tc.want)
		}
	}
	if _, err := c.Resolve("nope", ""); err == nil {
		t.Error("unknown space should error")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	home := t.TempDir()
	c, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	c.Upsert(Space{Name: "team", Repo: "~/runbooks", Publish: PublishPR, Base: "main"})
	c.Model = Model{Provider: "ollama", BaseURL: "http://localhost:11434/v1", Name: "llama3.2"}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if got.Current != "team" || len(got.Spaces) != 1 || got.Spaces[0].Publish != PublishPR || got.Model.Name != "llama3.2" {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if got.Path() != filepath.Join(home, "config.yaml") {
		t.Fatalf("path %s", got.Path())
	}
}
