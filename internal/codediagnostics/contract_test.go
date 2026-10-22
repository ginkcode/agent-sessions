package codediagnostics_test

// These test-only wire shapes and validators check synthetic CD-01 fixture
// conformance. They are not production API types, authorization/containment
// checks, snapshot verification evidence, or an implementation of manager races.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	safeMax = int64(9007199254740991)
	mib     = 1 << 20
)

type contextWire struct {
	ConnectionID string `json:"connectionId"`
	AttachmentID string `json:"attachmentId"`
	Generation   string `json:"generation"`
}
type positionWire struct {
	Line      int64 `json:"line"`
	Character int64 `json:"character"`
}
type rangeWire struct {
	Start positionWire `json:"start"`
	End   positionWire `json:"end"`
}
type basisWire struct {
	Kind       string `json:"kind"`
	Revision   string `json:"revision,omitempty"`
	DocumentID string `json:"documentId,omitempty"`
	Version    *int64 `json:"version,omitempty"`
}
type diagnosticWire struct {
	Path          string     `json:"path"`
	Range         *rangeWire `json:"range,omitempty"`
	Severity      string     `json:"severity"`
	Message       string     `json:"message"`
	Source        string     `json:"source"`
	Code          string     `json:"code,omitempty"`
	Freshness     string     `json:"freshness"`
	Basis         basisWire  `json:"basis"`
	LocationIssue string     `json:"locationIssue,omitempty"`
}
type scopeWire struct {
	Kind       string `json:"kind"`
	Path       string `json:"path,omitempty"`
	DocumentID string `json:"documentId,omitempty"`
}
type setWire struct {
	ProviderID  string           `json:"providerId"`
	Origin      string           `json:"origin"`
	Scope       scopeWire        `json:"scope"`
	Diagnostics []diagnosticWire `json:"diagnostics"`
	RunID       string           `json:"runId,omitempty"`
}
type coverageWire struct {
	Kind            string   `json:"kind"`
	Roots           []string `json:"roots"`
	Partial         bool     `json:"partial"`
	ExternalChanges string   `json:"externalChanges"`
}
type providerWire struct {
	ProviderID   string       `json:"providerId"`
	Availability string       `json:"availability"`
	Phase        string       `json:"phase"`
	Coverage     coverageWire `json:"coverage"`
	Freshness    string       `json:"freshness"`
	Truncated    bool         `json:"truncated"`
	RunID        string       `json:"runId,omitempty"`
	Notice       string       `json:"notice,omitempty"`
}
type approvalWire struct {
	State       string `json:"state"`
	Mode        string `json:"mode,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}
type snapshotWire struct {
	ContractVersion int64          `json:"contractVersion"`
	Context         contextWire    `json:"context"`
	Sequence        int64          `json:"sequence"`
	Approval        approvalWire   `json:"approval"`
	Providers       []providerWire `json:"providers"`
	ResultSets      []setWire      `json:"resultSets"`
}
type eventWire struct {
	ContractVersion int64        `json:"contractVersion"`
	Context         contextWire  `json:"context"`
	Sequence        int64        `json:"sequence"`
	UpdateID        string       `json:"updateId"`
	PartIndex       int64        `json:"partIndex"`
	PartCount       int64        `json:"partCount"`
	Snapshot        snapshotWire `json:"snapshot"`
}

// Fixture decoding rejects explicit null anywhere: optional values are omitted,
// and required arrays must be present as arrays. Typed decoding then rejects
// nonintegral numeric syntax and overflow rather than coercing through float64.
func decodeWire(data []byte, dst any) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var noNull func(any) bool
	noNull = func(v any) bool {
		if v == nil {
			return false
		}
		switch v := v.(type) {
		case map[string]any:
			for _, child := range v {
				if !noNull(child) {
					return false
				}
			}
		case []any:
			for _, child := range v {
				if !noNull(child) {
					return false
				}
			}
		}
		return true
	}
	if !noNull(raw) {
		return fmt.Errorf("explicit null in fixture wire data")
	}
	return json.Unmarshal(data, dst)
}

func fixture(t *testing.T, name string, dst any) {
	t.Helper()
	data, err := os.ReadFile("testdata/contract/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.Valid(data) {
		t.Fatal("fixture is not UTF-8")
	}
	if err := decodeWire(data, dst); err != nil {
		t.Fatal(err)
	}
}
func bounded(s string, max int) bool { return s != "" && len(s) <= max && utf8.ValidString(s) }
func id(s string) bool               { return bounded(s, 256) }
func safe(n int64) bool              { return n >= 0 && n <= safeMax }
func oneOf(s string, options ...string) bool {
	for _, v := range options {
		if s == v {
			return true
		}
	}
	return false
}

func validContext(c contextWire) bool {
	return id(c.ConnectionID) && id(c.AttachmentID) && id(c.Generation)
}

// Lexical wire path checks only: host-native canonical/symlink containment is
// deliberately outside fixture validation.
func validPath(p string) bool {
	if !bounded(p, 4096) || strings.ContainsAny(p, "\\:") {
		return false
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return false
		}
	}
	for _, c := range strings.Split(p, "/") {
		if c == "" || c == "." || c == ".." {
			return false
		}
	}
	return true
}

func validRange(r rangeWire) bool {
	return safe(r.Start.Line) && safe(r.Start.Character) && safe(r.End.Line) && safe(r.End.Character) &&
		(r.Start.Line < r.End.Line || r.Start.Line == r.End.Line && r.Start.Character <= r.End.Character)
}

func validateDiagnostic(d diagnosticWire) error {
	if !validPath(d.Path) {
		return fmt.Errorf("invalid path")
	}
	if !oneOf(d.Severity, "error", "warning", "information", "hint") || !bounded(d.Message, 8192) || !id(d.Source) || d.Code != "" && !id(d.Code) {
		return fmt.Errorf("invalid diagnostic text/severity")
	}
	if !oneOf(d.Freshness, "current", "stale", "uncertain") {
		return fmt.Errorf("invalid freshness")
	}
	if d.Range != nil && !validRange(*d.Range) {
		return fmt.Errorf("invalid range")
	}
	if d.LocationIssue != "" && (d.LocationIssue != "invalidRange" || d.Range != nil) {
		return fmt.Errorf("invalid locationIssue")
	}
	verified := false
	switch d.Basis.Kind {
	case "saved":
		if d.Basis.DocumentID != "" || d.Basis.Version != nil || d.Basis.Revision != "" && !id(d.Basis.Revision) {
			return fmt.Errorf("invalid saved basis")
		}
		verified = d.Basis.Revision != ""
	case "document":
		if !id(d.Basis.DocumentID) || d.Basis.Revision != "" {
			return fmt.Errorf("invalid document basis")
		}
		if d.Basis.Version != nil && (!safe(*d.Basis.Version) || *d.Basis.Version < 1) {
			return fmt.Errorf("invalid version")
		}
		verified = d.Basis.Version != nil
	default:
		return fmt.Errorf("invalid basis kind")
	}
	// Presence records the fixture's assertion of verified evidence; this does not
	// prove what a checker actually read or that a saved basis matches a draft.
	if d.Freshness == "current" && !verified {
		return fmt.Errorf("current without verified basis")
	}
	return nil
}

func setKey(s setWire) string {
	return s.ProviderID + "\x00" + s.Origin + "\x00" + s.Scope.Kind + "\x00" + s.Scope.Path + "\x00" + s.Scope.DocumentID
}

func validateSnapshot(s snapshotWire) error {
	if s.ContractVersion != 1 || !validContext(s.Context) || !safe(s.Sequence) {
		return fmt.Errorf("invalid snapshot identity/sequence")
	}
	a := s.Approval
	if !oneOf(a.State, "required", "approved", "revoked") {
		return fmt.Errorf("invalid approval")
	}
	if a.State == "approved" {
		if !oneOf(a.Mode, "session", "persistent") || !id(a.Fingerprint) {
			return fmt.Errorf("invalid approved metadata")
		}
	} else if a.Mode != "" || a.Fingerprint != "" {
		return fmt.Errorf("unexpected approval metadata")
	}
	if s.Providers == nil || len(s.Providers) > 32 || s.ResultSets == nil {
		return fmt.Errorf("invalid required arrays")
	}
	providers := map[string]bool{}
	for _, p := range s.Providers {
		if !id(p.ProviderID) || providers[p.ProviderID] {
			return fmt.Errorf("invalid/duplicate provider")
		}
		providers[p.ProviderID] = true
		if !oneOf(p.Availability, "approvalRequired", "available", "missingTool", "unsupported", "disabled") || !oneOf(p.Phase, "idle", "starting", "checking", "failed", "canceled", "disconnected") || !oneOf(p.Freshness, "current", "stale", "uncertain") {
			return fmt.Errorf("invalid provider state")
		}
		c := p.Coverage
		if !oneOf(c.Kind, "savedProject", "openedDocuments") || c.Roots == nil || len(c.Roots) > 128 || !oneOf(c.ExternalChanges, "reconciled", "unwatched", "watching") || p.Truncated && !c.Partial {
			return fmt.Errorf("invalid coverage/truncation")
		}
		for _, root := range c.Roots {
			if root != "" && !validPath(root) {
				return fmt.Errorf("invalid coverage root")
			}
		}
		if p.RunID != "" && !id(p.RunID) || p.Notice != "" && !bounded(p.Notice, 2048) {
			return fmt.Errorf("invalid provider metadata")
		}
	}
	keys := map[string]bool{}
	count, findingBytes := 0, 0
	for _, set := range s.ResultSets {
		key := setKey(set)
		if !providers[set.ProviderID] || keys[key] || set.Diagnostics == nil {
			return fmt.Errorf("invalid/duplicate result set")
		}
		keys[key] = true
		switch set.Origin {
		case "checker":
			if set.Scope.Kind != "project" || set.Scope.Path != "" || set.Scope.DocumentID != "" || !id(set.RunID) {
				return fmt.Errorf("invalid checker scope/run")
			}
		case "lsp":
			if set.Scope.Kind != "document" || !validPath(set.Scope.Path) || !id(set.Scope.DocumentID) || set.RunID != "" {
				return fmt.Errorf("invalid LSP scope")
			}
		default:
			return fmt.Errorf("invalid origin")
		}
		for _, d := range set.Diagnostics {
			if err := validateDiagnostic(d); err != nil {
				return err
			}
			if set.Origin == "lsp" && (d.Path != set.Scope.Path || d.Basis.Kind != "document" || d.Basis.DocumentID != set.Scope.DocumentID) {
				return fmt.Errorf("finding outside LSP scope")
			}
			encoded, err := json.Marshal(d)
			if err != nil {
				return err
			}
			findingBytes += len(encoded)
			count++
		}
	}
	if count > 10000 || findingBytes > 7*mib {
		return fmt.Errorf("finding budget exceeded")
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		return err
	}
	// Removing diagnostics measures metadata (and preserves explicit empty sets).
	metadata := s
	metadata.ResultSets = append([]setWire{}, s.ResultSets...)
	for i := range metadata.ResultSets {
		metadata.ResultSets[i].Diagnostics = []diagnosticWire{}
	}
	meta, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if len(encoded) > 8*mib || len(meta) > mib {
		return fmt.Errorf("snapshot/metadata budget exceeded")
	}
	return nil
}

func validateEvent(e eventWire) error {
	if err := validateSnapshot(e.Snapshot); err != nil {
		return err
	}
	if e.ContractVersion != 1 || !validContext(e.Context) || !safe(e.Sequence) || !id(e.UpdateID) || e.PartCount < 1 || e.PartCount > 32 || e.PartIndex < 0 || e.PartIndex >= e.PartCount {
		return fmt.Errorf("invalid event header")
	}
	if e.Context != e.Snapshot.Context || e.Sequence != e.Snapshot.Sequence {
		return fmt.Errorf("event/snapshot mismatch")
	}
	// Include the JSON-RPC notification envelope, not just the event payload.
	frame, err := json.Marshal(struct {
		JSONRPC string    `json:"jsonrpc"`
		Method  string    `json:"method"`
		Params  eventWire `json:"params"`
	}{"2.0", "codeDiagnostics:updated", e})
	if err != nil {
		return err
	}
	if len(frame) > mib {
		return fmt.Errorf("event frame budget exceeded")
	}
	return nil
}

func roundTrip[T any](t *testing.T, value T) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded T
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(value, decoded) {
		t.Fatal("wire round trip changed value (including absent fields/empty arrays)")
	}
}

func TestContractSnapshotFixtures(t *testing.T) {
	var s snapshotWire
	fixture(t, "snapshot", &s)
	if err := validateSnapshot(s); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, s)
	severities := map[string]bool{}
	for _, set := range s.ResultSets {
		for _, d := range set.Diagnostics {
			severities[d.Severity] = true
		}
	}
	if len(severities) != 4 {
		t.Fatal("fixture must cover all four severities")
	}
	a, b := s.ResultSets[0].Diagnostics[0], s.ResultSets[1].Diagnostics[0]
	if a.Range != nil || a.Freshness != "stale" || a.Basis.Revision != "saved-old" {
		t.Fatal("expected file-level stale saved basis")
	}
	// Compare only documented display equivalence, retaining independent raw
	// attribution and basis. This is not a deduplication manager implementation.
	display := func(d diagnosticWire) string {
		data, err := json.Marshal([]any{d.Path, d.Range, d.Severity, d.Message, d.Code})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if display(a) != display(b) || a.Source == b.Source || s.ResultSets[0].ProviderID == s.ResultSets[1].ProviderID || a.Freshness == b.Freshness {
		t.Fatal("equivalent findings must retain distinct provider attribution and freshness")
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`\u003c\u0026\u003e`)) {
		t.Fatal("bounds must use Go JSON HTML escaping")
	}
	// Additive fields are intentionally ignored, not rejected.
	data = append([]byte(`{"futureOptional":true,`), data[1:]...)
	var additive snapshotWire
	if err := json.Unmarshal(data, &additive); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, additive) {
		t.Fatal("unknown additive field changed snapshot")
	}
}

func TestContractEmptyReplacement(t *testing.T) {
	var s snapshotWire
	fixture(t, "empty-replacement", &s)
	if err := validateSnapshot(s); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, s)
	if s.Sequence != 0 || len(s.ResultSets) != 1 || s.ResultSets[0].Origin != "checker" || s.ResultSets[0].Diagnostics == nil || len(s.ResultSets[0].Diagnostics) != 0 {
		t.Fatal("expected initial sequence boundary and explicit empty successful checker replacement")
	}
	// This records the wire distinction only; it does not execute a manager clear.
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"diagnostics":[]`)) {
		t.Fatal("empty replacement lost")
	}
}

