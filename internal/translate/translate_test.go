package translate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testKey = "sk-test-secret-0123456789"

func config(base string) Config {
	return Config{BaseURL: base, APIKey: testKey, Model: "gpt-test", Language: "Vietnamese"}
}

func TestTranslateRequest(t *testing.T) {
	var got chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if h := r.Header.Get("Authorization"); h != "Bearer "+testKey {
			t.Errorf("Authorization = %q", h)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		var raw map[string]any
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatal(err)
		}
		if _, ok := raw["temperature"]; ok {
			t.Error("temperature sent")
		}
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &got)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"  Xin chào  \n"}}]}`))
	}))
	defer srv.Close()

	// A trailing slash on the base URL is trimmed.
	for _, base := range []string{srv.URL + "/v1", srv.URL + "/v1/"} {
		out, err := NewClient().Translate(context.Background(), config(base), "Hello")
		if err != nil || out != "Xin chào" {
			t.Fatalf("Translate(%s) = %q, %v", base, out, err)
		}
		if got.Model != "gpt-test" || len(got.Messages) != 2 || got.Messages[0].Role != "system" ||
			!strings.Contains(got.Messages[0].Content, "Vietnamese") || got.Messages[1] != (chatMessage{"user", "Hello"}) {
			t.Errorf("request = %+v", got)
		}
	}
}

func TestTranslateErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"provider message", 401, `{"error":{"message":"Incorrect API key provided: ` + testKey + `","type":"invalid_request_error"}}`, "401 Unauthorized: Incorrect API key provided: [redacted]"},
		{"string error", 400, `{"error":"model not found"}`, "400 Bad Request: model not found"},
		{"plain body", 502, "<html>bad gateway</html>", "502 Bad Gateway: <html>bad gateway</html>"},
		{"empty body", 500, "", "500 Internal Server Error"},
		{"no choices", 200, `{"choices":[]}`, "no choices"},
		{"empty content", 200, `{"choices":[{"message":{"content":"  "}}]}`, "empty"},
		{"not json", 200, `hello`, "not a chat completion: hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			_, err := NewClient().Translate(context.Background(), config(srv.URL), "Hello")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), testKey) {
				t.Errorf("error leaks the key: %v", err)
			}
		})
	}
}

func TestTranslateLongErrorIsCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(strings.Repeat("x", 5000) + testKey))
	}))
	defer srv.Close()
	_, err := NewClient().Translate(context.Background(), config(srv.URL), "Hello")
	if err == nil || len([]rune(err.Error())) > maxErrorLen+len("translate: ") {
		t.Fatalf("err = %v", err)
	}
}

func TestTranslateCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := NewClient().Translate(ctx, config(srv.URL), "Hello")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestTranslateRefusesBadInput(t *testing.T) {
	c := NewClient()
	ctx := context.Background()
	if _, err := c.Translate(ctx, Config{BaseURL: "https://x"}, "Hello"); err == nil {
		t.Error("unconfigured accepted")
	}
	if _, err := c.Translate(ctx, config("https://example.invalid"), "  \n"); err == nil {
		t.Error("blank text accepted")
	}
	if _, err := c.Translate(ctx, config("https://example.invalid"), strings.Repeat("a", MaxText+1)); err == nil {
		t.Error("long text accepted")
	}
}

func TestValidate(t *testing.T) {
	good := []Config{
		{},
		{BaseURL: "https://api.openai.com/v1", APIKey: "sk-abc", Model: "gpt-4o-mini", Language: "Vietnamese"},
		{BaseURL: "http://localhost:11434/v1"},
	}
	for _, c := range good {
		if err := c.Validate(); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	bad := []Config{
		{BaseURL: "api.openai.com/v1"},
		{BaseURL: "ftp://example.com"},
		{BaseURL: "https://"},
		{BaseURL: "https://user:pass@example.com"},
		{APIKey: `sk-"x`},
		{APIKey: `sk-\x`},
		{Model: "gpt\n4"},
		{Language: "Viet\tnamese"},
		{Model: strings.Repeat("m", maxModel+1)},
		{Language: strings.Repeat("l", maxLanguage+1)},
	}
	for _, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
}

func TestConfigured(t *testing.T) {
	c := config("https://x")
	if !c.Configured() {
		t.Error("full config not configured")
	}
	for _, f := range []func(*Config){
		func(c *Config) { c.BaseURL = "" },
		func(c *Config) { c.APIKey = "" },
		func(c *Config) { c.Model = "" },
	} {
		c := config("https://x")
		f(&c)
		if c.Configured() {
			t.Errorf("%+v configured", c)
		}
	}
}
