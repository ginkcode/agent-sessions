package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/configfile"
	"github.com/ginkcode/agent-sessions/internal/translate"
)

// TranslateSettings is where messages are translated. Like the terminal it
// is local to this computer, and the API key is never sent back: APIKeySet
// only says whether one is saved.
type TranslateSettings struct {
	BaseURL    string `json:"baseURL"`
	Model      string `json:"model"`
	Language   string `json:"language"`
	APIKeySet  bool   `json:"apiKeySet"`
	Configured bool   `json:"configured"`
}

// TranslateSettingsRequest saves the translation settings. An empty APIKey
// keeps the saved key; ClearAPIKey removes it.
type TranslateSettingsRequest struct {
	BaseURL     string `json:"baseURL"`
	Model       string `json:"model"`
	Language    string `json:"language"`
	APIKey      string `json:"apiKey"`
	ClearAPIKey bool   `json:"clearApiKey"`
}

const translateTable = "translate"

var defaultTranslator = translate.NewClient()

// TranslateSettings returns the translation settings without the API key.
func (a *App) TranslateSettings() (TranslateSettings, error) {
	cfg, err := a.translateConfig()
	if err != nil {
		return TranslateSettings{}, err
	}
	return translateSettings(cfg), nil
}

// SetTranslateSettings saves the translation settings.
func (a *App) SetTranslateSettings(req TranslateSettingsRequest) (TranslateSettings, error) {
	next := translate.Config{
		BaseURL:  strings.TrimSpace(req.BaseURL),
		APIKey:   strings.TrimSpace(req.APIKey),
		Model:    strings.TrimSpace(req.Model),
		Language: strings.TrimSpace(req.Language),
	}
	if next.Language == "" {
		next.Language = translate.DefaultLanguage
	}
	if err := next.Validate(); err != nil {
		return TranslateSettings{}, err
	}
	path, err := a.localConfigPath()
	if err != nil {
		return TranslateSettings{}, err
	}
	var saved translate.Config
	err = configfile.Update(path, func(data []byte) []byte {
		key, _ := configfile.Value(data, translateTable, "api_key")
		switch {
		case req.ClearAPIKey:
			key = ""
		case next.APIKey != "":
			key = next.APIKey
		}
		data = configfile.SetValue(data, translateTable, "base_url", strconv.Quote(next.BaseURL))
		data = configfile.SetValue(data, translateTable, "api_key", strconv.Quote(key))
		data = configfile.SetValue(data, translateTable, "model", strconv.Quote(next.Model))
		data = configfile.SetValue(data, translateTable, "language", strconv.Quote(next.Language))
		saved = next
		saved.APIKey = key
		return data
	})
	if err != nil {
		return TranslateSettings{}, fmt.Errorf("save translation settings: %w", err)
	}
	return translateSettings(saved), nil
}

// Translate translates text into the configured language. It runs on this
// computer whichever host is selected, so the key never leaves it except to
// the provider.
func (a *App) Translate(text string) (string, error) {
	cfg, err := a.translateConfig()
	if err != nil {
		return "", err
	}
	if !cfg.Configured() {
		return "", errors.New("translation is not set up: add a base URL, API key and model in Settings")
	}
	client := a.translator
	if client == nil {
		client = defaultTranslator
	}
	return client.Translate(a.appCtx(), cfg, text)
}

func translateSettings(cfg translate.Config) TranslateSettings {
	return TranslateSettings{
		BaseURL:    cfg.BaseURL,
		Model:      cfg.Model,
		Language:   cfg.Language,
		APIKeySet:  cfg.APIKey != "",
		Configured: cfg.Configured(),
	}
}

// translateConfig reads [translate] from the local config file.
func (a *App) translateConfig() (translate.Config, error) {
	cfg := translate.Config{Language: translate.DefaultLanguage}
	path, err := a.localConfigPath()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read settings: %w", err)
	}
	cfg.BaseURL, _ = configfile.Value(data, translateTable, "base_url")
	cfg.APIKey, _ = configfile.Value(data, translateTable, "api_key")
	cfg.Model, _ = configfile.Value(data, translateTable, "model")
	if lang, _ := configfile.Value(data, translateTable, "language"); lang != "" {
		cfg.Language = lang
	}
	return cfg, nil
}
