package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", "")
	rc := filepath.Join(home, ".zshrc")
	_ = os.WriteFile(rc, []byte("export FOO=1\n"), 0o644)
	if Installed("zsh") {
		t.Fatal("not installed yet")
	}
	for i := 0; i < 2; i++ {
		if _, _, err := Install("zsh"); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(rc)
	if strings.Count(string(b), "recap shell-init zsh") != 1 || !strings.HasPrefix(string(b), "export FOO=1\n") {
		t.Fatalf("rc file:\n%s", b)
	}
	if !Installed("zsh") {
		t.Fatal("should be installed")
	}
}

func TestFishScriptAndInstall(t *testing.T) {
	s, err := Script("fish")
	if err != nil || s == "" {
		t.Fatalf("fish script: %v", err)
	}
	for _, want := range []string{"RECAP_WRAPPED=1", "fish_preexec", "fish_postexec", "set -gx RECAP_SESSION", "set -e RECAP_SESSION"} {
		if !strings.Contains(s, want) {
			t.Fatalf("fish script missing %q", want)
		}
	}
	if strings.Contains(strings.ReplaceAll(s, "RECAP_WRAPPED=1 command recap $argv", ""), "command recap $argv") {
		t.Fatal("every pass-through call must set RECAP_WRAPPED")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	for i := 0; i < 2; i++ {
		if _, _, err := Install("fish"); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(home, ".config", "fish", "config.fish"))
	if strings.Count(string(b), "recap shell-init fish | source") != 1 || !Installed("fish") {
		t.Fatalf("config.fish:\n%s", b)
	}
	t.Setenv("SHELL", "/opt/homebrew/bin/fish")
	if Detect() != "fish" {
		t.Fatal("detect fish")
	}
}

func TestScriptsMarkWrappedCalls(t *testing.T) {
	for _, s := range []string{zsh, bash} {
		if strings.Contains(strings.ReplaceAll(s, "RECAP_WRAPPED=1 command recap \"$@\"", ""), "command recap \"$@\"") {
			t.Fatal("every pass-through call must set RECAP_WRAPPED")
		}
	}
}
