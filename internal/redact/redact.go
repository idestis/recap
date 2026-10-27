// Package redact strips secrets locally, before any model call or Git write
// (SPEC D-11, invariant 1). It never relies on a model.
package redact

import (
	"math"
	"regexp"
	"strings"
)

type rule struct {
	name string
	re   *regexp.Regexp
	// group to replace; 0 = whole match
	group int
}

var rules = []rule{
	{"private-key", regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), 0},
	{"aws-key", regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`), 0},
	{"github-token", regexp.MustCompile(`\b(ghp|gho|ghu|ghs|ghr|github_pat)_[A-Za-z0-9_]{20,}\b`), 0},
	{"slack-token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}\b`), 0},
	{"api-key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`), 0},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`), 0},
	{"bearer", regexp.MustCompile(`(?i)\bbearer\s+([A-Za-z0-9._~+/=-]{12,})`), 1},
	{"url-credentials", regexp.MustCompile(`://[^/\s:@]+:([^@\s/]+)@`), 1},
	{"assignment", regexp.MustCompile(`(?i)\b[a-z_]*(?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key)\s*[=:]\s*["']?([^\s"'&]+)`), 1},
	{"flag-secret", regexp.MustCompile(`(?i)--(?:password|token|secret|api-key)[= ]["']?([^\s"']+)`), 1},
}

var candidate = regexp.MustCompile(`[A-Za-z0-9+/_=-]{32,}`)

// Result reports what was masked.
type Result struct {
	Text  string
	Count int
	Kinds []string
}

// Redact masks secrets in s.
func Redact(s string) Result {
	r := Result{Text: s}
	for _, ru := range rules {
		r.Text = ru.re.ReplaceAllStringFunc(r.Text, func(m string) string {
			if ru.group == 0 {
				r.add(ru.name)
				return "[REDACTED:" + ru.name + "]"
			}
			sub := ru.re.FindStringSubmatch(m)
			if len(sub) <= ru.group || sub[ru.group] == "" || strings.HasPrefix(sub[ru.group], "[REDACTED") {
				return m
			}
			r.add(ru.name)
			return strings.Replace(m, sub[ru.group], "[REDACTED:"+ru.name+"]", 1)
		})
	}
	// High-entropy leftovers (random tokens with no known prefix).
	r.Text = candidate.ReplaceAllStringFunc(r.Text, func(m string) string {
		if strings.Contains(m, "REDACTED") || looksLikeHash(m) || entropy(m) < 4.2 {
			return m
		}
		r.add("high-entropy")
		return "[REDACTED:high-entropy]"
	})
	return r
}

func (r *Result) add(kind string) {
	r.Count++
	for _, k := range r.Kinds {
		if k == kind {
			return
		}
	}
	r.Kinds = append(r.Kinds, kind)
}

// Git SHAs and image digests are hex and useful context; keep them.
func looksLikeHash(s string) bool {
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func entropy(s string) float64 {
	freq := map[rune]float64{}
	for _, c := range s {
		freq[c]++
	}
	var h float64
	n := float64(len(s))
	for _, f := range freq {
		p := f / n
		h -= p * math.Log2(p)
	}
	return h
}
