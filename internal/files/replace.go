package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ErrInvalidReplacement reports replacement text a file could not hold:
// invalid UTF-8, a NUL or a carriage return.
var ErrInvalidReplacement = errors.New("invalid replacement text")

// ReplaceMatch names one match as Search reported it: its line and its start
// column in UTF-16 code units.
type ReplaceMatch struct {
	Line  int `json:"line"`
	Start int `json:"start"`
}

// ReplaceFile is one file to change and the revision it was searched at.
type ReplaceFile struct {
	Path             string `json:"path"`
	ExpectedRevision string `json:"expectedRevision"`
	// Matches, when set, are the only matches replaced; otherwise every
	// match in the file is.
	Matches []ReplaceMatch `json:"matches,omitempty"`
}

// ReplaceRequest replaces the matches of Query in Files with Replacement.
// For a regular expression, $1 and ${name} in Replacement expand to the
// groups of each match, as in Go's Regexp.Expand ($$ is a dollar sign);
// otherwise Replacement is taken as it is.
type ReplaceRequest struct {
	Query       SearchQuery   `json:"query"`
	Replacement string        `json:"replacement"`
	Files       []ReplaceFile `json:"files"`
}

// ReplaceStatus is the outcome for one file.
type ReplaceStatus string

const (
	ReplaceStatusReplaced ReplaceStatus = "replaced"
	// ReplaceStatusConflict: the file changed since it was searched;
	// nothing was written.
	ReplaceStatusConflict ReplaceStatus = "conflict"
	// ReplaceStatusSkipped: the file may not be or need not be changed (see
	// Reason); nothing was written.
	ReplaceStatusSkipped ReplaceStatus = "skipped"
	// ReplaceStatusFailed: an error, in Message. The file is unchanged
	// unless Message reports an incomplete write (ErrSaveIncomplete).
	ReplaceStatusFailed ReplaceStatus = "failed"
)

// Reasons a file is skipped, besides the Edit reasons for files that cannot
// be edited (EditProtected, EditLinked, EditReadOnly, EditTooLarge, which
// also covers a result that would grow past MaxEditBytes).
const (
	// ReplaceUnsupportedText: binary, or mixed or bare-CR line endings.
	ReplaceUnsupportedText = "unsupported_text"
	// ReplaceStaleMatch: a match to replace is not there; the query or the
	// match list does not describe the file.
	ReplaceStaleMatch = "stale_match"
	// ReplaceDuplicate: the file is listed again in the same request.
	ReplaceDuplicate = "duplicate"
)

