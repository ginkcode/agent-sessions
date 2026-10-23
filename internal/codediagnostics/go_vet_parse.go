package codediagnostics

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

type (
	Position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	}
	Range struct {
		Start Position `json:"start"`
		End   Position `json:"end"`
	}
	SavedBasis struct {
		Kind       string `json:"kind"`
		Revision   string `json:"revision,omitempty"`
		DocumentID string `json:"documentId,omitempty"`
		Version    *int64 `json:"version,omitempty"`
		// ObservedVersion binds an unversioned TypeScript push to the mirror at receipt,
		// not to a version confirmed by the server.
		ObservedVersion *int64 `json:"observedVersion,omitempty"`
	}
	Diagnostic struct {
		Path          string     `json:"path"`
		Severity      string     `json:"severity"`
		Message       string     `json:"message"`
		Source        string     `json:"source"`
		Freshness     string     `json:"freshness"`
		Basis         SavedBasis `json:"basis"`
		Range         *Range     `json:"range,omitempty"`
		Code          string     `json:"code,omitempty"`
		LocationIssue string     `json:"locationIssue,omitempty"`
		// Backend-only normalization evidence; never part of the wire contract.
		partial   bool
		truncated bool
	}
)

type (
	goVetRecord struct {
		Posn    string `json:"posn"`
		End     string `json:"end"`
		Message string `json:"message"`
	}
	vetParsed struct {
		diagnostics []Diagnostic
		partial     bool
	}
)

// Consecutive pretty-printed objects, NOT NDJSON. Unknown shapes fail closed.
func parseGoVet(root AuthorizedRoot, dir string, data []byte, budget int) (vetParsed, error) {
	out := vetParsed{diagnostics: []Diagnostic{}}
	bad := func() (vetParsed, error) { return out, failure("toolFailed", "Go vet output could not be verified") }
	if len(data) > 10<<20 || !utf8.Valid(data) {
		return bad()
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	objects := 0
	sourceBudget := int64(16 << 20)
	for {
		var packages map[string]map[string][]goVetRecord
		err := dec.Decode(&packages)
		if err == io.EOF {
			if objects == 0 {
				return bad()
			}
			break
		}
		if err != nil || packages == nil {
			return bad()
		}
		objects++
		// Sorted traversal gives a stable prefix regardless of Go map iteration.
		for _, pkg := range sortedKeys(packages) {
			analyzers := packages[pkg]
			if pkg == "" || analyzers == nil {
				return bad()
			}
			for _, code := range sortedKeys(analyzers) {
				if !bounded(code, 256) || analyzers[code] == nil {
					return bad()
				}
				for _, record := range analyzers[code] {
					if record.Message == "" || !utf8.ValidString(record.Message) {
						return bad()
					}
					if len(out.diagnostics) >= budget {
						return out, failure("limitExceeded", "Go vet finding limit exceeded")
					}
					path, line, col, hasCoords := vetLocation(record.Posn)
					if path == "" {
						out.partial = true
						continue
					}
					if !filepath.IsAbs(path) {
						path = filepath.Join(root.Project().Root, filepath.FromSlash(dir), path)
					}
					rel, err := filepath.Rel(root.Project().Root, path)
					if err != nil {
						out.partial = true
						continue
					}
					rel = filepath.ToSlash(rel)
					if _, err = root.ResolvePath(root.Project().HostID, rel); err != nil {
						out.partial = true
						continue
					}
					message := record.Message
					if len(message) > 8192 {
						message = message[:8192]
						for !utf8.ValidString(message) {
							message = message[:len(message)-1]
						}
						out.partial = true
					}
					d := Diagnostic{Path: rel, Severity: "warning", Message: message, Source: "go vet", Code: code, Freshness: "uncertain", Basis: SavedBasis{Kind: "saved"}}
					if hasCoords {
						d.LocationIssue = "invalidRange"
						// Ranges describe only this bounded saved read, not a proven tool snapshot.
						// Freshness stays uncertain: downstream must NEVER decorate drafts with it.
						limit := min(int64(5<<20), sourceBudget)
						text, err := rootedRead(root, rel, limit)
						sourceBudget -= int64(len(text))
						if err != nil {
							out.partial = true
						}
						if err == nil {
							start, ok := bytePosition(text, line, col)
							end := start
							if record.End != "" {
								ep, el, ec, coords := vetLocation(record.End)
								if !filepath.IsAbs(ep) {
									ep = filepath.Join(root.Project().Root, filepath.FromSlash(dir), ep)
								}
								if ep != path || !coords {
									ok = false
								} else {
									var valid bool
									end, valid = bytePosition(text, el, ec)
									ok = ok && valid
								}
							}
							if ok && (end.Line > start.Line || end.Line == start.Line && end.Character >= start.Character) {
								d.Range = &Range{start, end}
								d.LocationIssue = ""
							}
						}
					} else if record.End != "" {
						d.LocationIssue = "invalidRange"
					}
					out.diagnostics = append(out.diagnostics, d)
				}
			}
		}
	}
	return out, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Parse numeric suffixes from the right, preserving spaces and drive colons.
// A bare filename is a synthetic file-level location, not line 1.
func vetLocation(s string) (string, int, int, bool) {
	last := strings.LastIndexByte(s, ':')
	if last < 0 {
		return s, 0, 0, false
	}
	prev := strings.LastIndexByte(s[:last], ':')
	if prev < 0 {
		return s, 0, 0, false
	}
	line, e1 := strconv.Atoi(s[prev+1 : last])
	col, e2 := strconv.Atoi(s[last+1:])
	if e1 != nil || e2 != nil {
		return s, 0, 0, false
	}
	return s[:prev], line, col, true
}

func bytePosition(data []byte, line, column int) (Position, bool) {
	if line < 1 || column < 1 || !utf8.Valid(data) {
		return Position{}, false
	}
	// Avoid allocating a line slice for each line in a large source file.
	start := 0
	for n := 1; n < line; n++ {
		i := bytes.IndexByte(data[start:], '\n')
		if i < 0 {
			return Position{}, false
		}
		start += i + 1
	}
	end := len(data)
	if i := bytes.IndexByte(data[start:], '\n'); i >= 0 {
		end = start + i
	}
	if end > start && data[end-1] == '\r' {
		end--
	}
	if column-1 > end-start {
		return Position{}, false
	}
	prefix := data[start : start+column-1]
	if !utf8.Valid(prefix) {
		return Position{}, false
	}
	units := 0
	for _, r := range string(prefix) {
		units++
		if r > 0xffff {
			units++
		}
	}
	return Position{line - 1, units}, true
}
