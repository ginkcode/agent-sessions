package bundle

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// MaxFidelityBytes is the total amount of resolved blob content one bundle
// transcript may add on top of the display transcript. Content past the cap
// stays truncated and is named in FidelityReport rather than cut silently.
const MaxFidelityBytes = 64 << 20

// BlobFunc resolves one lazy reference (a truncated tool output or an
// attachment) to its full bytes. It is provider.Provider.Blob.
type BlobFunc func(ctx context.Context, ref model.SessionRef, key string) ([]byte, error)

// FidelityReport records what full fidelity did and could not do.
type FidelityReport struct {
	// Resolved is how many truncated outputs and attachments were restored.
	Resolved int `json:"resolved"`
	// ResolvedBytes is the total size of the restored content.
	ResolvedBytes int64 `json:"resolvedBytes"`
	// Overflow names items left truncated because the cap was reached,
	// as "<sessionID> <ref>".
	Overflow []string `json:"overflow,omitempty"`
	// Unavailable names items whose blob could not be loaded, as
	// "<sessionID> <ref>: <reason>".
	Unavailable []string `json:"unavailable,omitempty"`
}

// ResolveFidelity returns a deep copy of transcripts with truncated tool
// outputs and attachment contents restored from blob. Restoration stops once
// MaxFidelityBytes of restored content has been added; later items keep their
// display form and are listed in the report.
func ResolveFidelity(ctx context.Context, transcripts []model.Transcript, blob BlobFunc) ([]model.Transcript, FidelityReport, error) {
	out := make([]model.Transcript, len(transcripts))
	var report FidelityReport
	var used int64
	for i, tr := range transcripts {
		if err := ctx.Err(); err != nil {
			return nil, FidelityReport{}, err
		}
		out[i] = copyTranscript(tr)
		for mi := range out[i].Messages {
			msg := &out[i].Messages[mi]
			for pi := range msg.Parts {
				part := &msg.Parts[pi]
				switch {
				case part.Tool != nil && part.Tool.OutputTruncated && part.Tool.OutputRef != "":
					used = resolveTool(ctx, blob, out[i].Meta.Ref, part.Tool, used, &report)
				case part.File != nil && part.File.Ref != "":
					used = resolveFile(ctx, blob, out[i].Meta.Ref, part, used, &report)
				}
			}
		}
	}
	return out, report, nil
}

// resolveTool replaces a truncated tool output with the full blob text.
// Binary blobs are not tool output, so they are reported and left as-is.
func resolveTool(ctx context.Context, blob BlobFunc, ref model.SessionRef, tool *model.ToolCall, used int64, report *FidelityReport) int64 {
	label := ref.ID + " " + tool.OutputRef
	if used >= MaxFidelityBytes {
		report.Overflow = append(report.Overflow, label)
		return used
	}
	data, err := blob(ctx, ref, tool.OutputRef)
	if err != nil {
		report.Unavailable = append(report.Unavailable, fmt.Sprintf("%s: %v", label, err))
		return used
	}
	if !utf8.Valid(data) {
		report.Unavailable = append(report.Unavailable, label+": tool output is not text")
		return used
	}
	if used+int64(len(data)) > MaxFidelityBytes {
		report.Overflow = append(report.Overflow, label)
		return used
	}
	tool.Output = string(data)
	tool.OutputTruncated = false
	tool.OutputRef = ""
	report.Resolved++
	report.ResolvedBytes += int64(len(data))
	return used + int64(len(data))
}

// resolveFile inlines an attachment's bytes into the part text when they are
// valid UTF-8. Binary attachments stay referenced: the bundle has no place
// for arbitrary bytes inside the transcript.
func resolveFile(ctx context.Context, blob BlobFunc, ref model.SessionRef, part *model.Part, used int64, report *FidelityReport) int64 {
	file := part.File
	label := ref.ID + " " + file.Ref
	if used >= MaxFidelityBytes {
		report.Overflow = append(report.Overflow, label)
		return used
	}
	data, err := blob(ctx, ref, file.Ref)
	if err != nil {
		report.Unavailable = append(report.Unavailable, fmt.Sprintf("%s: %v", label, err))
		return used
	}
	if !utf8.Valid(data) {
		report.Unavailable = append(report.Unavailable, label+": attachment is not text")
		return used
	}
	if used+int64(len(data)) > MaxFidelityBytes {
		report.Overflow = append(report.Overflow, label)
		return used
	}
	name := file.Name
	if name == "" {
		name = "attachment"
	}
	part.Kind = model.PartText
	part.Text = "Attached file " + name + ":\n" + string(data)
	part.File = nil
	report.Resolved++
	report.ResolvedBytes += int64(len(data))
	return used + int64(len(data))
}

func copyTranscript(tr model.Transcript) model.Transcript {
	out := tr
	out.Messages = make([]model.Message, len(tr.Messages))
	for i, msg := range tr.Messages {
		out.Messages[i] = msg
		out.Messages[i].Parts = make([]model.Part, len(msg.Parts))
		for j, part := range msg.Parts {
			out.Messages[i].Parts[j] = part
			if part.Tool != nil {
				tool := *part.Tool
				out.Messages[i].Parts[j].Tool = &tool
			}
			if part.File != nil {
				file := *part.File
				out.Messages[i].Parts[j].File = &file
			}
		}
	}
	return out
}
