package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ginkcode/agent-sessions/internal/jsonl"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"

	"github.com/tidwall/gjson"
)

// The committed token total in Meta excludes PendingUsage until finalize. A
// Claude API message may span multiple records, and its final record has the
// authoritative usage. Keeping that message pending prevents double-counting
// when a scan resumes in the middle of it.
type checkpoint struct {
	Version         int               `json:"version,omitempty"`
	Meta            model.SessionMeta `json:"meta"`
	LastAssistantID string            `json:"lastAssistantId"`
	PendingUsage    model.TokenUsage  `json:"pendingUsage"`
	AITitle         string            `json:"aiTitle"`
	Summary         string            `json:"summary"`

	FirstUserSeen         bool   `json:"firstUserSeen,omitempty"`
	LastUserPrompt        string `json:"lastUserPrompt,omitempty"`
	SubagentDescription   string `json:"subagentDescription,omitempty"`
	SubagentMetaPath      string `json:"subagentMetaPath,omitempty"`
	SubagentMetaSize      int64  `json:"subagentMetaSize,omitempty"`
	SubagentMetaModTimeNs int64  `json:"subagentMetaModTimeNs,omitempty"`
	LinesRead             int    `json:"linesRead,omitempty"`
	path                  string
	lineNo                int
}

const checkpointVersion = 2

var ignoredRecordTypes = map[string]bool{
	"attachment": true, "last-prompt": true, "mode": true,
	"cost-state": true, "queue-operation": true, "atis-latch": true,
	"system": true,
}

func (c *checkpoint) consume(line []byte, d *provider.Diagnostics) {
	if !gjson.ValidBytes(line) {
		d.ParseErrors++
		d.Warn(c.path, c.lineNo, "invalid JSON record")
		return
	}

	stamp := gjson.GetBytes(line, "timestamp").String()
	if stamp != "" {
		if ts, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
			ts = ts.UTC()
			if c.Meta.CreatedAt.IsZero() || ts.Before(c.Meta.CreatedAt) {
				c.Meta.CreatedAt = ts
			}
			if ts.After(c.Meta.UpdatedAt) {
				c.Meta.UpdatedAt = ts
			}
		} else {
			d.Warn(c.path, c.lineNo, "invalid timestamp %q", stamp)
		}
	}

	if c.Meta.CWD == "" {
		if cwd := gjson.GetBytes(line, "cwd").String(); cwd != "" {
			c.Meta.CWD = pathutil.NormalizeDir(cwd)
		}
	}
	if branch := gjson.GetBytes(line, "gitBranch").String(); branch != "" && branch != "HEAD" {
		c.Meta.GitBranch = branch
	}
	if version := gjson.GetBytes(line, "version").String(); version != "" {
		c.Meta.AgentVersion = version
	}

	switch kind := gjson.GetBytes(line, "type").String(); kind {
	case "user":
		if gjson.GetBytes(line, "isMeta").Bool() || gjson.GetBytes(line, "isCompactSummary").Bool() {
			return
		}
		text, counts := userContent(gjson.GetBytes(line, "message.content"))
		if counts {
			c.Meta.Counts.User++
			if !c.FirstUserSeen {
				c.FirstUserSeen = true
				c.Meta.FirstPrompt = model.TruncateRunes(model.OneLine(text), 300)
			}
			if prompt := model.TruncateRunes(model.OneLine(text), 300); prompt != "" {
				c.LastUserPrompt = prompt
			}
		}
	case "assistant":
		if gjson.GetBytes(line, "isApiErrorMessage").Bool() {
			return
		}
		id := gjson.GetBytes(line, "message.id").String()
		if id != "" && id != c.LastAssistantID {
			c.Meta.Tokens = c.Meta.Tokens.Add(c.PendingUsage)
			c.LastAssistantID = id
			c.Meta.Counts.Assistant++
		}
		if id != "" {
			usage := gjson.GetBytes(line, "message.usage")
			c.PendingUsage = model.TokenUsage{
				Input:      usage.Get("input_tokens").Int(),
				Output:     usage.Get("output_tokens").Int(),
				Reasoning:  usage.Get("output_tokens_details.thinking_tokens").Int(),
				CacheRead:  usage.Get("cache_read_input_tokens").Int(),
				CacheWrite: usage.Get("cache_creation_input_tokens").Int(),
			}
		}
		if candidate := gjson.GetBytes(line, "message.model").String(); candidate != "" && candidate != "<synthetic>" {
			c.Meta.Model = candidate
		}
		gjson.GetBytes(line, "message.content").ForEach(func(_, part gjson.Result) bool {
			if part.Get("type").String() == "tool_use" {
				c.Meta.Counts.ToolCalls++
			}
			return true
		})
	case "ai-title":
		if title := gjson.GetBytes(line, "aiTitle").String(); title != "" {
			c.AITitle = title
		}
	case "summary":
		if title := gjson.GetBytes(line, "summary").String(); title != "" {
			c.Summary = title
		}
	default:
		if !ignoredRecordTypes[kind] {
			d.Unknown(kind)
		}
	}
}

