// Package translate translates transcript messages with an OpenAI-compatible
// chat completions endpoint. The desktop app calls it, so the API key stays
// out of the webview and outbound requests are not subject to its CSP.
package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultLanguage is the language messages are translated into until the
// user picks another.
const DefaultLanguage = "Vietnamese"

const (
	maxBaseURL  = 2048
	maxAPIKey   = 1024
	maxModel    = 200
	maxLanguage = 100
	// MaxText bounds the message text sent in one request.
	MaxText = 200_000
	// maxResponse bounds how much of a reply is read.
	maxResponse = 8 << 20
	// maxErrorLen bounds an error message built from a provider reply.
	maxErrorLen = 300
	timeout     = 120 * time.Second
)

// Config is where and how messages are translated.
type Config struct {
	BaseURL  string
	APIKey   string
	Model    string
	Language string
}

// Configured reports whether translation can run: it needs a base URL, an
// API key and a model.
func (c Config) Configured() bool {
	return c.BaseURL != "" && c.APIKey != "" && c.Model != ""
}

// Validate checks each field set. Values are stored as quoted strings that
// are read back without unescaping, so quotes, backslashes and characters
// strconv.Quote would escape are refused rather than saved in a form that
// would not read back.
func (c Config) Validate() error {
	fields := []struct {
		name, value string
		max         int
	}{
		{"base URL", c.BaseURL, maxBaseURL},
		{"API key", c.APIKey, maxAPIKey},
		{"model", c.Model, maxModel},
		{"language", c.Language, maxLanguage},
	}
	for _, f := range fields {
		if len(f.value) > f.max {
			return fmt.Errorf("%s is longer than %d characters", f.name, f.max)
		}
		if strings.ContainsAny(f.value, `"\`) || strings.ContainsFunc(f.value, notPrintable) {
			return fmt.Errorf("%s must not contain quotes, backslashes or control characters", f.name)
		}
	}
	if c.BaseURL != "" {
		u, err := url.Parse(c.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("base URL must be an http or https URL, such as https://api.openai.com/v1")
		}
		if u.User != nil {
			return errors.New("base URL must not contain credentials; enter the API key separately")
		}
	}
	return nil
}

func notPrintable(r rune) bool { return !strconv.IsPrint(r) }

// Client sends translation requests.
type Client struct {
	HTTP *http.Client
}

// NewClient returns a Client that honours the proxy environment variables
// and gives up after two minutes.
func NewClient() *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	return &Client{HTTP: &http.Client{Timeout: timeout, Transport: transport}}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// SystemPrompt is the instruction sent with every message.
func SystemPrompt(language string) string {
	if language == "" {
		language = DefaultLanguage
	}
	return "You are a translator. Translate the user's message into " + language + ". " +
		"Keep the Markdown formatting. Leave code blocks, inline code, URLs and file paths unchanged. " +
		"Reply with the translation only, without notes or explanations."
}

// Translate returns text translated into cfg.Language.
func (c *Client) Translate(ctx context.Context, cfg Config, text string) (string, error) {
	if !cfg.Configured() {
		return "", errors.New("translation is not set up")
	}
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", errors.New("nothing to translate")
	}
	if len(text) > MaxText {
		return "", fmt.Errorf("message is too long to translate (over %d characters)", MaxText)
	}
	body, err := json.Marshal(chatRequest{
		Model: cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: SystemPrompt(cfg.Language)},
			{Role: "user", Content: text},
		},
	})
	if err != nil {
		return "", err
	}
	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("translate: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = NewClient().HTTP
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		// url.Error carries the URL, never the headers.
		return "", fmt.Errorf("translate: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return "", fmt.Errorf("translate: read reply: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("translate: %s", statusError(resp.Status, redact(data, cfg.APIKey)))
	}
	var out chatResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("translate: the reply is not a chat completion: %s", snippet(redact(data, cfg.APIKey)))
	}
	if len(out.Choices) == 0 {
		return "", errors.New("translate: the reply has no choices")
	}
	result := strings.TrimSpace(out.Choices[0].Message.Content)
	if result == "" {
		return "", errors.New("translate: the reply is empty")
	}
	return result, nil
}

// statusError describes a failed reply: its status plus the provider's
// error message, or the start of the body.
func statusError(status string, data []byte) string {
	var e struct {
		Error json.RawMessage `json:"error"`
	}
	msg := ""
	if json.Unmarshal(data, &e) == nil && len(e.Error) > 0 {
		var obj struct {
			Message string `json:"message"`
		}
		var s string
		switch {
		case json.Unmarshal(e.Error, &obj) == nil && obj.Message != "":
			msg = obj.Message
		case json.Unmarshal(e.Error, &s) == nil:
			msg = s
		}
	}
	if msg == "" {
		msg = snippet(data)
	}
	if msg == "" {
		return status
	}
	return truncate(status + ": " + strings.Join(strings.Fields(msg), " "))
}

func snippet(data []byte) string {
	return truncate(strings.Join(strings.Fields(string(data)), " "))
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) <= maxErrorLen {
		return s
	}
	return string(r[:maxErrorLen-1]) + "…"
}

// redact removes the key should a provider echo it back. It runs before the
// reply is cut short, so a truncated key cannot slip through.
func redact(data []byte, key string) []byte {
	if key == "" {
		return data
	}
	return bytes.ReplaceAll(data, []byte(key), []byte("[redacted]"))
}
