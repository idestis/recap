// Package store is the prototype's local store: plain files under RECAP_HOME.
// The MVP replaces this with the recapd daemon + SQLite (SPEC D-03), keeping
// the same functions so the CLI does not change.
package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/idestis/recap/internal/config"
	"github.com/idestis/recap/internal/session"
)

type Store struct{ Home string }

// Open resolves RECAP_HOME (default ~/.recap). Config lives next to the
// sessions in config.yaml (internal/config).
func Open() (*Store, error) {
	home, err := config.Home()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o700); err != nil {
		return nil, err
	}
	return &Store{Home: home}, nil
}

func (s *Store) dir(handle string) string { return filepath.Join(s.Home, "sessions", handle) }

// FilesDir is where attached files and screenshots are kept (never in Git).
func (s *Store) FilesDir(handle string) string { return filepath.Join(s.dir(handle), "files") }

func (s *Store) SaveSession(se *session.Session) error {
	if err := os.MkdirAll(s.dir(se.Handle), 0o700); err != nil {
		return err
	}
	return writeJSON(filepath.Join(s.dir(se.Handle), "session.json"), se)
}

func (s *Store) LoadSession(handle string) (*session.Session, error) {
	b, err := os.ReadFile(filepath.Join(s.dir(handle), "session.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no session %q", handle)
	}
	if err != nil {
		return nil, err
	}
	var se session.Session
	return &se, json.Unmarshal(b, &se)
}

// NextHandle returns today's next CalVer handle.
func (s *Store) NextHandle(t time.Time) string {
	prefix := t.Format("2006-01-02") + "."
	n := 0
	entries, _ := os.ReadDir(filepath.Join(s.Home, "sessions"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			n++
		}
	}
	return session.Handle(t, n+1)
}

// Active returns the active session handle, or "".
func (s *Store) Active() string {
	b, err := os.ReadFile(filepath.Join(s.Home, "active"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (s *Store) SetActive(handle string) error {
	p := filepath.Join(s.Home, "active")
	if handle == "" {
		err := os.Remove(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return os.WriteFile(p, []byte(handle+"\n"), 0o600)
}

// LastStopped is the most recently stopped session, for `recap publish`.
func (s *Store) LastStopped() string {
	b, err := os.ReadFile(filepath.Join(s.Home, "last"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (s *Store) SetLastStopped(handle string) error {
	return os.WriteFile(filepath.Join(s.Home, "last"), []byte(handle+"\n"), 0o600)
}

func (s *Store) AppendEvent(handle string, ev session.Event) error {
	f, err := os.OpenFile(filepath.Join(s.dir(handle), "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func (s *Store) Events(handle string) ([]session.Event, error) {
	f, err := os.Open(filepath.Join(s.dir(handle), "events.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []session.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 4*1024*1024)
	for sc.Scan() {
		var ev session.Event
		if json.Unmarshal(sc.Bytes(), &ev) == nil {
			out = append(out, ev)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out, sc.Err()
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// KubeContext reads current-context from the kubeconfig without running kubectl,
// so `recap start` stays instant and offline (SPEC D-05).
func KubeContext() string {
	path := os.Getenv("KUBECONFIG")
	if path == "" {
		h, _ := os.UserHomeDir()
		path = filepath.Join(h, ".kube", "config")
	}
	path = strings.Split(path, string(os.PathListSeparator))[0]
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "current-context:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "current-context:")), `"'`)
		}
	}
	return ""
}
