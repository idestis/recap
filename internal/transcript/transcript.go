// Package transcript extracts intent from a Claude Code transcript (JSONL):
// the user's prompts and the assistant text that precedes each Bash step
// (SPEC D-18). Output is redacted here, before anything is stored (D-11).
package transcript

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/idestis/recap/internal/redact"
)

const (
	maxStep   = 280
	maxPrompt = 500
)

// Intent is one recorded piece of intent. Kind is "prompt" or "step".
type Intent struct {
	Time time.Time
	Kind string
	Text string
	Line int // zero-based index of the transcript line that produced it
}

type line struct {
	Type        string    `json:"type"`
	IsMeta      bool      `json:"isMeta"`
	IsSidechain bool      `json:"isSidechain"`
	Timestamp   time.Time `json:"timestamp"`
	Message     struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Name  string `json:"name"`
	Input struct {
		Command string `json:"command"`
	} `json:"input"`
}

// Parse reads the transcript at path and returns intents from lines at index
// >= from, plus the number of complete lines consumed (the next cursor). The
// whole file is scanned so that text preceding a step is still known when the
// previous call stopped mid-turn. A trailing line without a newline is left
// for the next call, since Claude Code may still be writing it.
func Parse(path string, from int) ([]Intent, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, from, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	var out []Intent
	var pending string
	n := 0
	for {
		raw, err := r.ReadBytes('\n')
		if len(raw) == 0 || raw[len(raw)-1] != '\n' {
			break // EOF, possibly a partial line
		}
		idx := n
		n++
		if len(raw) > 4<<20 {
			continue
		}
		var l line
		if json.Unmarshal(raw, &l) != nil || l.IsSidechain || l.IsMeta {
			continue
		}
		emit := func(kind, text string) {
			if idx < from || text == "" {
				return
			}
			out = append(out, Intent{Time: l.Timestamp, Kind: kind, Text: text, Line: idx})
		}
		switch l.Type {
		case "user":
			if p := userPrompt(l.Message.Content); p != "" {
				pending = ""
				emit("prompt", clean(p, maxPrompt))
			}
		case "assistant":
			for _, b := range blocks(l.Message.Content) {
				switch {
				case b.Type == "text" && strings.TrimSpace(b.Text) != "":
					pending = b.Text
				case b.Type == "tool_use" && b.Name == "Bash":
					emit("step", clean(pending, maxStep))
					pending = ""
				case b.Type == "tool_use":
					pending = ""
				}
			}
		}
		if err != nil {
			break
		}
	}
	return out, n, nil
}

// userPrompt returns the text of a real user prompt, or "" for tool results,
// slash-command wrappers and other injected content.
func userPrompt(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return injected(s)
	}
	var text []string
	for _, b := range blocks(raw) {
		if b.Type == "text" {
			text = append(text, b.Text)
		}
	}
	return injected(strings.Join(text, "\n"))
}

func injected(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "<") || strings.HasPrefix(s, "[Request interrupted") {
		return ""
	}
	return s
}

func blocks(raw json.RawMessage) []block {
	var bs []block
	_ = json.Unmarshal(raw, &bs)
	return bs
}

// clean redacts, collapses whitespace and truncates on a rune boundary.
func clean(s string, max int) string {
	s = strings.Join(strings.Fields(redact.Redact(s).Text), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max-1]) + "…"
	}
	return s
}
