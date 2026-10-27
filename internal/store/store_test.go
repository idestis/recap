package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/idestis/recap/internal/session"
)

// newStore opens a store under a fresh RECAP_HOME so tests never touch ~/.recap.
func newStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv("RECAP_HOME", t.TempDir())
	s, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func mustSave(t *testing.T, s *Store, handle string) {
	t.Helper()
	if err := s.SaveSession(&session.Session{ID: "ID-" + handle, Handle: handle}); err != nil {
		t.Fatalf("SaveSession(%s): %v", handle, err)
	}
}

func strPtr(s string) *string { return &s }

func TestOpenUsesRecapHomeAndCreatesSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECAP_HOME", home)

	s, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.Home != home {
		t.Errorf("Home = %q, want %q", s.Home, home)
	}
	fi, err := os.Stat(filepath.Join(home, "sessions"))
	if err != nil || !fi.IsDir() {
		t.Errorf("sessions dir not created: %v", err)
	}
}

func TestSaveLoadSessionRoundTrip(t *testing.T) {
	s := newStore(t)
	stopped := time.Date(2026, 10, 28, 12, 30, 0, 0, time.UTC)
	fixed := true
	in := &session.Session{
		ID:          "01HZX0000000000000000000AB",
		Handle:      "2026-10-28.1",
		Name:        "checkout-latency",
		NameSource:  "user",
		Description: "investigating p99",
		StartedAt:   time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC),
		StoppedAt:   &stopped,
		Links:       map[string]string{"dashboard": "https://example.test/d"},
		Tabs:        []string{"grafana", "slack"},
		Space:       "platform",
		Fixed:       &fixed,
	}
	if err := s.SaveSession(in); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	got, err := s.LoadSession(in.Handle)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("round trip mismatch:\n got  %+v\n want %+v", got, in)
	}
}

