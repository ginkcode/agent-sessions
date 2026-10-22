package codediagnostics_test

// CD-04 format assertions only: never launch checkers or authorize a project.
import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

type checkerFixture struct {
	ID           string            `json:"id"`
	Tool         string            `json:"tool"`
	Platform     string            `json:"platform"`
	Provenance   string            `json:"provenance"`
	Argv         []string          `json:"argv"`
	Cwd          string            `json:"cwd"`
	ExitCode     int               `json:"exitCode"`
	Stdout       string            `json:"stdout"`
	Stderr       string            `json:"stderr"`
	Expected     string            `json:"expected"`
	Versions     map[string]string `json:"versions"`
	Sanitization string            `json:"sanitization"`
	Files        map[string]string `json:"files"`
}

func checkerFixtures(t *testing.T) map[string]checkerFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/checkers/evidence.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []checkerFixture
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fixtures); err != nil {
		t.Fatal(err)
	}
	result := make(map[string]checkerFixture)
	for _, f := range fixtures {
		if _, exists := result[f.ID]; exists || f.ID == "" {
			t.Fatalf("duplicate/empty fixture: %q", f.ID)
		}
		if f.Provenance != "observed" && f.Provenance != "synthetic-mutation" {
			t.Fatalf("bad provenance: %s", f.ID)
		}
		if f.Platform != "darwin/arm64" || len(f.Argv) == 0 || !strings.HasPrefix(f.Argv[0], "/installed/") || !strings.HasPrefix(f.Cwd, "/fixture") || f.Sanitization == "" || len(f.Versions) != 5 {
			t.Fatalf("incomplete metadata: %s", f.ID)
		}
		encoded, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"/Users/", "/home/", "/private/", "kw94cjxs", "BEGIN PRIVATE KEY", "npx", "npm run"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("unsanitized/unsafe %s: %s", f.ID, forbidden)
			}
		}
		for path := range f.Files {
			if !validPath(path) {
				t.Fatalf("fixture source path %q", path)
			}
		}
		result[f.ID] = f
	}
	if len(result) != 22 {
		t.Fatalf("fixture count %d", len(result))
	}
	return result
}

func TestCheckerVersionAndExitEvidence(t *testing.T) {
	f := checkerFixtures(t)
	for id, want := range map[string]string{"go-version": "go version go1.26.8 darwin/arm64\n", "node-version": "v22.23.2\n", "svelte-version": "svelte-check, 4.7.6\n"} {
		if f[id].Stdout != want || f[id].ExitCode != 0 || f[id].Stderr != "" {
			t.Fatalf("version evidence changed: %s", id)
		}
	}
	for id, want := range map[string]int{"go-clean": 0, "go-findings": 0, "go-text-findings": 1, "go-build-failure": 1, "go-missing-dependency": 1, "svelte-clean": 0, "svelte-warning": 0, "svelte-findings": 1, "svelte-config-failure": 1, "svelte-svelte-config-failure": 1, "svelte-missing-entry": 1, "svelte-missing-config-dependency": 1} {
		if f[id].ExitCode != want {
			t.Fatalf("%s exit != %d", id, want)
		}
	}
	for _, id := range []string{"go-build-failure", "go-missing-dependency", "svelte-missing-entry"} {
		if f[id].Stdout != "" || f[id].Stderr == "" || f[id].Expected != "failure" {
			t.Fatalf("failure streams: %s", id)
		}
	}
}

type vetFinding struct {
	Posn    string `json:"posn"`
	End     string `json:"end"`
	Message string `json:"message"`
}

func vetFixtureRecords(text string) ([]vetFinding, int, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	var findings []vetFinding
	objects := 0
	for {
		var packages map[string]map[string][]vetFinding
		err := dec.Decode(&packages)
		if err == io.EOF {
			return findings, objects, nil
		}
		if err != nil {
			return nil, objects, err
		}
		objects++
		for _, analyzers := range packages {
			for analyzer, ds := range analyzers {
				if analyzer != "printf" {
					return nil, objects, fmt.Errorf("unexpected fixture analyzer %q", analyzer)
				}
				for _, d := range ds {
					if d.Posn == "" || d.End == "" || d.Message == "" {
						return nil, objects, fmt.Errorf("incomplete fixture record")
					}
					findings = append(findings, d)
				}
			}
		}
	}
}

