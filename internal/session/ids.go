// Package session holds the session model, IDs, handles and naming rules.
package session

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewULID returns a 26-char ULID: 48-bit ms timestamp + 80 random bits.
// Unique without a server and sortable by time (SPEC D-07).
func NewULID(t time.Time) string {
	var b [16]byte
	ms := uint64(t.UnixMilli())
	for i := 5; i >= 0; i-- {
		b[i] = byte(ms)
		ms >>= 8
	}
	if _, err := rand.Read(b[6:]); err != nil {
		panic(err)
	}
	// 128 bits -> 26 chars (first char carries 3 bits).
	out := make([]byte, 26)
	var acc uint64
	bits := 0
	idx := 25
	for i := 15; i >= 0; i-- {
		acc |= uint64(b[i]) << bits
		bits += 8
		for bits >= 5 && idx >= 0 {
			out[idx] = crockford[acc&31]
			acc >>= 5
			bits -= 5
			idx--
		}
	}
	for idx >= 0 {
		out[idx] = crockford[acc&31]
		acc >>= 5
		idx--
	}
	return string(out)
}

// Handle builds the CalVer handle "YYYY-MM-DD.N" (SPEC D-07).
func Handle(t time.Time, n int) string {
	return fmt.Sprintf("%s.%d", t.Format("2006-01-02"), n)
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// Slug makes a short, file-safe name from free text: at most 5 words, 40 chars.
func Slug(s string) string {
	s = strings.ToLower(s)
	s = nonAlnum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	parts := strings.Split(s, "-")
	if len(parts) > 5 {
		parts = parts[:5]
	}
	s = strings.Join(parts, "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		s = "session"
	}
	return s
}

var incChannel = regexp.MustCompile(`^inc-?(\d+)-?(.*)$`)

// SlackChannelName turns "#inc-482-checkout-latency" into name
// "checkout-latency" and external incident id "INC-482" (SPEC D-08).
func SlackChannelName(ch string) (name, incident string) {
	ch = strings.TrimPrefix(strings.ToLower(ch), "#")
	if m := incChannel.FindStringSubmatch(ch); m != nil {
		if m[2] != "" {
			name = Slug(m[2])
		}
		return name, "INC-" + m[1]
	}
	return Slug(ch), ""
}

// Name sources in priority order (SPEC D-08). Higher wins.
var namePriority = map[string]int{"start": 1, "llm": 2, "slack": 3, "user": 4}

// CanRename reports whether a name from source `from` may replace the current one.
func CanRename(current, from string) bool {
	return namePriority[from] >= namePriority[current]
}