func TestSaveSessionOverwrites(t *testing.T) {
	s := newStore(t)
	mustSave(t, s, "2026-10-28.1")
	if err := s.SaveSession(&session.Session{ID: "x", Handle: "2026-10-28.1", Name: "renamed"}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	got, err := s.LoadSession("2026-10-28.1")
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got.Name != "renamed" {
		t.Errorf("Name = %q, want renamed", got.Name)
	}
}

func TestLoadSessionMissingErrors(t *testing.T) {
	s := newStore(t)
	for _, handle := range []string{"2026-10-28.1", "does-not-exist"} {
		if _, err := s.LoadSession(handle); err == nil {
			t.Errorf("LoadSession(%q) returned nil error", handle)
		}
	}
}

func TestNextHandle(t *testing.T) {
	day := time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)
	nextDay := day.AddDate(0, 0, 1)

	tests := []struct {
		name string
		seed []string // handles saved before asking
		at   time.Time
		want string
	}{
		{"empty store", nil, day, "2026-10-28.1"},
		{"one session today", []string{"2026-10-28.1"}, day, "2026-10-28.2"},
		{"three sessions today", []string{"2026-10-28.1", "2026-10-28.2", "2026-10-28.3"}, day, "2026-10-28.4"},
		{"new day ignores yesterday", []string{"2026-10-28.1", "2026-10-28.2"}, nextDay, "2026-10-29.1"},
		{"today counts only today", []string{"2026-10-27.1", "2026-10-27.2", "2026-10-28.1"}, day, "2026-10-28.2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			for _, h := range tt.seed {
				mustSave(t, s, h)
			}
			if got := s.NextHandle(tt.at); got != tt.want {
				t.Errorf("NextHandle = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestActiveSetActive(t *testing.T) {
	s := newStore(t)

	if got := s.Active(); got != "" {
		t.Errorf("Active on fresh store = %q, want empty", got)
	}
	if err := s.SetActive("2026-10-28.1"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if got := s.Active(); got != "2026-10-28.1" {
		t.Errorf("Active = %q, want 2026-10-28.1", got)
	}
	if err := s.SetActive("2026-10-28.2"); err != nil {
		t.Fatalf("SetActive second: %v", err)
	}
	if got := s.Active(); got != "2026-10-28.2" {
		t.Errorf("Active after replace = %q, want 2026-10-28.2", got)
	}
	if err := s.SetActive(""); err != nil {
		t.Fatalf("SetActive(\"\"): %v", err)
	}
	if got := s.Active(); got != "" {
		t.Errorf("Active after clear = %q, want empty", got)
	}
}

func TestSetActiveClearWhenUnsetIsNoop(t *testing.T) {
	s := newStore(t)
	if err := s.SetActive(""); err != nil {
		t.Errorf("SetActive(\"\") on unset store: %v", err)
	}
}

func TestLastStoppedSetLastStopped(t *testing.T) {
	s := newStore(t)

	if got := s.LastStopped(); got != "" {
		t.Errorf("LastStopped on fresh store = %q, want empty", got)
	}
	if err := s.SetLastStopped("2026-10-28.1"); err != nil {
		t.Fatalf("SetLastStopped: %v", err)
	}
	if err := s.SetLastStopped("2026-10-28.2"); err != nil {
		t.Fatalf("SetLastStopped second: %v", err)
	}
	if got := s.LastStopped(); got != "2026-10-28.2" {
		t.Errorf("LastStopped = %q, want 2026-10-28.2", got)
	}
}

func TestActiveAndLastStoppedAreIndependent(t *testing.T) {
	s := newStore(t)
	if err := s.SetActive("2026-10-28.1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLastStopped("2026-10-28.2"); err != nil {
		t.Fatal(err)
	}
	if got := s.Active(); got != "2026-10-28.1" {
		t.Errorf("Active = %q, want 2026-10-28.1", got)
	}
	if got := s.LastStopped(); got != "2026-10-28.2" {
		t.Errorf("LastStopped = %q, want 2026-10-28.2", got)
	}
}

func TestAppendEventsOrder(t *testing.T) {
	base := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	exit0 := 0
	chronological := []session.Event{
		{Time: base, Kind: "cmd", Source: "terminal", Text: "kubectl get pods", Exit: &exit0},
		{Time: base.Add(time.Second), Kind: "note", Source: "overlay", Text: "p99 spiking"},
		{Time: base.Add(2 * time.Second), Kind: "link", Source: "claude", Text: "https://example.test", LinkType: "dashboard", Ref: "abc"},
		{Time: base.Add(3 * time.Second), Kind: "file", Source: "ctx", Text: "shot.png", Tags: []string{"decision"}},
	}
	sameTime := []session.Event{
		{Time: base, Kind: "note", Text: "first"},
		{Time: base, Kind: "note", Text: "second"},
		{Time: base, Kind: "note", Text: "third"},
	}
	outOfOrder := []session.Event{
		{Time: base.Add(time.Minute), Kind: "note", Text: "later"},
		{Time: base, Kind: "note", Text: "earlier"},
	}
	tests := []struct {
		name   string
		append []session.Event
		want   []session.Event // expected order from Events
	}{
		{"chronological appends come back in order", chronological, chronological},
		{"same timestamp keeps append order", sameTime, sameTime},
		// Events sorts by time, so out-of-order appends come back chronologically.
		{"out of order appends come back sorted by time", outOfOrder, []session.Event{outOfOrder[1], outOfOrder[0]}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			mustSave(t, s, "2026-10-28.1")
			for _, ev := range tt.append {
				if err := s.AppendEvent("2026-10-28.1", ev); err != nil {
					t.Fatalf("AppendEvent: %v", err)
				}
			}
			got, err := s.Events("2026-10-28.1")
			if err != nil {
				t.Fatalf("Events: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d events, want %d", len(got), len(tt.want))
			}
			for i := range tt.want {
				if got[i].Text != tt.want[i].Text || !got[i].Time.Equal(tt.want[i].Time) {
					t.Errorf("event %d = %q @%v, want %q @%v", i, got[i].Text, got[i].Time, tt.want[i].Text, tt.want[i].Time)
				}
			}
		})
	}
}

func TestEventFieldsRoundTrip(t *testing.T) {
	s := newStore(t)
	mustSave(t, s, "2026-10-28.1")
	exit := 2
	in := session.Event{
		Time:     time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC),
		Kind:     "cmd",
		Source:   "terminal",
		Text:     "helm upgrade",
		Exit:     &exit,
		Dir:      "/srv/app",
		TTY:      "/dev/ttys001",
		LinkType: "pr",
		Ref:      "C0123",
		Tags:     []string{"decision"},
	}
	if err := s.AppendEvent("2026-10-28.1", in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Events("2026-10-28.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], in) {
		t.Errorf("round trip mismatch:\n got  %+v\n want %+v", got, in)
	}
}

func TestEventsNoneForEmptyOrUnknownSession(t *testing.T) {
	s := newStore(t)
	mustSave(t, s, "2026-10-28.1")
	for _, handle := range []string{"2026-10-28.1", "2026-10-28.9"} {
		got, err := s.Events(handle)
		if err != nil {
			t.Errorf("Events(%q): %v", handle, err)
		}
		if len(got) != 0 {
			t.Errorf("Events(%q) returned %d events, want 0", handle, len(got))
		}
	}
}

func TestFilesDir(t *testing.T) {
	s := newStore(t)
	want := filepath.Join(s.Home, "sessions", "2026-10-28.1", "files")
	if got := s.FilesDir("2026-10-28.1"); got != want {
		t.Errorf("FilesDir = %q, want %q", got, want)
	}
}

func TestKubeContext(t *testing.T) {
	tests := []struct {
		name    string
		content *string // kubeconfig body; nil leaves the file missing
		list    bool    // KUBECONFIG is a path list whose first entry is the file
		want    string
	}{
		{name: "plain", content: strPtr("apiVersion: v1\ncurrent-context: prod-eu\n"), want: "prod-eu"},
		{name: "double quoted", content: strPtr("current-context: \"staging\"\n"), want: "staging"},
		{name: "single quoted", content: strPtr("current-context: 'dev'\n"), want: "dev"},
		{name: "no current-context", content: strPtr("apiVersion: v1\nclusters: []\n"), want: ""},
		{name: "missing file", content: nil, want: ""},
		{name: "path list uses first entry", content: strPtr("current-context: first\n"), list: true, want: "first"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Keep HOME in a temp dir so the default ~/.kube/config is never read.
			t.Setenv("HOME", t.TempDir())
			dir := t.TempDir()
			path := filepath.Join(dir, "config")
			if tt.content != nil {
				if err := os.WriteFile(path, []byte(*tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				path = filepath.Join(dir, "missing")
			}
			kc := path
			if tt.list {
				kc = path + string(os.PathListSeparator) + filepath.Join(dir, "other")
			}
			t.Setenv("KUBECONFIG", kc)
			if got := KubeContext(); got != tt.want {
				t.Errorf("KubeContext = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKubeContextDefaultsToHomeKubeConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KUBECONFIG", "")
	if err := os.MkdirAll(filepath.Join(home, ".kube"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".kube", "config"), []byte("current-context: from-home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := KubeContext(); got != "from-home" {
		t.Errorf("KubeContext = %q, want from-home", got)
	}
}
