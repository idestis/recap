package recipe

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idestis/recap/internal/config"
	"github.com/idestis/recap/internal/llm"
	"github.com/idestis/recap/internal/risk"
)

func TestCosine(t *testing.T) {
	cases := []struct {
		a, b []float32
		want float64
	}{
		{[]float32{1, 0}, []float32{1, 0}, 1},
		{[]float32{1, 0}, []float32{0, 1}, 0},
		{[]float32{1, 0}, []float32{-1, 0}, -1},
		{[]float32{3, 4}, []float32{6, 8}, 1},
		{[]float32{1, 1}, []float32{1, 0}, math.Sqrt2 / 2},
		{[]float32{0, 0}, []float32{1, 0}, 0},
		{[]float32{1}, []float32{1, 0}, 0},
		{nil, nil, 0},
	}
	for _, c := range cases {
		if got := Cosine(c.a, c.b); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Cosine(%v,%v)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func writeRecipe(t *testing.T, repo, id, riskLabel, body string) {
	t.Helper()
	dir := filepath.Join(repo, "recipes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "---\nid: " + id + "\ntitle: " + id + "\nrisk: " + riskLabel + "\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

func draft() *Recipe {
	return &Recipe{
		ID: "d", Title: "pods crash looping", Situation: "pods restart", Tools: []string{"kubectl"},
		Steps: []Step{{Title: "restart", Cmd: "kubectl rollout restart deploy/api", Risk: risk.Result{}}},
	}
}

// fakeEmbedder maps text to a vector by keyword so tests control similarity.
type fakeEmbedder struct {
	calls  int
	inputs []string
	err    error
}

func (f *fakeEmbedder) Embed(_ context.Context, in []string) ([][]float32, error) {
	f.calls++
	f.inputs = append(f.inputs, in...)
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]float32, len(in))
	for i, s := range in {
		switch {
		case strings.Contains(s, "crash"):
			out[i] = []float32{1, 0.1}
		case strings.Contains(s, "cert"):
			out[i] = []float32{0, 1}
		default:
			out[i] = []float32{0.5, 0.5}
		}
	}
	return out, nil
}

func TestDedupEmbeddingsPicksByMeaning(t *testing.T) {
	repo := t.TempDir()
	writeRecipe(t, repo, "crash-fix", "safe", "kubectl crash loop fix")
	writeRecipe(t, repo, "cert-fix", "safe", "kubectl cert renewal")
	m, err := DedupWith(repo, draft(), DedupOptions{Embedder: &fakeEmbedder{}, Model: "e"})
	if err != nil || m == nil || m.ID != "crash-fix" {
		t.Fatalf("match %+v err %v", m, err)
	}
	if m.Score < EmbedMatchThreshold {
		t.Errorf("score %v", m.Score)
	}
}

func TestDedupEmbeddingsStillGated(t *testing.T) {
	repo := t.TempDir()
	// Semantically identical, but prod vs non-prod and a different tool.
	writeRecipe(t, repo, "prod-crash", "high-prod", "kubectl crash loop fix")
	writeRecipe(t, repo, "other-tool", "safe", "terraform crash loop fix")
	f := &fakeEmbedder{}
	m, err := DedupWith(repo, draft(), DedupOptions{Embedder: f, Model: "e"})
	if err != nil || m != nil {
		t.Fatalf("expected no match, got %+v err %v", m, err)
	}
	if f.calls != 0 {
		t.Errorf("gated-out recipes must not be embedded, calls=%d", f.calls)
	}
}

func TestDedupEmbeddingsBelowThreshold(t *testing.T) {
	repo := t.TempDir()
	writeRecipe(t, repo, "cert-fix", "safe", "kubectl cert renewal")
	m, _ := DedupWith(repo, draft(), DedupOptions{Embedder: &fakeEmbedder{}, Model: "e"})
	if m != nil {
		t.Fatalf("unexpected match %+v", m)
	}
}

func TestDedupFallsBackToTokens(t *testing.T) {
	repo := t.TempDir()
	// Shares many tokens with the draft so jaccard matches.
	writeRecipe(t, repo, "tok", "safe", "pods crash looping pods restart kubectl rollout restart deploy api restart")
	for name, opt := range map[string]DedupOptions{
		"no embedder": {},
		"error":       {Embedder: &fakeEmbedder{err: errors.New("down")}, Model: "e"},
	} {
		m, err := DedupWith(repo, draft(), opt)
		if err != nil || m == nil || m.ID != "tok" {
			t.Errorf("%s: match %+v err %v", name, m, err)
		}
	}
}

func TestEmbedCacheAndRedaction(t *testing.T) {
	repo := t.TempDir()
	writeRecipe(t, repo, "crash-fix", "safe", "kubectl crash loop fix token=AKIAIOSFODNN7EXAMPLE")
	cache := filepath.Join(t.TempDir(), "embeddings.json")
	f := &fakeEmbedder{}
	opt := DedupOptions{Embedder: f, Model: "e", CacheFile: cache}
	if _, err := DedupWith(repo, draft(), opt); err != nil {
		t.Fatal(err)
	}
	for _, in := range f.inputs {
		if strings.Contains(in, "AKIAIOSFODNN7EXAMPLE") {
			t.Fatalf("unredacted text sent to the embedder: %q", in)
		}
	}
	if len(f.inputs) != 2 {
		t.Fatalf("first run embeds draft + recipe, got %d", len(f.inputs))
	}
	// Second run: the recipe comes from the cache, only the draft is embedded.
	f2 := &fakeEmbedder{}
	opt.Embedder = f2
	if m, _ := DedupWith(repo, draft(), opt); m == nil {
		t.Fatal("cached run lost the match")
	}
	if len(f2.inputs) != 1 {
		t.Errorf("expected 1 embedding on cached run, got %d", len(f2.inputs))
	}
	// Changed content invalidates the entry.
	writeRecipe(t, repo, "crash-fix", "safe", "kubectl crash loop fix, edited")
	f3 := &fakeEmbedder{}
	opt.Embedder = f3
	_, _ = DedupWith(repo, draft(), opt)
	if len(f3.inputs) != 2 {
		t.Errorf("edited recipe should be re-embedded, got %d", len(f3.inputs))
	}
}

func TestDedupHTTPFakeServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Input []string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		var data []map[string]any
		for i, s := range req.Input {
			v := []float32{0, 1}
			if strings.Contains(s, "crash") {
				v = []float32{1, 0}
			}
			data = append(data, map[string]any{"index": i, "embedding": v})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()
	t.Setenv("RECAP_API_KEY", "k")
	cl, err := llm.New(config.Model{Provider: "openai", BaseURL: srv.URL + "/v1", Name: "e"})
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	writeRecipe(t, repo, "crash-fix", "safe", "kubectl crash loop fix")
	m, err := DedupWith(repo, draft(), DedupOptions{Embedder: cl, Model: "e"})
	if err != nil || m == nil || m.ID != "crash-fix" {
		t.Fatalf("match %+v err %v", m, err)
	}
}