func TestVetFixtureFormat(t *testing.T) {
	f := checkerFixtures(t)
	for _, tc := range []struct {
		id             string
		count, objects int
	}{{"go-clean", 0, 1}, {"go-findings", 2, 1}, {"go-multiple-packages", 2, 2}} {
		ds, n, err := vetFixtureRecords(f[tc.id].Stdout)
		if err != nil || n != tc.objects || len(ds) != tc.count || f[tc.id].Stderr != "" {
			t.Fatalf("%s: %d objects %d records %v", tc.id, n, len(ds), err)
		}
	}
	if _, _, err := vetFixtureRecords(f["go-truncated"].Stdout); err == nil {
		t.Fatal("truncated JSON accepted")
	}
	// Go's one-based byte columns differ from UTF-16 after é and a non-BMP rune.
	ds, _, err := vetFixtureRecords(f["go-findings"].Stdout)
	if err != nil {
		t.Fatal(err)
	}
	source := f["go-findings"].Files["first file.go"]
	line := strings.Split(source, "\r\n")[2]
	if !strings.Contains(ds[0].Posn, "first file.go:3:40") || !strings.Contains(ds[0].End, ":3:42") {
		t.Fatal("position evidence changed")
	}
	prefix := line[:39]
	if !utf8.ValidString(prefix) || len(utf16.Encode([]rune(prefix))) != 36 || line[39:41] != "%d" {
		t.Fatal("byte to UTF-16 evidence changed")
	}
}

type svelteFixtureRecord struct {
	Type     string          `json:"type"`
	Filename string          `json:"filename"`
	Start    *positionWire   `json:"start"`
	End      *positionWire   `json:"end"`
	Message  string          `json:"message"`
	Code     json.RawMessage `json:"code"`
	Source   string          `json:"source"`
}

var summaryPattern = regexp.MustCompile(`^1700000000000 COMPLETED ([0-9]+) FILES ([0-9]+) ERRORS ([0-9]+) WARNINGS ([0-9]+) FILES_WITH_PROBLEMS$`)

func svelteFixtureRecords(text string) ([]svelteFixtureRecord, error) {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], `1700000000000 START "/fixture/`) {
		return nil, fmt.Errorf("missing start")
	}
	summary := summaryPattern.FindStringSubmatch(lines[len(lines)-1])
	if summary == nil {
		return nil, fmt.Errorf("missing completed summary")
	}
	var ds []svelteFixtureRecord
	errors, warnings := 0, 0
	for _, line := range lines[1 : len(lines)-1] {
		body, ok := strings.CutPrefix(line, "1700000000000 ")
		if !ok {
			return nil, fmt.Errorf("missing timestamp")
		}
		var d svelteFixtureRecord
		if err := json.Unmarshal([]byte(body), &d); err != nil {
			return nil, err
		}
		if d.Filename == "" || d.Message == "" {
			return nil, fmt.Errorf("missing record fields")
		}
		if !validPath(d.Filename) {
			return nil, fmt.Errorf("invalid filename")
		}
		switch d.Type {
		case "ERROR":
			errors++
		case "WARNING":
			warnings++
		default:
			return nil, fmt.Errorf("unexpected severity")
		}
		if (d.Start == nil) != (d.End == nil) {
			return nil, fmt.Errorf("half range")
		}
		ds = append(ds, d)
	}
	wantErrors, _ := strconv.Atoi(summary[2])
	wantWarnings, _ := strconv.Atoi(summary[3])
	if errors != wantErrors || warnings != wantWarnings {
		return nil, fmt.Errorf("summary mismatch")
	}
	return ds, nil
}

func TestSvelteFixtureFormat(t *testing.T) {
	f := checkerFixtures(t)
	for _, tc := range []struct {
		id    string
		count int
	}{{"svelte-clean", 0}, {"svelte-warning", 1}, {"svelte-findings", 4}, {"svelte-config-failure", 1}, {"svelte-svelte-config-failure", 1}, {"svelte-missing-config-dependency", 1}, {"svelte-file-level", 1}} {
		ds, err := svelteFixtureRecords(f[tc.id].Stdout)
		if err != nil || len(ds) != tc.count {
			t.Fatalf("%s: %d records %v", tc.id, len(ds), err)
		}
		if tc.id == "svelte-file-level" && (ds[0].Start != nil || f[tc.id].Provenance != "synthetic-mutation") {
			t.Fatal("file-level case must not claim observation")
		}
	}
	for _, id := range []string{"svelte-malformed", "svelte-no-summary"} {
		if _, err := svelteFixtureRecords(f[id].Stdout); err == nil {
			t.Fatalf("accepted %s", id)
		}
	}
	ds, err := svelteFixtureRecords(f["svelte-findings"].Stdout)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, d := range ds {
		if strings.Contains(d.Message, "Cannot find name") {
			found = true
			line := strings.Split(f["svelte-findings"].Files[d.Filename], "\r\n")[3]
			prefix := strings.Split(line, "missing")[0]
			if d.Start.Line != 3 || d.Start.Character != 11 || d.End.Character != 18 || len(utf16.Encode([]rune(prefix))) != 11 || len(prefix) == 11 {
				t.Fatal("UTF-16/CRLF evidence changed")
			}
		}
	}
	if !found {
		t.Fatal("missing Unicode diagnostic")
	}
	// Config errors may have numeric code and no source; never assume either exists.
	config, err := svelteFixtureRecords(f["svelte-config-failure"].Stdout)
	if err != nil || string(config[0].Code) != "1136" || config[0].Source != "" {
		t.Fatal("config code/source evidence changed")
	}
}
