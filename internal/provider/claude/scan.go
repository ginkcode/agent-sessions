package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
	SubagentTask          string `json:"subagentTask,omitempty"`
	SubagentMetaPath      string `json:"subagentMetaPath,omitempty"`
	SubagentMetaSize      int64  `json:"subagentMetaSize,omitempty"`
	SubagentMetaModTimeNs int64  `json:"subagentMetaModTimeNs,omitempty"`
	LinesRead             int    `json:"linesRead,omitempty"`
	// PendingTools holds the IDs of tool calls without a result yet.
	PendingTools []string `json:"pendingTools,omitempty"`
	path         string
	lineNo       int
}

// checkpointVersion 4 normalizes Windows CWDs in their own style; 5 adds
// Turn. A full rescan replaces metadata cached by earlier versions.
const checkpointVersion = 5

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
		if gjson.GetBytes(line, "isCompactSummary").Bool() {
			return
		}
		if !gjson.GetBytes(line, "isMeta").Bool() {
			c.userTurn(gjson.GetBytes(line, "message.content"), gjson.GetBytes(line, "isSidechain").Bool())
		}
		if gjson.GetBytes(line, "isMeta").Bool() {
			// A subagent's task prompt can arrive as its opening meta record.
			// It names the subagent when the spawn metadata has no description.
			if c.Meta.ParentID != "" && !c.FirstUserSeen && c.SubagentTask == "" {
				text, _ := userContent(gjson.GetBytes(line, "message.content"))
				c.SubagentTask = model.TruncateRunes(model.OneLine(text), 300)
			}
			return
		}
		text, counts := userContent(gjson.GetBytes(line, "message.content"))
		if counts {
			c.Meta.Counts.User++
			if !c.FirstUserSeen {
				c.FirstUserSeen = true
				c.Meta.FirstPrompt = model.TruncateRunes(model.OneLine(text), 300)
			}
			if prompt := model.TruncateRunes(model.OneLine(text), 300); prompt != "" && !isHarnessNotice(text) {
				c.LastUserPrompt = prompt
			}
		}
	case "assistant":
		if gjson.GetBytes(line, "isApiErrorMessage").Bool() {
			// Claude Code gave up on the request: the turn is over.
			if !gjson.GetBytes(line, "isSidechain").Bool() {
				c.PendingTools = nil
				c.Meta.Turn = model.TurnDone
			}
			return
		}
		if !gjson.GetBytes(line, "isSidechain").Bool() {
			c.assistantTurn(gjson.GetBytes(line, "message.content"), gjson.GetBytes(line, "message.stop_reason").String())
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

// userTurn follows the turn through a user record: a prompt starts one, an
// interruption ends it, and tool results answer pending calls.
func (c *checkpoint) userTurn(content gjson.Result, sidechain bool) {
	if sidechain {
		return
	}
	text, counts := userContent(content)
	if strings.HasPrefix(strings.TrimSpace(text), "[Request interrupted by user") {
		c.PendingTools = nil
		c.Meta.Turn = model.TurnDone
		return
	}
	if counts {
		// A prompt; any call still pending was abandoned.
		c.PendingTools = nil
		c.Meta.Turn = model.TurnWorking
		return
	}
	answered := false
	content.ForEach(func(_, part gjson.Result) bool {
		if part.Get("type").String() == "tool_result" {
			answered = true
			id := part.Get("tool_use_id").String()
			c.PendingTools = slices.DeleteFunc(c.PendingTools, func(p string) bool { return p == id })
		}
		return true
	})
	if answered {
		c.Meta.Turn = c.pendingOr(model.TurnWorking)
	}
}

// assistantTurn follows the turn through an assistant record: a tool call
// waits for its result, and a final stop reason ends the turn.
func (c *checkpoint) assistantTurn(content gjson.Result, stopReason string) {
	content.ForEach(func(_, part gjson.Result) bool {
		if part.Get("type").String() == "tool_use" {
			if id := part.Get("id").String(); id != "" && !slices.Contains(c.PendingTools, id) {
				c.PendingTools = append(c.PendingTools, id)
			}
		}
		return true
	})
	switch stopReason {
	case "end_turn", "stop_sequence", "max_tokens", "refusal":
		c.Meta.Turn = c.pendingOr(model.TurnDone)
	case "":
		// Older Claude Code wrote each block as it streamed, without a stop
		// reason: text last reads as the answer, a tool call follows at once.
		last := ""
		content.ForEach(func(_, part gjson.Result) bool {
			last = part.Get("type").String()
			return true
		})
		if last == "text" {
			c.Meta.Turn = c.pendingOr(model.TurnDone)
		} else {
			c.Meta.Turn = c.pendingOr(model.TurnWorking)
		}
	default:
		c.Meta.Turn = c.pendingOr(model.TurnWorking)
	}
}

// pendingOr is TurnTool while a call waits for its result, else state.
func (c *checkpoint) pendingOr(state string) string {
	if len(c.PendingTools) > 0 {
		return model.TurnTool
	}
	return state
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

// isHarnessNotice reports user records that Claude Code writes on the user's
// behalf. They still count as user turns but never name a session.
func isHarnessNotice(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "<task-notification>") || strings.HasPrefix(text, "[Request interrupted by user")
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
	case c.SubagentTask != "":
		meta.Title = c.SubagentTask
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

// Only unchanged decisions are cached, never mutable parser state. Raw bytes are
// copied (not hashed or aliased to caller-owned ScanState), so even an in-place
// checkpoint edit must miss. Admission limits bound both bytes and bookkeeping.
const (
	checkpointReuseLimit    = 1024
	checkpointReuseMaxBytes = 16 << 10
)

type checkpointSummary struct {
	raw                   string
	size, modTime, offset int64
	info                  os.FileInfo
	metaPath              string
	metaSize, metaModTime int64
}

type checkpointReuse struct {
	mu      sync.Mutex
	entries map[string]checkpointSummary
}

func (c *checkpointReuse) lookup(path string, old provider.SourceState) (checkpointSummary, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.entries[path]
	return s, ok && s.size == old.Size && s.modTime == old.ModTimeNs && s.offset == old.Offset && bytes.Equal([]byte(s.raw), old.Checkpoint)
}

func (c *checkpointReuse) remember(src source, state provider.SourceState, info os.FileInfo, cp *checkpoint) {
	if len(state.Checkpoint) > checkpointReuseMaxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]checkpointSummary)
	}
	if _, ok := c.entries[src.Path]; !ok && len(c.entries) >= checkpointReuseLimit {
		return
	}
	c.entries[src.Path] = checkpointSummary{
		raw: string(state.Checkpoint), size: state.Size, modTime: state.ModTimeNs, offset: state.Offset, info: info,
		metaPath: cp.SubagentMetaPath, metaSize: cp.SubagentMetaSize, metaModTime: cp.SubagentMetaModTimeNs,
	}
}

