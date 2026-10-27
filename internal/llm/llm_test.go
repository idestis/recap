package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/idestis/recap/internal/config"
)

func TestJSONAgainstCompatibleServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("auth %q", got)
		}
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "m" || req.ResponseFormat["type"] != "json_object" {
			t.Errorf("request %+v", req)
		}
		// Fenced JSON, as small local models often reply.
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"` + "```json\\n{\\\"title\\\":\\\"ok\\\"}\\n```" + `"}}]}`))
	}))
	defer srv.Close()
	t.Setenv("RECAP_API_KEY", "k")
	c, err := New(config.Model{Provider: "openai", BaseURL: srv.URL + "/v1", Name: "m"})
	if err != nil {
		t.Fatal(err)
	}
	var v struct{ Title string }
	if err := c.JSON(context.Background(), "s", "u", &v); err != nil || v.Title != "ok" {
		t.Fatalf("got %+v, %v", v, err)
	}
}

func TestEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path %s", r.URL.Path)
		}
		var req embedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "e" || len(req.Input) != 2 {
			t.Errorf("request %+v", req)
		}
		// Out of order on purpose: index decides placement.
		_, _ = w.Write([]byte(`{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}]}`))
	}))
	defer srv.Close()
	t.Setenv("RECAP_API_KEY", "k")
	c, err := New(config.Model{Provider: "openai", BaseURL: srv.URL + "/v1", Name: "e"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Embed(context.Background(), []string{"a", "b"})
	if err != nil || v[0][0] != 1 || v[1][1] != 1 {
		t.Fatalf("got %v %v", v, err)
	}
}

func TestEmbedErrors(t *testing.T) {
	for name, body := range map[string]string{
		"api error":   `{"error":{"message":"no such model"}}`,
		"wrong count": `{"data":[{"index":0,"embedding":[1]}]}`,
		"not json":    `oops`,
	} {
		body := body
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		c := &Client{BaseURL: srv.URL, Model: "e", HTTP: srv.Client()}
		if _, err := c.Embed(context.Background(), []string{"a", "b"}); err == nil {
			t.Errorf("%s: expected error", name)
		}
		srv.Close()
	}
}

func TestKeyAccount(t *testing.T) {
	if got := KeyAccount(config.Model{Provider: "openai"}); got != "openai@api.openai.com" {
		t.Fatal(got)
	}
}