func TestContractCoordinates(t *testing.T) {
	var doc struct {
		Path             string    `json:"path"`
		DocumentID       string    `json:"documentId"`
		Version          int64     `json:"version"`
		Text             string    `json:"text"`
		UTF16LineLengths []int     `json:"utf16LineLengths"`
		EmojiRange       rangeWire `json:"emojiRange"`
	}
	fixture(t, "document", &doc)
	roundTrip(t, doc)
	lines := strings.Split(doc.Text, "\r\n")
	var lengths []int
	for _, line := range lines {
		lengths = append(lengths, len(utf16.Encode([]rune(line))))
	}
	if !reflect.DeepEqual(lengths, doc.UTF16LineLengths) || !reflect.DeepEqual(lengths, []int{5, 2, 0}) {
		t.Fatalf("CRLF/tab/UTF-16 lengths: %v", lengths)
	}
	r := doc.EmojiRange
	units := utf16.Encode([]rune(lines[r.Start.Line]))
	if string(utf16.Decode(units[r.Start.Character:r.End.Character])) != "😀" {
		t.Fatal("exclusive UTF-16 emoji span incorrect")
	}
	var s snapshotWire
	fixture(t, "snapshot", &s)
	set := s.ResultSets[2]
	if doc.Path != set.Scope.Path || doc.DocumentID != set.Scope.DocumentID || doc.Version != *set.Diagnostics[0].Basis.Version || r != *set.Diagnostics[0].Range {
		t.Fatal("document and finding metadata disagree")
	}
	for _, d := range set.Diagnostics {
		if d.Range != nil {
			for _, p := range []positionWire{d.Range.Start, d.Range.End} {
				if p.Line >= int64(len(lengths)) || p.Character > int64(lengths[p.Line]) {
					t.Fatal("range outside synthetic basis text")
				}
			}
		}
	}
}

