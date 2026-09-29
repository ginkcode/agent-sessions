// Package redact detects and scrubs credentials, secrets, and local home paths
// from strings, transcripts, and JSON structures.
package redact

import (
	"bytes"
	"encoding/json"
	"regexp"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// Counts tracks how many times each redaction rule was triggered.
type Counts struct {
	PEM        int `json:"pem"`
	Token      int `json:"token"`
	JWT        int `json:"jwt"`
	Assignment int `json:"assignment"`
	Home       int `json:"home"`
}

// Total returns the sum of all redactions performed.
func (c Counts) Total() int {
	return c.PEM + c.Token + c.JWT + c.Assignment + c.Home
}

// Add combines two counts.
func (c Counts) Add(o Counts) Counts {
	return Counts{
		PEM:        c.PEM + o.PEM,
		Token:      c.Token + o.Token,
		JWT:        c.JWT + o.JWT,
		Assignment: c.Assignment + o.Assignment,
		Home:       c.Home + o.Home,
	}
}

// Map returns rule counts as a map matching manifest.json schema.
func (c Counts) Map() map[string]int {
	return map[string]int{
		"pem":        c.PEM,
		"token":      c.Token,
		"jwt":        c.JWT,
		"assignment": c.Assignment,
		"home":       c.Home,
	}
}

var (
	// PEM private key blocks.
	rePEM = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY[A-Z0-9 ]*-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY[A-Z0-9 ]*-----`)

	// Known secret tokens.
	reTokenAnthropic = regexp.MustCompile(`\bsk-ant-[a-zA-Z0-9_\-]{20,}\b`)
	reTokenOpenAI    = regexp.MustCompile(`\bsk-[a-zA-Z0-9_\-]{20,}\b`)
	reTokenGitHub    = regexp.MustCompile(`\b(ghp_[a-zA-Z0-9]{20,}|github_pat_[a-zA-Z0-9_]{20,})\b`)
	reTokenSlack     = regexp.MustCompile(`\bxox[bpar]-[a-zA-Z0-9_\-]{10,}\b`)
	reTokenAWS       = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)

	// JWTs and Bearer tokens.
	reJWT    = regexp.MustCompile(`\beyJ[a-zA-Z0-9_\-]{10,}\.eyJ[a-zA-Z0-9_\-]{10,}\.[a-zA-Z0-9_\-]+\b`)
	reBearer = regexp.MustCompile(`(?i)\bBearer\s+([a-zA-Z0-9_\-\.]{20,})\b`)

	// KEY=... / password: ... assignments and .env lines.
	reAssignment = regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:API_?KEY|SECRET|PASSWORD|PASSWD|TOKEN|AUTH|PRIVATE_?KEY)[A-Z0-9_]*\s*[:=]\s*["']?)([^"'\s\r\n]{4,})(["']?)`)

	// Home directories on Linux (/home/<user>) and macOS (/Users/<user>).
	reHomeLinux  = regexp.MustCompile(`/home/[a-zA-Z0-9_.\-]+`)
	reHomeDarwin = regexp.MustCompile(`/Users/[a-zA-Z0-9_.\-]+`)
)

const (
	redactedPEM        = "[REDACTED PRIVATE KEY]"
	redactedToken      = "[REDACTED TOKEN]"
	redactedJWT        = "[REDACTED JWT]"
	redactedBearer     = "[REDACTED BEARER TOKEN]"
	redactedAssignment = "[REDACTED SECRET]"
)

// Text scrubs secrets and home directories from a plain-text string.
func Text(s string) (string, Counts) {
	var counts Counts
	if s == "" {
		return "", counts
	}

	// 1. PEM private keys.
	if rePEM.MatchString(s) {
		matches := rePEM.FindAllString(s, -1)
		counts.PEM += len(matches)
		s = rePEM.ReplaceAllString(s, redactedPEM)
	}

	// 2. Specific API tokens.
	replaceToken := func(re *regexp.Regexp) {
		if re.MatchString(s) {
			matches := re.FindAllString(s, -1)
			counts.Token += len(matches)
			s = re.ReplaceAllString(s, redactedToken)
		}
	}
	replaceToken(reTokenAnthropic)
	replaceToken(reTokenOpenAI)
	replaceToken(reTokenGitHub)
	replaceToken(reTokenSlack)
	replaceToken(reTokenAWS)

	// 3. JWTs & Bearer tokens.
	if reJWT.MatchString(s) {
		matches := reJWT.FindAllString(s, -1)
		counts.JWT += len(matches)
		s = reJWT.ReplaceAllString(s, redactedJWT)
	}
	if reBearer.MatchString(s) {
		matches := reBearer.FindAllStringSubmatch(s, -1)
		counts.JWT += len(matches)
		s = reBearer.ReplaceAllString(s, "Bearer "+redactedBearer)
	}

	// 4. Assignments (KEY=value).
	if reAssignment.MatchString(s) {
		matches := reAssignment.FindAllStringSubmatch(s, -1)
		counts.Assignment += len(matches)
		s = reAssignment.ReplaceAllString(s, "${1}"+redactedAssignment+"${3}")
	}

	// 5. Home directories rewrite to ~.
	replaceHome := func(re *regexp.Regexp) {
		if re.MatchString(s) {
			matches := re.FindAllString(s, -1)
			counts.Home += len(matches)
			s = re.ReplaceAllString(s, "~")
		}
	}
	replaceHome(reHomeLinux)
	replaceHome(reHomeDarwin)

	return s, counts
}

// Transcript redacts a model.Transcript in place and returns total counts.
func Transcript(ts *model.Transcript) Counts {
	if ts == nil {
		return Counts{}
	}
	var total Counts

	redactStr := func(p *string) {
		if *p == "" {
			return
		}
		var c Counts
		*p, c = Text(*p)
		total = total.Add(c)
	}

	redactStr(&ts.Meta.CWD)
	redactStr(&ts.Meta.RepoRoot)
	redactStr(&ts.Meta.Title)
	redactStr(&ts.Meta.FirstPrompt)
	redactStr(&ts.Meta.SourcePath)

	for mIdx := range ts.Messages {
		msg := &ts.Messages[mIdx]
		for pIdx := range msg.Parts {
			part := &msg.Parts[pIdx]
			redactStr(&part.Text)
			for fIdx := range part.Files {
				redactStr(&part.Files[fIdx])
			}
			if part.File != nil {
				redactStr(&part.File.Name)
				redactStr(&part.File.Ref)
			}
			if part.Tool != nil {
				redactStr(&part.Tool.Output)
				if len(part.Tool.Input) > 0 {
					var rawCounts Counts
					part.Tool.Input, rawCounts = JSONBytes(part.Tool.Input)
					total = total.Add(rawCounts)
				}
			}
		}
	}

	return total
}

// JSONBytes redacts strings inside JSON bytes while preserving JSON structure.
func JSONBytes(data []byte) ([]byte, Counts) {
	if len(data) == 0 {
		return data, Counts{}
	}
	var val any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&val); err != nil {
		// Fallback to text redaction
		s, counts := Text(string(data))
		return []byte(s), counts
	}

	var total Counts
	var walk func(v any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case string:
			res, c := Text(t)
			total = total.Add(c)
			return res
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
			return t
		case map[string]any:
			for k, child := range t {
				t[k] = walk(child)
			}
			return t
		default:
			return v
		}
	}

	val = walk(val)
	out, err := json.Marshal(val)
	if err != nil {
		s, counts := Text(string(data))
		return []byte(s), counts
	}
	return out, total
}
