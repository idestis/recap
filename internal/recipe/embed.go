package recipe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/idestis/recap/internal/config"
	"github.com/idestis/recap/internal/llm"
	"github.com/idestis/recap/internal/redact"
)

// EmbeddingOptions enables embedding dedup when an embedding model is
// configured and its client can be built. Otherwise it returns the zero value,
// which keeps the token-overlap path (SPEC D-14). The cache lives under home.
func EmbeddingOptions(m config.Model, home string) DedupOptions {
	if !m.Enabled() {
		return DedupOptions{}
	}
	cl, err := llm.New(m)
	if err != nil {
		return DedupOptions{}
	}
	return DedupOptions{Embedder: cl, Model: m.Name, CacheFile: filepath.Join(home, "embeddings.json"), Timeout: cl.HTTP.Timeout}
}

// EmbedMatchThreshold is the cosine similarity at which a draft becomes a
// proposed variant of an existing recipe (SPEC D-14). Recipes that fix the same
// problem (same tool, same environment class) usually score 0.75-0.9 with
// nomic-embed-text and 0.6-0.8 with OpenAI text-embedding-3-*; unrelated recipes
// that share a tool land around 0.3-0.55. A wrong match only proposes a variant
// that a person reviews, while a miss creates a near-duplicate, so the cut sits
// just above the unrelated band and below the related one.
const EmbedMatchThreshold = 0.65

// maxEmbedChars keeps inputs inside small embedding context windows
// (nomic-embed-text: 2048 tokens).
const maxEmbedChars = 4000

// Embedder turns redacted text into vectors. *llm.Client satisfies it.
type Embedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
}

// DedupOptions enables the embedding path. The zero value uses token overlap.
type DedupOptions struct {
	Embedder  Embedder
	Model     string // embedding model name, part of the cache key
	CacheFile string // local cache under RECAP_HOME, never in the repo; "" disables caching
	Timeout   time.Duration
}

// Cosine returns the cosine similarity of two vectors, 0 for mismatched or
// empty input.
func Cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// embedText redacts first (invariant 1) and bounds the length.
func embedText(s string) string {
	s = redact.Redact(s).Text
	if len(s) > maxEmbedChars {
		s = s[:maxEmbedChars]
	}
	return s
}

type cand struct {
	m    Match
	text string
}

// embedDedup picks the best candidate by cosine similarity. ok is false on any
// embedding problem so the caller falls back to token overlap.
func embedDedup(opt DedupOptions, draft string, cands []cand) (best *Match, ok bool) {
	if len(cands) == 0 {
		return nil, true
	}
	cache := loadCache(opt.CacheFile)
	keys := make([]string, len(cands))
	vecs := make([][]float32, len(cands))
	texts := []string{embedText(draft)}
	var need []int
	for i, c := range cands {
		t := embedText(c.text)
		id := c.m.ID
		if id == "" {
			id = filepath.Base(c.m.Path)
		}
		keys[i] = cacheKey(opt.Model, id, t)
		if v, hit := cache.get(keys[i]); hit {
			vecs[i] = v
			continue
		}
		need = append(need, i)
		texts = append(texts, t)
	}
	timeout := opt.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	got, err := opt.Embedder.Embed(ctx, texts)
	if err != nil || len(got) != len(texts) {
		return nil, false
	}
	for j, i := range need {
		vecs[i] = got[j+1]
		cache.put(keys[i], got[j+1])
	}
	if len(need) > 0 {
		cache.save()
	}
	for i, c := range cands {
		m := c.m
		m.Score = Cosine(got[0], vecs[i])
		if m.Score >= EmbedMatchThreshold && (best == nil || m.Score > best.Score) {
			mm := m
			best = &mm
		}
	}
	return best, true
}

func cacheKey(model, id, text string) string {
	h := sha256.Sum256([]byte(text))
	return model + "\x00" + id + "\x00" + hex.EncodeToString(h[:8])
}

// embedCache is a small JSON file of vectors keyed by model, recipe id and
// content hash. It lives under RECAP_HOME, never in Git (invariant 5). Only
// keys used in the latest run are kept on save, so deleted or edited recipes
// do not accumulate.
type embedCache struct {
	path string
	Vecs map[string][]float32 `json:"vectors"`
	used map[string]bool
}

func loadCache(path string) *embedCache {
	c := &embedCache{path: path, Vecs: map[string][]float32{}, used: map[string]bool{}}
	if path == "" {
		return c
	}
	if b, err := os.ReadFile(path); err == nil {
		var on embedCache
		if json.Unmarshal(b, &on) == nil && on.Vecs != nil {
			c.Vecs = on.Vecs
		}
	}
	return c
}

func (c *embedCache) get(k string) ([]float32, bool) {
	v, ok := c.Vecs[k]
	if ok {
		c.used[k] = true
	}
	return v, ok
}

func (c *embedCache) put(k string, v []float32) {
	c.Vecs[k] = v
	c.used[k] = true
}

// save is best effort: a cache that cannot be written only costs a re-embed.
func (c *embedCache) save() {
	if c.path == "" {
		return
	}
	keep := map[string][]float32{}
	for k := range c.used {
		keep[k] = c.Vecs[k]
	}
	b, err := json.Marshal(embedCache{Vecs: keep})
	if err != nil || os.MkdirAll(filepath.Dir(c.path), 0o700) != nil {
		return
	}
	tmp := c.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, c.path)
	}
}
