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

func TestKeyAccount(t *testing.T) {
	if got := KeyAccount(config.Model{Provider: "openai"}); got != "openai@api.openai.com" {
		t.Fatal(got)
	}
}
