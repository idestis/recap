package store

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Cursor returns how many lines of an external source (e.g. a Claude Code
// transcript, keyed by path) were already processed for the session.
func (s *Store) Cursor(handle, key string) int {
	return s.cursors(handle)[key]
}

// SetCursor records the processed line count for key.
func (s *Store) SetCursor(handle, key string, n int) error {
	m := s.cursors(handle)
	m[key] = n
	return writeJSON(filepath.Join(s.dir(handle), "cursors.json"), m)
}

func (s *Store) cursors(handle string) map[string]int {
	m := map[string]int{}
	if b, err := os.ReadFile(filepath.Join(s.dir(handle), "cursors.json")); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}