// userContent reports whether this is a conversational user message and
// returns its first non-command text for the FirstPrompt fallback. A record
// containing only tool results is not a user message. An image counts as a
// message even if it has no text to use as its title.
func userContent(content gjson.Result) (string, bool) {
	if content.Type == gjson.String {
		text := content.String()
		return text, strings.TrimSpace(text) != "" && !isCommandText(text)
	}
	if !content.IsArray() {
		return "", false
	}
	var prompt string
	counts := false
	content.ForEach(func(_, part gjson.Result) bool {
		switch part.Get("type").String() {
		case "image":
			counts = true
		case "text":
			text := part.Get("text").String()
			if !isCommandText(text) {
				counts = true
				if prompt == "" {
					prompt = text
				}
			}
		}
		return true
	})
	return prompt, counts
}

func isCommandText(text string) bool {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{"<command-", "<local-command-", "Caveat:", "<system-reminder>"} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func (c *checkpoint) finalize(fileModTime time.Time) model.SessionMeta {
	meta := c.Meta
	meta.Tokens = meta.Tokens.Add(c.PendingUsage)
	if meta.UpdatedAt.IsZero() {
		meta.UpdatedAt = fileModTime.UTC()
	}
	switch {
	case c.AITitle != "":
		meta.Title = c.AITitle
	case c.SubagentDescription != "":
		meta.Title = c.SubagentDescription
	case c.Summary != "":
		meta.Title = c.Summary
	case c.LastUserPrompt != "":
		meta.Title = c.LastUserPrompt
	case meta.FirstPrompt != "": // Checkpoints written before LastUserPrompt existed.
		meta.Title = meta.FirstPrompt
	default:
		meta.Title = "(untitled)"
	}
	return meta
}

type sourceScan struct {
	state   provider.SourceState
	meta    model.SessionMeta
	diag    provider.Diagnostics
	changed bool
	err     error
}

// Scan discovers Claude transcripts and returns only new or changed sessions.
// Work is parallelized by file; results and diagnostics are merged in source
// order so scheduling does not affect the output.
func (p *Provider) Scan(ctx context.Context, prev provider.ScanState) (provider.ScanResult, error) {
	sources, err := p.discover(ctx)
	if err != nil {
		return provider.ScanResult{}, err
	}
	result := provider.ScanResult{State: provider.ScanState{Sources: make(map[string]provider.SourceState, len(sources))}}
	if len(sources) == 0 {
		for path, old := range prev.Sources {
			result.Removed = append(result.Removed, removedRef(path, old))
		}
		sort.Slice(result.Removed, func(i, j int) bool { return result.Removed[i].Key() < result.Removed[j].Key() })
		return result, nil
	}

	workers := min(8, runtime.GOMAXPROCS(0))
	jobs := make(chan int)
	outcomes := make([]sourceScan, len(sources))
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				old, hasOld := prev.Sources[sources[index].Path]
				outcomes[index] = p.scanSource(ctx, sources[index], old, hasOld)
			}
		}()
	}
	for i := range sources {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	present := make(map[string]bool, len(sources))
	for i, src := range sources {
		present[src.Path] = true
		out := outcomes[i]
		if out.err != nil {
			return provider.ScanResult{}, out.err
		}
		result.Diag.Merge(out.diag)
		if out.changed {
			result.Changed = append(result.Changed, out.meta)
			result.State.Sources[src.Path] = out.state
		} else if old, ok := prev.Sources[src.Path]; ok {
			result.State.Sources[src.Path] = old
		}
	}
	for path, old := range prev.Sources {
		if !present[path] {
			result.Removed = append(result.Removed, removedRef(path, old))
		}
	}
	sort.Slice(result.Removed, func(i, j int) bool { return result.Removed[i].Key() < result.Removed[j].Key() })

	if err := ctx.Err(); err != nil {
		return provider.ScanResult{}, err
	}
	live, err := p.Live(ctx)
	if err != nil {
		return provider.ScanResult{}, err
	}
	for i := range result.Changed {
		m := &result.Changed[i]
		if m.ParentID == "" {
			if info, ok := live[m.Ref.ID]; ok {
				m.Live = true
				m.LiveStatus = info.Status
			}
		}
	}
	return result, nil
}