// ReplaceFileResult is what happened to one file.
type ReplaceFileResult struct {
	Path   string        `json:"path"`
	Status ReplaceStatus `json:"status"`
	// Replaced is the number of matches replaced, and Revision the file's
	// new revision, for replaced.
	Replaced int    `json:"replaced,omitempty"`
	Revision string `json:"revision,omitempty"`
	// ObservedRevision is the file's current revision, for a conflict.
	ObservedRevision string `json:"observedRevision,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Message          string `json:"message,omitempty"`
}

// ReplaceResult has one entry per requested file, in request order.
type ReplaceResult struct {
	Files []ReplaceFileResult `json:"files"`
	// Replaced is the number of matches replaced in all files.
	Replaced int `json:"replaced"`
}

// Replace replaces matches of a search in files under root, through the
// same checks as Save: the policy, the edit size limit, and the revision the
// file was searched at, so a file changed since is never written. Each file
// keeps its byte order mark and line endings. Matches are found line by
// line in the text with LF endings, as Search finds them. One file's
// failure does not stop the others; when ctx ends, the files not yet
// reached fail with its error.
func Replace(ctx context.Context, policy EditPolicy, root string, req ReplaceRequest) (ReplaceResult, error) {
	if req.Query.Pattern == "" {
		return ReplaceResult{}, fmt.Errorf("%w: empty search pattern", ErrInvalidPattern)
	}
	if !utf8.ValidString(req.Replacement) || strings.ContainsAny(req.Replacement, "\x00\r") {
		return ReplaceResult{}, ErrInvalidReplacement
	}
	re, err := CompileQuery(req.Query)
	if err != nil {
		return ReplaceResult{}, err
	}
	r := replacer{re: re, regex: req.Query.Regex, template: req.Replacement}
	out := ReplaceResult{Files: make([]ReplaceFileResult, 0, len(req.Files))}
	seen := map[string]bool{}
	for _, f := range req.Files {
		var res ReplaceFileResult
		switch {
		case ctx.Err() != nil:
			res = ReplaceFileResult{Path: f.Path, Status: ReplaceStatusFailed, Message: ctx.Err().Error()}
		case seen[f.Path]:
			res = ReplaceFileResult{Path: f.Path, Status: ReplaceStatusSkipped, Reason: ReplaceDuplicate}
		default:
			seen[f.Path] = true
			res = replaceFile(ctx, policy, root, f, r)
		}
		out.Replaced += res.Replaced
		out.Files = append(out.Files, res)
	}
	return out, nil
}

// replacer computes the new text of one line.
type replacer struct {
	re       *regexp.Regexp
	regex    bool
	template string
}

// replaceFile replaces in one file, holding saveSlot.
func replaceFile(ctx context.Context, policy EditPolicy, root string, f ReplaceFile, r replacer) ReplaceFileResult {
	res := ReplaceFileResult{Path: f.Path}
	fail := func(err error) ReplaceFileResult {
		if reason := replaceSkipReason(err); reason != "" {
			res.Status, res.Reason = ReplaceStatusSkipped, reason
			return res
		}
		res.Status, res.Message = ReplaceStatusFailed, err.Error()
		return res
	}
	if !revisionPattern.MatchString(f.ExpectedRevision) {
		return fail(ErrInvalidRevision)
	}
	unlock, err := lockSaves(ctx)
	if err != nil {
		return fail(err)
	}
	defer unlock()
	t, err := openEditTargetFlag(ctx, policy, root, f.Path, os.O_RDWR)
	if err != nil {
		return fail(err)
	}
	defer t.Close()
	if t.revision != f.ExpectedRevision {
		res.Status, res.ObservedRevision = ReplaceStatusConflict, t.revision
		return res
	}

	text := string(t.data)
	if t.format.BOM {
		text = text[len(utf8BOM):]
	}
	if t.format.Newline == NewlineCRLF {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	next, n, ok := r.replaceText(text, f.Matches)
	if !ok || n == 0 {
		res.Status, res.Reason = ReplaceStatusSkipped, ReplaceStaleMatch
		return res
	}
	data, err := encodeEditText(next, t.format)
	if err != nil {
		return fail(err)
	}
	if err := t.revalidate(policy, root, f.Path); err != nil {
		return fail(err)
	}
	// Last chance to stop: once the write starts it is finished.
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := t.overwrite(data); err != nil {
		return fail(err)
	}
	res.Status, res.Replaced, res.Revision = ReplaceStatusReplaced, n, revisionOf(data)
	return res
}

// replaceText returns text with the chosen matches replaced and how many
// were; ok is false when a chosen match is not in text.
func (r replacer) replaceText(text string, chosen []ReplaceMatch) (string, int, bool) {
	want := map[ReplaceMatch]bool{}
	for _, m := range chosen {
		want[m] = true
	}
	var b strings.Builder
	b.Grow(len(text))
	n, found := 0, 0
	for line, rest := 1, text; ; line++ {
		s, after, more := strings.Cut(rest, "\n")
		last := 0
		for _, at := range r.re.FindAllStringSubmatchIndex(s, -1) {
			if at[0] == at[1] {
				continue // an empty match marks nothing, as in Search
			}
			if len(chosen) > 0 {
				if !want[ReplaceMatch{Line: line, Start: utf16Len(s[:at[0]])}] {
					continue
				}
				found++
			}
			b.WriteString(s[last:at[0]])
			b.WriteString(r.expand(s, at))
			last = at[1]
			n++
		}
		b.WriteString(s[last:])
		if !more {
			break
		}
		b.WriteByte('\n')
		rest = after
	}
	if len(chosen) > 0 && found != len(want) {
		return "", 0, false
	}
	return b.String(), n, true
}

// expand returns the replacement for the match at in s.
func (r replacer) expand(s string, at []int) string {
	if !r.regex {
		return r.template
	}
	return string(r.re.ExpandString(nil, r.template, s, at))
}

// replaceSkipReason maps an error that leaves a file as it is, by design,
// to a skip reason; "" for a failure.
func replaceSkipReason(err error) string {
	switch {
	case errors.Is(err, ErrEditProtected):
		return EditProtected
	case errors.Is(err, ErrEditLink):
		return EditLinked
	case errors.Is(err, ErrEditReadOnly):
		return EditReadOnly
	case errors.Is(err, ErrEditTooLarge):
		return EditTooLarge
	case errors.Is(err, ErrUnsupportedEditText):
		return ReplaceUnsupportedText
	}
	return ""
}
