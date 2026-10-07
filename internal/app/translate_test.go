package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/configfile"
	"github.com/ginkcode/agent-sessions/internal/manage"
	"github.com/ginkcode/agent-sessions/internal/translate"
)

func TestTranslateSettingsRoundTrip(t *testing.T) {
	a, _, _ := setupHandoffTest(t)
	a.roots.Config = t.TempDir()
	path := filepath.Join(a.roots.Config, "config.toml")

	s, err := a.TranslateSettings()
	if err != nil || s != (TranslateSettings{Language: "Vietnamese"}) {
		t.Fatalf("defaults = %+v, %v", s, err)
	}

	// Other tables survive.
	if err := os.WriteFile(path, []byte("# mine\n[terminal]\napp = \"kitty\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manage.NewConfigStore(path).Update(func(c *manage.Config) { c.Enabled = true }); err != nil {
		t.Fatal(err)
	}

	const key = "sk-secret-123"
	s, err = a.SetTranslateSettings(TranslateSettingsRequest{
		BaseURL: " https://api.example.com/v1 ", Model: "gpt-test", Language: "", APIKey: key,
	})
	want := TranslateSettings{BaseURL: "https://api.example.com/v1", Model: "gpt-test", Language: "Vietnamese", APIKeySet: true, Configured: true}
	if err != nil || s != want {
		t.Fatalf("SetTranslateSettings = %+v, %v", s, err)
	}
	if s, _ := a.TranslateSettings(); s != want {
		t.Errorf("read back %+v", s)
	}
	data, _ := os.ReadFile(path)
	if v, _ := configfile.Value(data, "translate", "api_key"); v != key {
		t.Errorf("key not saved:\n%s", data)
	}
	if v, _ := configfile.Value(data, "terminal", "app"); v != "kitty" || !strings.Contains(string(data), "# mine") {
		t.Errorf("terminal lost:\n%s", data)
	}
	if cfg, err := manage.NewConfigStore(path).Load(); err != nil || !cfg.Enabled {
		t.Errorf("manage lost: %+v, %v", cfg, err)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, %v", fi.Mode(), err)
		}
	}

	// An empty key keeps the saved one.
	s, err = a.SetTranslateSettings(TranslateSettingsRequest{BaseURL: "https://api.example.com/v1", Model: "other", Language: "French"})
	if err != nil || !s.APIKeySet || s.Model != "other" || s.Language != "French" {
		t.Fatalf("keep key: %+v, %v", s, err)
	}
	if cfg, _ := a.translateConfig(); cfg.APIKey != key {
		t.Errorf("key = %q", cfg.APIKey)
	}

	// Clear removes it.
	s, err = a.SetTranslateSettings(TranslateSettingsRequest{BaseURL: "https://api.example.com/v1", Model: "other", ClearAPIKey: true})
	if err != nil || s.APIKeySet || s.Configured {
		t.Fatalf("clear key: %+v, %v", s, err)
	}
	if cfg, _ := a.translateConfig(); cfg.APIKey != "" {
		t.Errorf("key = %q", cfg.APIKey)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), key) || strings.Count(string(data), "[translate]") != 1 {
		t.Errorf("config:\n%s", data)
	}

	// Invalid input is refused and changes nothing.
	for _, req := range []TranslateSettingsRequest{
		{BaseURL: "file:///etc/passwd"},
		{BaseURL: "https://x", APIKey: `a"b`},
		{BaseURL: "https://x", Model: "a\\b"},
	} {
		if _, err := a.SetTranslateSettings(req); err == nil {
			t.Errorf("%+v accepted", req)
		}
	}
	if s, _ := a.TranslateSettings(); s.Model != "other" {
		t.Errorf("changed by a refused request: %+v", s)
	}
}

func TestTranslateUsesSettings(t *testing.T) {
	a, _, _ := setupHandoffTest(t)
	a.roots.Config = t.TempDir()
	if _, err := a.Translate("Hello"); err == nil || !strings.Contains(err.Error(), "not set up") {
		t.Fatalf("unconfigured err = %v", err)
	}

	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Bonjour"}}]}`))
	}))
	defer srv.Close()
	a.translator = &translate.Client{HTTP: srv.Client()}
	if _, err := a.SetTranslateSettings(TranslateSettingsRequest{BaseURL: srv.URL + "/v1/", Model: "m", Language: "French", APIKey: "k1"}); err != nil {
		t.Fatal(err)
	}
	out, err := a.Translate("Hello")
	if err != nil || out != "Bonjour" || auth != "Bearer k1" || path != "/v1/chat/completions" {
		t.Errorf("Translate = %q, %v (auth %q, path %q)", out, err, auth, path)
	}
}

func TestTranslateWhileRemote(t *testing.T) {
	stub := &stubRemoteBackend{}
	a := setupRemoteApp(stub)
	a.roots.Config = t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Xin chào"}}]}`))
	}))
	defer srv.Close()
	a.translator = &translate.Client{HTTP: srv.Client()}
	if _, err := a.SetTranslateSettings(TranslateSettingsRequest{BaseURL: srv.URL, Model: "m", APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	if out, err := a.Translate("Hello"); err != nil || out != "Xin chào" {
		t.Errorf("Translate = %q, %v", out, err)
	}
}
