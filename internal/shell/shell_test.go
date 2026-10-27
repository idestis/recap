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

func TestScriptsMarkWrappedCalls(t *testing.T) {
	for _, s := range []string{zsh, bash} {
		if strings.Contains(strings.ReplaceAll(s, "RECAP_WRAPPED=1 command recap \"$@\"", ""), "command recap \"$@\"") {
			t.Fatal("every pass-through call must set RECAP_WRAPPED")
		}
	}
}
