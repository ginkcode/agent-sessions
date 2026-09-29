package handoff

import (
	"fmt"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// trimToBudget fits the prompt document within the requested token budget.
// Core sections (1-5, 7, 8) are preserved; timeline (section 6) is trimmed
// in order: 1) old tool outputs, 2) old reasoning, 3) older assistant text.
func trimToBudget(
	s1, s2, s3, s4, s5, s6, s7, s8 string,
	timelineTurns []model.Message,
	opts Options,
) (doc string, trimmed bool, dropped []string) {
	budget := opts.BudgetTokens
	if budget <= 0 {
		// Unlimited budget: keep everything
		parts := []string{s1, s2, s3}
		if s4 != "" {
			parts = append(parts, s4)
		}
		parts = append(parts, s5, s6, s7, s8)
		return strings.Join(parts, "\n\n"), false, nil
	}

	maxChars := budget * 4

	// Assemble baseline core (1-5, 7, 8)
	coreParts := []string{s1, s2, s3}
	if s4 != "" {
		coreParts = append(coreParts, s4)
	}
	coreParts = append(coreParts, s5, s7, s8)
	coreDoc := strings.Join(coreParts, "\n\n")

	// If core + full timeline fits, we're done!
	fullTimelineParts := append([]string{s1, s2, s3}, s4, s5, s6, s7, s8)
	var filtered []string
	for _, p := range fullTimelineParts {
		if p != "" {
			filtered = append(filtered, p)
		}
	}
	fullDoc := strings.Join(filtered, "\n\n")
	if len(fullDoc) <= maxChars {
		return fullDoc, false, nil
	}

	// Trimming is needed.
	trimmed = true

	// Step 1: Trim old tool outputs in timeline
	t1 := buildTimelineWithoutToolOutputs(timelineTurns, opts.IncludeReasoning)
	cand1 := assemble(s1, s2, s3, s4, s5, t1, s7, s8)
	dropped = append(dropped, "Detailed tool outputs in older timeline turns")
	if len(cand1) <= maxChars {
		return cand1, trimmed, dropped
	}

	// Step 2: Drop old reasoning in timeline
	t2 := buildTimelineWithoutToolOutputs(timelineTurns, false)
	cand2 := assemble(s1, s2, s3, s4, s5, t2, s7, s8)
	dropped = append(dropped, "Reasoning blocks in older timeline turns")
	if len(cand2) <= maxChars {
		return cand2, trimmed, dropped
	}

	// Step 3: Condense older assistant text to first paragraph
	t3 := buildTimelineCondensed(timelineTurns)
	cand3 := assemble(s1, s2, s3, s4, s5, t3, s7, s8)
	dropped = append(dropped, "Older assistant responses condensed to first paragraph")
	if len(cand3) <= maxChars {
		return cand3, trimmed, dropped
	}

	// Step 4: Drop section 6 (timeline) entirely, keeping core sections (1-5, 7, 8)
	dropped = append(dropped, "Older conversation timeline (summary kept in Working State)")
	if len(coreDoc) <= maxChars {
		return coreDoc, trimmed, dropped
	}

	// Extreme case: Core sections alone exceed budget.
	// Middle-truncate long user messages in section 3.
	dropped = append(dropped, "Extremely long user prompts middle-truncated to fit budget")
	trimmedS3 := middleTruncate(s3, maxChars/3)
	extremeDoc := assemble(s1, s2, trimmedS3, s4, s5, "", s7, s8)
	return extremeDoc, trimmed, dropped
}

func assemble(s1, s2, s3, s4, s5, s6, s7, s8 string) string {
	parts := []string{s1, s2, s3}
	if s4 != "" {
		parts = append(parts, s4)
	}
	parts = append(parts, s5)
	if s6 != "" {
		parts = append(parts, s6)
	}
	parts = append(parts, s7, s8)
	return strings.Join(parts, "\n\n")
}

func buildTimelineWithoutToolOutputs(turns []model.Message, includeReasoning bool) string {
	if len(turns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Recent Conversation Timeline\n\n")
	for _, m := range turns {
		var body strings.Builder
		for _, p := range m.Parts {
			switch p.Kind {
			case model.PartText:
				if strings.TrimSpace(p.Text) != "" {
					body.WriteString(strings.TrimSpace(p.Text))
					body.WriteString("\n\n")
				}
			case model.PartReasoning:
				if includeReasoning && strings.TrimSpace(p.Text) != "" {
					body.WriteString("> *Reasoning:*\n> ")
					body.WriteString(strings.ReplaceAll(strings.TrimSpace(p.Text), "\n", "\n> "))
					body.WriteString("\n\n")
				}
			case model.PartTool:
				if p.Tool != nil {
					body.WriteString(fmt.Sprintf("- **Tool: `%s`** *[output omitted for length]*\n", p.Tool.Name))
				}
			}
		}
		if body.Len() == 0 {
			continue
		}
		roleLabel := "User"
		if m.Role == model.RoleAssistant {
			roleLabel = "Assistant"
		}
		b.WriteString(fmt.Sprintf("### %s\n", roleLabel))
		b.WriteString(body.String())
	}
	return b.String()
}

func buildTimelineCondensed(turns []model.Message) string {
	if len(turns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Recent Conversation Timeline (Condensed)\n\n")
	for _, m := range turns {
		var body strings.Builder
		for _, p := range m.Parts {
			if p.Kind == model.PartText && strings.TrimSpace(p.Text) != "" {
				text := strings.TrimSpace(p.Text)
				// Keep first paragraph
				if idx := strings.Index(text, "\n\n"); idx > 0 {
					text = text[:idx] + " …"
				}
				body.WriteString(text)
				body.WriteString("\n\n")
			}
		}
		if body.Len() == 0 {
			continue
		}
		roleLabel := "User"
		if m.Role == model.RoleAssistant {
			roleLabel = "Assistant"
		}
		b.WriteString(fmt.Sprintf("### %s\n", roleLabel))
		b.WriteString(body.String())
	}
	return b.String()
}

func middleTruncate(s string, maxLen int) string {
	if len(s) <= maxLen || maxLen <= 50 {
		return s
	}
	half := (maxLen - 30) / 2
	head := s[:half]
	tail := s[len(s)-half:]
	return head + "\n\n[... content truncated for token budget ...]\n\n" + tail
}
