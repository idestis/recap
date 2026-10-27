package redact

import (
	"strings"
	"testing"
)

func TestRedactMasksSecrets(t *testing.T) {
	cases := map[string]string{
		"aws":      "export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
		"github":   "git clone https://ghp_abcdefghijklmnopqrstuvwxyz0123456789@github.com/acme/x",
		"slack":    "curl -H 'Authorization: Bearer xoxb-1234567890-abcdefghijkl'",
		"password": "psql postgres://app:S3cr3tPass@db:5432/app",
		"assign":   "PGPASSWORD=hunter2hunter2 psql -h db",
		"flag":     "vault login --token=s.AbCdEf123456",
		"entropy":  "curl -H 'X-Key: Zq8vN2rT6yW1pL4mK9sD3fG7hJ0bV5cXq8vN2rT6'",
	}
	for name, in := range cases {
		out := Redact(in)
		if out.Count == 0 || !strings.Contains(out.Text, "[REDACTED:") {
			t.Errorf("%s: nothing redacted in %q -> %q", name, in, out.Text)
		}
	}
}

func TestRedactKeepsUsefulContext(t *testing.T) {
	in := "kubectl set image deploy/api api=repo/api@sha256:3f1c9a8e7d6b5a4c3f1c9a8e7d6b5a4c3f1c9a8e7d6b5a4c3f1c9a8e7d6b5a4c -n checkout"
	out := Redact(in)
	if out.Count != 0 {
		t.Fatalf("hex digest should be kept, got %q", out.Text)
	}
}