// Prune on discovery, including an empty/reset scan. Concurrent scans can evict
// useful entries, but cannot authorize reuse of a different checkpoint.
func (c *checkpointReuse) prune(sources []source, prev provider.ScanState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	present := make(map[string]bool, len(sources))
	for _, src := range sources {
		present[src.Path] = true
	}
	for path := range c.entries {
		if _, ok := prev.Sources[path]; !ok || !present[path] {
			delete(c.entries, path)
		}
	}
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
	p.checkpointReuse.prune(sources, prev)
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

	work := provider.ScanWorkFromContext(ctx)
	var cp checkpoint
	metaSize, metaModTimeNs := subagentMetaStat(src.MetaPath)
	summary, known := p.checkpointReuse.lookup(src.Path, old)
	sameFile := !known || os.SameFile(summary.info, info)
	if hasOld && known && sameFile && old.Size == info.Size() && old.ModTimeNs == info.ModTime().UnixNano() &&
		summary.metaPath == src.MetaPath && summary.metaSize == metaSize && summary.metaModTime == metaModTimeNs {
		return out
	}
	resume := sameFile && hasOld && len(old.Checkpoint) != 0 && old.Offset >= 0 && old.Size <= info.Size() && old.Offset <= info.Size()
	if resume {
		if work != nil {
			work.CheckpointDecodes.Add(1)
		}
		if err := json.Unmarshal(old.Checkpoint, &cp); err != nil {
			out.diag.Warn(src.Path, 0, "invalid scan checkpoint: %v", err)
			resume = false
		} else if cp.Version != checkpointVersion {
			resume = false
		}
	}
	metaChanged := !resume || cp.SubagentMetaPath != src.MetaPath || cp.SubagentMetaSize != metaSize || cp.SubagentMetaModTimeNs != metaModTimeNs
	if resume && hasOld && old.Size == info.Size() && old.ModTimeNs == info.ModTime().UnixNano() && !metaChanged {
		p.checkpointReuse.remember(src, old, info, &cp)
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
	if work != nil {
		work.SourceOpens.Add(1)
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
		if work != nil {
			work.Records.Add(1)
			work.RecordBytes.Add(int64(len(line)))
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
	if work != nil {
		work.CheckpointEncodes.Add(1)
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
	p.checkpointReuse.remember(src, out.state, info, &cp)
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
