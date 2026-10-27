package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	got, n, err := Parse("testdata/sample.jsonl", 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 11 {
		t.Fatalf("lines = %d, want 11", n)
	}
	want := []struct{ kind, sub string }{
		{"prompt", "Pods in payments"},
		{"step", "Checking pod status"},
		{"step", "Restarting the deployment"},
		{"step", "Now verifying"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d intents: %+v", len(got), got)
	}
	for i, w := range want {
		if got[i].Kind != w.kind || !strings.Contains(got[i].Text, w.sub) {
			t.Errorf("%d: %+v, want %s %q", i, got[i], w.kind, w.sub)
		}
	}
	if strings.Contains(got[0].Text, "abcdef123456") {
		t.Errorf("secret not redacted: %q", got[0].Text)
	}
}

func TestParseFromCursor(t *testing.T) {
	all, _, _ := Parse("testdata/sample.jsonl", 0)
	// Resume at the last assistant line: its pending text came from an
	// earlier line and must still be found.
	got, _, _ := Parse("testdata/sample.jsonl", 10)
	if len(got) != 1 || got[0].Text != "Now verifying." {
		t.Fatalf("got %+v", got)
	}
	again, n, _ := Parse("testdata/sample.jsonl", 11)
	if len(again) != 0 || n != 11 || len(all) != 4 {
		t.Fatalf("expected nothing new, got %+v n=%d", again, n)
	}
}

func TestPartialLineLeftForNextCall(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	full := `{"type":"user","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(p, []byte(full+`{"type":"user","mess`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, n, err := Parse(p, 0)
	if err != nil || n != 1 || len(got) != 1 {
		t.Fatalf("got %+v n=%d err=%v", got, n, err)
	}
}

func TestTruncate(t *testing.T) {
	s := clean(strings.Repeat("é", 400), maxStep)
	if len([]rune(s)) != maxStep {
		t.Fatalf("len = %d", len([]rune(s)))
	}
}

func TestMissingFile(t *testing.T) {
	if _, _, err := Parse("testdata/nope.jsonl", 0); err == nil {
		t.Fatal("want error")
	}
}
