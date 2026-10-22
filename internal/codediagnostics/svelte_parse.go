package codediagnostics

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	svelteSummary    = regexp.MustCompile(`^COMPLETED ([0-9]+) FILES ([0-9]+) ERRORS ([0-9]+) WARNINGS ([0-9]+) FILES_WITH_PROBLEMS$`)
	machineTimestamp = regexp.MustCompile(`^[0-9]+ `)
)

type svelteRecord struct {
	Type            string `json:"type"`
	Filename        string `json:"filename"`
	Start, End      *Position
	Message         string          `json:"message"`
	Code            json.RawMessage `json:"code"`
	Source          string          `json:"source"`
	CodeDescription json.RawMessage `json:"codeDescription"`
}

// Only the verified machine-verbose protocol is accepted. Summary counts include
// rejected external paths, not just the findings safe to return to the client.
func parseSvelte(root AuthorizedRoot, dir string, data []byte, exit, budget int) (vetParsed, error) {
	out := vetParsed{diagnostics: []Diagnostic{}}
	bad := func() (vetParsed, error) {
		return out, failure("toolFailed", "Svelte check output or configuration could not be verified")
	}
	if len(data) > 10<<20 || !utf8.Valid(data) || !bytes.HasSuffix(data, []byte("\n")) {
		return bad()
	}
	started, completed := false, false
	errors, warnings := 0, 0
	files := map[string]bool{}
	sourceBudget := int64(16 << 20)
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		prefix := machineTimestamp.FindString(line)
		if prefix == "" || completed {
			return bad()
		}
		record := line[len(prefix):]
		if strings.HasPrefix(record, "START ") {
			var workspace string
			if started || json.Unmarshal([]byte(strings.TrimPrefix(record, "START ")), &workspace) != nil || workspace != filepath.Join(root.Project().Root, filepath.FromSlash(dir)) {
				return bad()
			}
			started = true
			continue
		}
		if !started {
			return bad()
		}
		if m := svelteSummary.FindStringSubmatch(record); m != nil {
			counts := make([]int, 4)
			for i := range counts {
				n, err := strconv.Atoi(m[i+1])
				if err != nil {
					return bad()
				}
				counts[i] = n
			}
			if counts[1] != errors || counts[2] != warnings || counts[3] != len(files) || counts[0] < counts[3] || (exit == 1) != (errors > 0) || (exit != 0 && exit != 1) {
				return bad()
			}
			completed = true
			continue
		}
		var r svelteRecord
		dec := json.NewDecoder(strings.NewReader(record))
		dec.DisallowUnknownFields()
		if dec.Decode(&r) != nil || !json.Valid([]byte(record)) || r.Filename == "" || r.Message == "" || (r.Type != "ERROR" && r.Type != "WARNING") {
			return bad()
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(record), &fields) != nil {
			return bad()
		}
		malformedPosition := false
		for _, key := range []string{"start", "end"} {
			if raw := fields[key]; len(raw) > 0 && string(raw) != "null" {
				var coords map[string]json.RawMessage
				if json.Unmarshal(raw, &coords) != nil || len(coords["line"]) == 0 || len(coords["character"]) == 0 || string(coords["line"]) == "null" || string(coords["character"]) == "null" {
					malformedPosition = true
				}
			}
		}
		if r.Type == "ERROR" {
			errors++
		} else {
			warnings++
		}
		files[r.Filename] = true
		code := ""
		if len(r.Code) > 0 {
			if json.Unmarshal(r.Code, &code) != nil {
				var n json.Number
				if json.Unmarshal(r.Code, &n) != nil {
					return bad()
				}
				v, err := strconv.ParseInt(string(n), 10, 64)
				if err != nil {
					return bad()
				}
				code = strconv.FormatInt(v, 10)
			}
			if !bounded(code, 256) {
				return bad()
			}
		}
		// Config errors may look exactly like normal findings and even have COMPLETED.
		// Unknown global/config/dependency errors fail closed, including missing imports.
		base := filepath.Base(r.Filename)
		if strings.Contains(base, "config.") || strings.HasPrefix(r.Message, "Error in ") || code == "6053" || code == "2307" || code == "2688" || code == "2792" {
			return bad()
		}
		if len(out.diagnostics) >= budget {
			return out, failure("limitExceeded", "Svelte finding limit exceeded")
		}
		path := r.Filename
		if !filepath.IsAbs(path) {
			path = filepath.Join(root.Project().Root, filepath.FromSlash(dir), path)
		}
		rel, err := filepath.Rel(root.Project().Root, path)
		rel = filepath.ToSlash(rel)
		if err != nil {
			out.partial = true
			continue
		}
		if _, err = root.ResolvePath(root.Project().HostID, rel); err != nil {
			out.partial = true
			continue
		}
		source := r.Source
		if source == "" {
			source = "svelte-check"
		}
		if !bounded(source, 256) {
			return bad()
		}
		message := r.Message
		if len(message) > 8192 {
			message = message[:8192]
			for !utf8.ValidString(message) {
				message = message[:len(message)-1]
			}
			out.partial = true
		}
		d := Diagnostic{Path: rel, Severity: strings.ToLower(r.Type), Message: message, Source: source, Code: code, Freshness: "uncertain", Basis: SavedBasis{Kind: "saved"}}
		if r.Start != nil || r.End != nil {
			d.LocationIssue = "invalidRange"
			text, err := rootedRead(root, rel, min(int64(5<<20), sourceBudget))
			sourceBudget -= int64(len(text))
			if err != nil {
				out.partial = true
			}
			if err == nil && !malformedPosition && r.Start != nil && r.End != nil && validUTF16Position(text, *r.Start) && validUTF16Position(text, *r.End) && (r.End.Line > r.Start.Line || r.End.Line == r.Start.Line && r.End.Character >= r.Start.Character) {
				d.Range = &Range{*r.Start, *r.End}
				d.LocationIssue = ""
			}
		}
		out.diagnostics = append(out.diagnostics, d)
	}
	if !completed {
		return bad()
	}
	return out, nil
}

func validUTF16Position(text []byte, p Position) bool {
	if p.Line < 0 || p.Character < 0 || !utf8.Valid(text) {
		return false
	}
	start := 0
	for i := 0; i < p.Line; i++ {
		n := bytes.IndexByte(text[start:], '\n')
		if n < 0 {
			return false
		}
		start += n + 1
	}
	end := len(text)
	if n := bytes.IndexByte(text[start:], '\n'); n >= 0 {
		end = start + n
	}
	if end > start && text[end-1] == '\r' {
		end--
	}
	units := 0
	for _, r := range string(text[start:end]) {
		if units == p.Character {
			return true
		}
		units++
		if r > 0xffff {
			units++
		}
	}
	return units == p.Character
}
