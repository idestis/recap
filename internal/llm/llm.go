// Package llm is a minimal OpenAI-compatible chat client (SPEC D-13). It talks
// to OpenAI, any compatible host, and Ollama (which serves the same API under
// /v1). Callers must pass redacted text only (SPEC D-11).
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/idestis/recap/internal/config"
)

// Default endpoints per provider.
var defaultBase = map[string]string{
	"ollama": "http://localhost:11434/v1",
	"openai": "https://api.openai.com/v1",
}

// DefaultBaseURL returns the base URL used when none is configured.
func DefaultBaseURL(provider string) string { return defaultBase[provider] }

type Client struct {
	BaseURL string
	Model   string
	Key     string
	HTTP    *http.Client
}

// New builds a client from config. The API key comes from the configured env
// var, then RECAP_API_KEY, then the Keychain (SPEC D-15). Ollama needs none.
func New(m config.Model) (*Client, error) {
	if !m.Enabled() {
		return nil, errors.New("no model configured — run: recap models")
	}
	base := m.BaseURL
	if base == "" {
		base = defaultBase[m.Provider]
	}
	if base == "" {
		return nil, fmt.Errorf("provider %q needs --base-url", m.Provider)
	}
	timeout := time.Duration(m.TimeoutS) * time.Second
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	c := &Client{BaseURL: strings.TrimRight(base, "/"), Model: m.Name, HTTP: &http.Client{Timeout: timeout}}
	if m.KeyEnv != "" {
		c.Key = os.Getenv(m.KeyEnv)
	}
	if c.Key == "" {
		c.Key = os.Getenv("RECAP_API_KEY")
	}
	if c.Key == "" && m.Provider != "ollama" {
		k, err := GetKey(KeyAccount(m))
		if err != nil {
			return nil, fmt.Errorf("no API key for %s — run: recap models key set: %w", m.Provider, err)
		}
		c.Key = k
	}
	return c, nil
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string            `json:"model"`
	Messages       []Message         `json:"messages"`
	Temperature    float64           `json:"temperature"`
	ResponseFormat map[string]string `json:"response_format,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat sends messages and returns the first reply.
func (c *Client) Chat(ctx context.Context, msgs []Message, jsonMode bool) (string, error) {
	req := chatRequest{Model: c.Model, Messages: msgs, Temperature: 0}
	if jsonMode {
		req.ResponseFormat = map[string]string{"type": "json_object"}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	hr.Header.Set("Content-Type", "application/json")
	if c.Key != "" {
		hr.Header.Set("Authorization", "Bearer "+c.Key)
	}
	resp, err := c.HTTP.Do(hr)
	if err != nil {
		return "", fmt.Errorf("model request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("model response (%s): %s", resp.Status, truncate(string(raw), 200))
	}
	if out.Error != nil {
		return "", fmt.Errorf("model error (%s): %s", resp.Status, out.Error.Message)
	}
	if resp.StatusCode >= 300 || len(out.Choices) == 0 {
		return "", fmt.Errorf("model response %s: %s", resp.Status, truncate(string(raw), 200))
	}
	return out.Choices[0].Message.Content, nil
}

// JSON asks for a JSON object and decodes it into v. Models sometimes wrap
// JSON in prose or code fences, so the outermost object is extracted first.
func (c *Client) JSON(ctx context.Context, system, user string, v any) error {
	s, err := c.Chat(ctx, []Message{{Role: "system", Content: system}, {Role: "user", Content: user}}, true)
	if err != nil {
		return err
	}
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		s = s[i : j+1]
	}
	if err := json.Unmarshal([]byte(s), v); err != nil {
		return fmt.Errorf("model returned invalid JSON: %w", err)
	}
	return nil
}

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Embed returns one vector per input from POST /embeddings (OpenAI, Ollama
// under /v1, and compatible hosts). Callers must pass redacted text only
// (SPEC D-11).
func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(embedRequest{Model: c.Model, Input: inputs})
	if err != nil {
		return nil, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	if c.Key != "" {
		hr.Header.Set("Authorization", "Bearer "+c.Key)
	}
	resp, err := c.HTTP.Do(hr)
	if err != nil {
		return nil, fmt.Errorf("embedding request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	var out embedResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("embedding response (%s): %s", resp.Status, truncate(string(raw), 200))
	}
	if out.Error != nil {
		return nil, fmt.Errorf("embedding error (%s): %s", resp.Status, out.Error.Message)
	}
	if resp.StatusCode >= 300 || len(out.Data) != len(inputs) {
		return nil, fmt.Errorf("embedding response %s: got %d vectors for %d inputs", resp.Status, len(out.Data), len(inputs))
	}
	vecs := make([][]float32, len(inputs))
	for pos, d := range out.Data {
		i := d.Index
		if i < 0 || i >= len(inputs) || vecs[i] != nil {
			i = pos // servers that omit index return in input order
		}
		if len(d.Embedding) == 0 || vecs[i] != nil {
			return nil, errors.New("embedding response has empty or duplicate vectors")
		}
		vecs[i] = d.Embedding
	}
	return vecs, nil
}

// Models lists model IDs from GET /models (works for OpenAI and Ollama).
func (c *Client) Models(ctx context.Context) ([]string, error) {
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	if c.Key != "" {
		hr.Header.Set("Authorization", "Bearer "+c.Key)
	}
	resp, err := c.HTTP.Do(hr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET /models: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	var ids []string
	for _, d := range out.Data {
		ids = append(ids, d.ID)
	}
	return ids, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