func TestContractMultipartFixtures(t *testing.T) {
	var events []eventWire
	fixture(t, "events", &events)
	roundTrip(t, events)
	if len(events) != 2 {
		t.Fatal("expected two parts")
	}
	first := events[0]
	keys := map[string][]diagnosticWire{}
	runs := map[string]string{}
	findings := map[string]bool{}
	for i, e := range events {
		if err := validateEvent(e); err != nil {
			t.Fatal(err)
		}
		if e.PartIndex != int64(i) || e.PartCount != int64(len(events)) || e.UpdateID != first.UpdateID || e.Context != first.Context || e.Sequence != first.Sequence || !reflect.DeepEqual(e.Snapshot.Approval, first.Snapshot.Approval) || !reflect.DeepEqual(e.Snapshot.Providers, first.Snapshot.Providers) {
			t.Fatal("inconsistent multipart metadata")
		}
		for _, set := range e.Snapshot.ResultSets {
			key := setKey(set)
			if run, exists := runs[key]; exists && run != set.RunID {
				t.Fatal("fragment run identity mismatch")
			}
			runs[key] = set.RunID
			for _, d := range set.Diagnostics {
				data, err := json.Marshal(d)
				if err != nil {
					t.Fatal(err)
				}
				findingKey := key + "\x00" + string(data)
				if findings[findingKey] {
					t.Fatal("finding repeated across fixture parts")
				}
				findings[findingKey] = true
			}
			keys[key] = append(keys[key], set.Diagnostics...)
		}
	}
	// Inspect complete fixture content only. No buffering, ordering, duplicate
	// delivery, commit, or race behavior is claimed to be implemented here.
	project := first.Snapshot.ResultSets[0]
	empty := first.Snapshot.ResultSets[1]
	if len(keys[setKey(project)]) != 2 || len(empty.Diagnostics) != 0 || empty.Diagnostics == nil {
		t.Fatal("fragments/explicit empty replacement lost")
	}
	encoded, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"diagnostics":[]`)) {
		t.Fatal("empty replacement must not be null/omitted")
	}
}

func TestContractRejectsInvalidMutations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*snapshotWire)
	}{
		{"unsafe sequence", func(s *snapshotWire) { s.Sequence = safeMax + 1 }},
		{"negative sequence", func(s *snapshotWire) { s.Sequence = -1 }},
		{"empty context ID", func(s *snapshotWire) { s.Context.Generation = "" }},
		{"overlong context ID", func(s *snapshotWire) { s.Context.ConnectionID = strings.Repeat("é", 129) }},
		{"path traversal", func(s *snapshotWire) { s.ResultSets[0].Diagnostics[0].Path = "src/../outside" }},
		{"absolute path", func(s *snapshotWire) { s.ResultSets[0].Diagnostics[0].Path = "/outside" }},
		{"backslash path", func(s *snapshotWire) { s.ResultSets[0].Diagnostics[0].Path = `src\outside` }},
		{"reversed range", func(s *snapshotWire) { s.ResultSets[2].Diagnostics[0].Range.Start.Character = 4 }},
		{"unsafe coordinate", func(s *snapshotWire) { s.ResultSets[2].Diagnostics[0].Range.End.Line = safeMax + 1 }},
		{"current saved without revision", func(s *snapshotWire) { s.ResultSets[1].Diagnostics[0].Basis.Revision = "" }},
		{"current document without version", func(s *snapshotWire) { s.ResultSets[2].Diagnostics[0].Basis.Version = nil }},
		{"zero document version", func(s *snapshotWire) { *s.ResultSets[2].Diagnostics[0].Basis.Version = 0 }},
		{"truncated without partial", func(s *snapshotWire) { s.Providers[1].Coverage.Partial = false }},
		{"null diagnostics", func(s *snapshotWire) { s.ResultSets[0].Diagnostics = nil }},
		{"LSP document mismatch", func(s *snapshotWire) { s.ResultSets[2].Scope.DocumentID = "other-document" }},
		{"overlong message bytes", func(s *snapshotWire) { s.ResultSets[0].Diagnostics[0].Message = strings.Repeat("é", 4097) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s snapshotWire
			fixture(t, "snapshot", &s)
			tt.mutate(&s)
			if err := validateSnapshot(s); err == nil {
				t.Fatal("invalid fixture accepted")
			}
		})
	}
	eventTests := []struct {
		name   string
		mutate func(*eventWire)
	}{
		{"context mismatch", func(e *eventWire) { e.Context.Generation = "other-generation" }},
		{"sequence mismatch", func(e *eventWire) { e.Sequence++ }},
		{"too many parts", func(e *eventWire) { e.PartCount = 33 }},
		{"part index outside count", func(e *eventWire) { e.PartIndex = e.PartCount }},
	}
	for _, tt := range eventTests {
		t.Run(tt.name, func(t *testing.T) {
			var events []eventWire
			fixture(t, "events", &events)
			tt.mutate(&events[0])
			if err := validateEvent(events[0]); err == nil {
				t.Fatal("invalid event accepted")
			}
		})
	}
	// Decode into integer fields: no float, exponent, string coercion or overflow.
	for _, number := range []string{`1.5`, `1e0`, `"1"`, `18446744073709551616`, `null`} {
		t.Run("integer syntax "+number, func(t *testing.T) {
			// A raw null must also be rejected; encoding/json otherwise maps it to zero.
			var raw map[string]json.RawMessage
			fixture(t, "snapshot", &raw)
			raw["sequence"] = json.RawMessage(number)
			data, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			var s snapshotWire
			err = decodeWire(data, &s)
			if err == nil {
				t.Fatal("noninteger/null decoded")
			}
		})
	}
}

func TestContractEncodedBudgets(t *testing.T) {
	// '<' is one source byte but six bytes under Go's default JSON escaping.
	// Every finding remains below its text limit and the count remains below 10k.
	var s snapshotWire
	fixture(t, "snapshot", &s)
	d := s.ResultSets[0].Diagnostics[0]
	d.Message = strings.Repeat("<", 8192)
	s.ResultSets = s.ResultSets[:1]
	s.ResultSets[0].Diagnostics = make([]diagnosticWire, 149)
	for i := range s.ResultSets[0].Diagnostics {
		d.Path = fmt.Sprintf("src/%03d.txt", i)
		s.ResultSets[0].Diagnostics[i] = d
	}
	if err := validateSnapshot(s); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("escaped finding budget not enforced: %v", err)
	}
	var events []eventWire
	fixture(t, "events", &events)
	e := events[0]
	e.Snapshot.ResultSets = s.ResultSets
	e.Snapshot.ResultSets[0].Diagnostics = e.Snapshot.ResultSets[0].Diagnostics[:22]
	if err := validateSnapshot(e.Snapshot); err != nil {
		t.Fatalf("event test must fit retained budget: %v", err)
	}
	if err := validateEvent(e); err == nil || !strings.Contains(err.Error(), "frame budget") {
		t.Fatalf("escaped event envelope budget not enforced: %v", err)
	}
}