func (p *Provider) scanSource(ctx context.Context, src source, old provider.SourceState, hasOld bool) sourceScan {
	var out sourceScan
	if err := ctx.Err(); err != nil {
		out.err = err
		return out
	}
	info, err := os.Stat(src.Path)
	if err != nil {
		out.diag.Warn(src.Path, 0, "stat transcript: %v", err)
		return out
	}
	if !info.Mode().IsRegular() {
		out.diag.Warn(src.Path, 0, "not a regular transcript")
		return out
	}

	var cp checkpoint
	resume := hasOld && len(old.Checkpoint) != 0 && old.Offset >= 0 && old.Size <= info.Size() && old.Offset <= info.Size()
	if resume {
		if err := json.Unmarshal(old.Checkpoint, &cp); err != nil {
			out.diag.Warn(src.Path, 0, "invalid scan checkpoint: %v", err)
			resume = false
		} else if cp.Version != checkpointVersion {
			resume = false
		}
	}
	metaSize, metaModTimeNs := subagentMetaStat(src.MetaPath)
	metaChanged := !resume || cp.SubagentMetaPath != src.MetaPath || cp.SubagentMetaSize != metaSize || cp.SubagentMetaModTimeNs != metaModTimeNs
	if resume && hasOld && old.Size == info.Size() && old.ModTimeNs == info.ModTime().UnixNano() && !metaChanged {
		return out
	}
	// A same-size rewrite is not append-only. Rebuild rather than returning the
	// previous aggregate with no new lines read.
	if resume && old.Size == info.Size() && old.ModTimeNs != info.ModTime().UnixNano() {
		resume = false
	}
	if !resume {
		cp = checkpoint{}
		metaChanged = true
	}
	cp.path = src.Path
	cp.Meta.Ref = sessionRef(src)
	cp.Meta.ParentID = src.ParentID
	cp.Meta.SourcePath = src.Path
	if metaChanged {
		cp.SubagentMetaPath = src.MetaPath
		cp.SubagentMetaSize = metaSize
		cp.SubagentMetaModTimeNs = metaModTimeNs
		cp.SubagentDescription = ""
		cp.Meta.AgentName = ""
		cp.Meta.ParentToolCallID = ""
		if src.MetaPath != "" {
			data, err := os.ReadFile(src.MetaPath)
			if err != nil {
				out.diag.Warn(src.MetaPath, 0, "read subagent metadata: %v", err)
			} else {
				var meta struct {
					AgentType   string `json:"agentType"`
					Description string `json:"description"`
					ToolUseID   string `json:"toolUseId"`
				}
				if err := json.Unmarshal(data, &meta); err != nil {
					out.diag.ParseErrors++
					out.diag.Warn(src.MetaPath, 0, "invalid subagent metadata: %v", err)
				} else {
					cp.Meta.AgentName = meta.AgentType
					cp.Meta.ParentToolCallID = meta.ToolUseID
					cp.SubagentDescription = meta.Description
				}
			}
		}
	}

	offset := int64(0)
	if resume {
		offset = old.Offset
	}
	r, err := jsonl.Open(src.Path, offset)
	if err != nil {
		out.diag.Warn(src.Path, 0, "open transcript: %v", err)
		return out
	}
	defer func() { _ = r.Close() }()
	lineBase := cp.LinesRead
	for {
		if err := ctx.Err(); err != nil {
			out.err = err
			return out
		}
		line, _, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		cp.lineNo = lineBase + r.LineNo()
		if errors.Is(err, jsonl.ErrLineTooLong) {
			out.diag.ParseErrors++
			out.diag.Warn(src.Path, cp.lineNo, "JSONL record exceeds maximum line length")
			continue
		}
		if err != nil {
			out.err = fmt.Errorf("read transcript %s: %w", src.Path, err)
			return out
		}
		cp.consume(line, &out.diag)
	}
	cp.LinesRead = lineBase + r.LineNo()
	cp.Version = checkpointVersion
	out.meta = cp.finalize(info.ModTime())
	if out.meta.CWD != "" {
		out.meta.CWDMissing = !pathutil.Exists(out.meta.CWD)
		if p.git != nil {
			if repo, ok := p.git.Resolve(out.meta.CWD); ok {
				out.meta.RepoRoot = repo.MainRoot
			}
		}
	}
	checkpointBytes, err := json.Marshal(cp)
	if err != nil {
		out.err = fmt.Errorf("marshal scan checkpoint %s: %w", src.Path, err)
		return out
	}
	out.changed = true
	out.state = provider.SourceState{
		Size: info.Size(), ModTimeNs: info.ModTime().UnixNano(),
		Offset: r.Offset(), Checkpoint: checkpointBytes,
	}
	return out
}

func subagentMetaStat(path string) (int64, int64) {
	if path == "" {
		return 0, 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	return info.Size(), info.ModTime().UnixNano()
}

func sessionRef(src source) model.SessionRef {
	id := strings.TrimSuffix(filepath.Base(src.Path), ".jsonl")
	if src.ParentID != "" {
		id = src.ParentID + "/agent-" + src.AgentID
	}
	return model.SessionRef{Agent: model.AgentClaude, ID: id}
}

func removedRef(path string, old provider.SourceState) model.SessionRef {
	var cp checkpoint
	if json.Unmarshal(old.Checkpoint, &cp) == nil && cp.Meta.Ref.ID != "" {
		return cp.Meta.Ref
	}
	return model.SessionRef{Agent: model.AgentClaude, ID: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
}
