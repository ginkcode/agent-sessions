package codex

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

	"github.com/tidwall/gjson"

	"github.com/ginkcode/agent-sessions/internal/jsonl"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// checkpoint preserves scanner state across incremental runs.
// Counts and usage are saved so append resume avoids re-reading earlier lines.
type checkpoint struct {
	Version         int               `json:"version,omitempty"`
	Meta            model.SessionMeta `json:"meta"`
	LastAssistantID string            `json:"lastAssistantId,omitempty"`
	AssistantIDs    map[string]bool   `json:"assistantIds,omitempty"`
	CallIDs         map[string]bool   `json:"callIds,omitempty"`
	FirstUserSeen   bool              `json:"firstUserSeen,omitempty"`
	LastUserPrompt  string            `json:"lastUserPrompt,omitempty"`
	MetaCWD         bool              `json:"metaCwd,omitempty"`
	FirstLineTime   time.Time         `json:"firstLineTime,omitempty"`
	LinesRead       int               `json:"linesRead,omitempty"`
	// Canonical records the last emitted metadata (including index fields).
	// Meta remains the raw rollout aggregate for exact append resume.
	Canonical *model.SessionMeta `json:"canonical,omitempty"`
	path      string
	lineNo    int
}

const checkpointVersion = 2

// Rollout items known from Codex CLI source that are not conversational messages.
// These are skipped safely without generating an unknown-type diagnostic.
var ignoredRolloutTypes = map[string]bool{
	"compacted":                          true,
	"inter_agent_communication":          true,
	"inter_agent_communication_metadata": true,
	"token_usage_record":                 true,
	"world_state":                        true,
	"retained_context":                   true,
	"security_risk_score":                true,
	"realtime_item":                      true,
}

func (c *checkpoint) consume(line []byte, d *provider.Diagnostics) {
	if !gjson.ValidBytes(line) {
		d.ParseErrors++
		d.Warn(c.path, c.lineNo, "invalid JSON record")
		return
	}

	stampStr := gjson.GetBytes(line, "timestamp").String()
	if stampStr != "" {
		if ts, err := time.Parse(time.RFC3339Nano, stampStr); err == nil {
			ts = ts.UTC()
			if c.FirstLineTime.IsZero() {
				c.FirstLineTime = ts
			}
			if ts.After(c.Meta.UpdatedAt) {
				c.Meta.UpdatedAt = ts
			}
		} else {
			d.Warn(c.path, c.lineNo, "invalid timestamp %q", stampStr)
		}
	}

	typeResult := gjson.GetBytes(line, "type")
	if !typeResult.Exists() {
		// Historic envelope-less format check.
		c.consumeEnvelopeLess(line, d)
		return
	}

	kind := typeResult.String()
	payload := gjson.GetBytes(line, "payload")
	if !payload.Exists() {
		d.ParseErrors++
		d.Warn(c.path, c.lineNo, "missing rollout payload for %q", kind)
		return
	}

	switch kind {
	case "session_meta":
		c.consumeSessionMeta(payload, stampStr, d)
	case "turn_context":
		if modelName := payload.Get("model").String(); modelName != "" {
			c.Meta.Model = modelName
		}
		if !c.MetaCWD && c.Meta.CWD == "" {
			if cwd := payload.Get("cwd").String(); cwd != "" {
				c.Meta.CWD = pathutil.NormalizeDir(cwd)
			}
		}
	case "response_item":
		c.consumeResponseItem(payload, d)
	case "event_msg":
		c.consumeEventMsg(payload, d)
	default:
		if !ignoredRolloutTypes[kind] {
			d.Unknown(kind)
		}
	}
}

func (c *checkpoint) consumeEnvelopeLess(line []byte, d *provider.Diagnostics) {
	roleResult := gjson.GetBytes(line, "role")
	contentResult := gjson.GetBytes(line, "content")
	if roleResult.Exists() && contentResult.Exists() {
		c.handleMessage(roleResult.String(), contentResult, gjson.GetBytes(line, "id").String(), d)
		return
	}

	idResult := gjson.GetBytes(line, "id")
	cwdResult := gjson.GetBytes(line, "cwd")
	if idResult.Exists() && cwdResult.Exists() {
		if id := idResult.String(); id != "" {
			c.Meta.Ref.ID = id
		}
		if cwd := cwdResult.String(); cwd != "" {
			c.Meta.CWD = pathutil.NormalizeDir(cwd)
			c.MetaCWD = true
		}
		return
	}

	d.Warn(c.path, c.lineNo, "unrecognized envelope-less rollout record")
}

func (c *checkpoint) consumeSessionMeta(payload gjson.Result, lineStamp string, d *provider.Diagnostics) {
	if id := payload.Get("id").String(); id != "" {
		if c.Meta.Ref.ID != "" && c.Meta.Ref.ID != id {
			d.Warn(c.path, c.lineNo, "rollout session ID %q differs from earlier metadata %q", id, c.Meta.Ref.ID)
		} else {
			c.Meta.Ref.ID = id
		}
	}
	if cwd := payload.Get("cwd").String(); cwd != "" {
		c.Meta.CWD = pathutil.NormalizeDir(cwd)
		c.MetaCWD = true
	}
	if ver := payload.Get("cli_version").String(); ver != "" {
		c.Meta.AgentVersion = ver
	}
	if branch := payload.Get("git.branch").String(); branch != "" && branch != "HEAD" {
		c.Meta.GitBranch = branch
	}
	// Extract subagent parent ID only when confirmed by source structure.
	if parentID := payload.Get("source.SubAgent.parent_thread_id").String(); parentID != "" {
		c.Meta.ParentID = parentID
	} else if parentID := payload.Get("source.subagent.parent_thread_id").String(); parentID != "" {
		c.Meta.ParentID = parentID
	}

	createdStr := payload.Get("timestamp").String()
	if createdStr == "" {
		createdStr = lineStamp
	}
	if createdStr != "" {
		if ts, err := time.Parse(time.RFC3339Nano, createdStr); err == nil {
			c.Meta.CreatedAt = ts.UTC()
		}
	}
}

func (c *checkpoint) consumeResponseItem(payload gjson.Result, d *provider.Diagnostics) {
	pType := payload.Get("type").String()
	switch pType {
	case "message":
		role := payload.Get("role").String()
		if role == "" {
			d.ParseErrors++
			d.Warn(c.path, c.lineNo, "response message lacks role")
			return
		}
		content := payload.Get("content")
		id := payload.Get("id").String()
		if id == "" {
			id = payload.Get("message_id").String()
		}
		c.handleMessage(role, content, id, d)
	case "function_call", "custom_tool_call", "local_shell_call", "web_search_call":
		callID := payload.Get("call_id").String()
		if callID == "" {
			callID = payload.Get("id").String()
		}
		if callID != "" {
			if c.CallIDs == nil {
				c.CallIDs = make(map[string]bool)
			}
			if !c.CallIDs[callID] {
				c.CallIDs[callID] = true
				c.Meta.Counts.ToolCalls++
			}
		} else {
			c.Meta.Counts.ToolCalls++
		}
	case "function_call_output", "custom_tool_call_output", "local_shell_call_output", "web_search_call_output", "reasoning":
		// Results and reasoning do not count as conversational messages or tool calls.
	default:
		d.Unknown("response_item:" + pType)
	}
}

func (c *checkpoint) handleMessage(role string, content gjson.Result, id string, _ *provider.Diagnostics) {
	switch role {
	case "user":
		prompt, counts := isConversationalUser(content)
		if counts {
			c.Meta.Counts.User++
			if !c.FirstUserSeen {
				c.FirstUserSeen = true
				c.Meta.FirstPrompt = model.TruncateRunes(model.OneLine(prompt), 300)
			}
			if prompt = model.TruncateRunes(model.OneLine(prompt), 300); prompt != "" {
				c.LastUserPrompt = prompt
			}
		}
	case "assistant":
		if id != "" {
			if c.AssistantIDs == nil {
				c.AssistantIDs = make(map[string]bool)
			}
			if !c.AssistantIDs[id] {
				c.AssistantIDs[id] = true
				c.LastAssistantID = id
				c.Meta.Counts.Assistant++
			}
		} else {
			c.Meta.Counts.Assistant++
		}
	case "developer", "system":
		// Context, not a conversational user/assistant turn.
	}
}

func isConversationalUser(content gjson.Result) (string, bool) {
	if content.Type == gjson.String {
		text := content.String()
		if !isInjectedContext(text) && strings.TrimSpace(text) != "" {
			return text, true
		}
		return "", false
	}
	if !content.IsArray() {
		return "", false
	}
	var prompt string
	counts := false
	content.ForEach(func(_, part gjson.Result) bool {
		switch part.Get("type").String() {
		case "input_image", "image":
			counts = true
		case "input_text", "text", "":
			text := part.Get("text").String()
			if text == "" && part.Type == gjson.String {
				text = part.String()
			}
			if !isInjectedContext(text) && strings.TrimSpace(text) != "" {
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

func isInjectedContext(text string) bool {
	trimmed := strings.TrimSpace(text)
	for _, prefix := range []string{
		"<environment_context>",
		"<user_instructions>",
		"# AGENTS.md instructions for",
	} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func (c *checkpoint) consumeEventMsg(payload gjson.Result, _ *provider.Diagnostics) {
	pType := payload.Get("type").String()
	if pType == "token_count" {
		usage := payload.Get("info.total_token_usage")
		if !usage.Exists() {
			usage = payload.Get("total_token_usage")
		}
		if usage.Exists() {
			c.Meta.Tokens = model.TokenUsage{
				Input:      usage.Get("input_tokens").Int(),
				Output:     usage.Get("output_tokens").Int(),
				Reasoning:  firstNonZero(usage.Get("reasoning_output_tokens").Int(), usage.Get("reasoning_tokens").Int()),
				CacheRead:  firstNonZero(usage.Get("cached_input_tokens").Int(), usage.Get("cache_read_input_tokens").Int()),
				CacheWrite: firstNonZero(usage.Get("cache_creation_input_tokens").Int(), usage.Get("cache_write_input_tokens").Int(), usage.Get("cache_write_tokens").Int()),
			}
		}
	}
	// user_message and agent_message are duplicate UI events and must not be counted.
}

func firstNonZero(vals ...int64) int64 {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}

func (c *checkpoint) finalize(path string, modTime time.Time) model.SessionMeta {
	meta := c.Meta
	meta.Ref.Agent = model.AgentCodex
	if meta.Ref.ID == "" {
		meta.Ref.ID = idFromRolloutPath(path)
	}
	meta.SourcePath = path
	meta.Archived = isArchivedPath(path)
	if meta.CreatedAt.IsZero() {
		if !c.FirstLineTime.IsZero() {
			meta.CreatedAt = c.FirstLineTime
		} else {
			meta.CreatedAt = modTime.UTC()
		}
	}
	if meta.UpdatedAt.IsZero() {
		meta.UpdatedAt = modTime.UTC()
	}
	if meta.Title == "" {
		if c.LastUserPrompt != "" {
			meta.Title = c.LastUserPrompt
		} else if meta.FirstPrompt != "" { // Checkpoints written before LastUserPrompt existed.
			meta.Title = meta.FirstPrompt
		} else {
			meta.Title = "(untitled)"
		}
	}
	return meta
}

func idFromRolloutPath(path string) string {
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, ".jsonl")
	name = strings.TrimPrefix(name, "rollout-")
	if len(name) > 20 && name[10] == 'T' && name[19] == '-' {
		return name[20:]
	}
	return name
}

func isArchivedPath(path string) bool {
	clean := filepath.Clean(path)
	parts := strings.Split(clean, string(filepath.Separator))
	for _, part := range parts {
		if part == "archived_sessions" {
			return true
		}
	}
	return false
}

type sourceScan struct {
	path    string
	state   provider.SourceState
	meta    model.SessionMeta
	diag    provider.Diagnostics
	changed bool
	err     error
}

// Scan parses rollout metadata from discovered JSONL files.
func (p *Provider) Scan(ctx context.Context, prev provider.ScanState) (provider.ScanResult, error) {
	result := provider.ScanResult{
		State: provider.ScanState{Sources: make(map[string]provider.SourceState)},
	}
	paths, err := p.discoverWithDiagnostics(ctx, &result.Diag)
	if err != nil {
		return provider.ScanResult{}, err
	}
	index := p.readIndex(ctx, &result.Diag)
	if err := ctx.Err(); err != nil {
		return provider.ScanResult{}, err
	}

	// Valid index paths not found by conventional directory discovery are known
	// explicit sources. Never pass a DB path to scanSource until it has passed
	// containment, symlink, regular-file and readability checks.
	known := make(map[string]bool, len(paths))
	for _, path := range paths {
		known[path] = true
	}
	indexPaths := make(map[string]string, len(index.threads)) // ID -> validated path
	indexIDs := make([]string, 0, len(index.threads))
	for id := range index.threads {
		indexIDs = append(indexIDs, id)
	}
	sort.Strings(indexIDs)
	for _, id := range indexIDs {
		row := index.threads[id]
		path, err := p.indexRolloutPath(row.path)
		if err != nil {
			result.Diag.Warn(p.root, 0, "threads row %q: %v; using discovered rollouts", id, err)
			delete(index.threads, id)
			continue
		}
		if path != "" {
			indexPaths[id] = path
			if !known[path] {
				paths = append(paths, path)
				known[path] = true
			}
		}
	}
	sort.Strings(paths)

	workers := min(8, runtime.GOMAXPROCS(0))
	jobs := make(chan int)
	outcomes := make([]sourceScan, len(paths))
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if ctx.Err() != nil {
					outcomes[idx] = sourceScan{err: ctx.Err()}
					continue
				}
				old, hasOld := prev.Sources[paths[idx]]
				outcomes[idx] = p.scanSource(ctx, paths[idx], old, hasOld)
			}
		}()
	}
feed:
	for i := range paths {
		if err := ctx.Err(); err != nil {
			break
		}
		select {
		case <-ctx.Done():
			break feed
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return provider.ScanResult{}, err
	}

	present := make(map[string]bool, len(paths))
	byID := make(map[string]sourceScan, len(paths))

	for i, path := range paths {
		present[path] = true
		out := outcomes[i]
		if out.err != nil {
			return provider.ScanResult{}, out.err
		}
		result.Diag.Merge(out.diag)
		result.State.Sources[path] = out.state

		id := out.meta.Ref.ID
		if id == "" {
			continue
		}
		existing, ok := byID[id]
		if !ok || preferRollout(out, existing) {
			byID[id] = out
		}
	}

	// Choose the index's existing path for duplicate IDs (active/archive),
	// then merge its metadata into the file aggregate. An index row without a
	// scanned rollout is never a visible session.
	for _, id := range indexIDs {
		indexed, hasIndex := index.threads[id]
		if !hasIndex {
			continue
		}
		src, ok := byID[id]
		if !ok {
			result.Diag.Warn(p.root, 0, "threads row %q has no rollout; ignoring stale index row", id)
			continue
		}
		if wanted := indexPaths[id]; wanted != "" && src.path != wanted {
			for _, candidate := range outcomes {
				if candidate.path == wanted && candidate.meta.Ref.ID == id {
					src = candidate
					break
				}
			}
			if src.path != wanted {
				result.Diag.Warn(wanted, 0, "threads row %q points to a rollout with a different session ID; using discovered rollout", id)
				delete(index.threads, id)
				continue
			}
		}
		src.meta = p.mergeIndexedMeta(src.meta, indexed.meta)
		if indexed.archivedPresent {
			src.meta.Archived = indexed.meta.Archived
		} else {
			src.meta.Archived = isArchivedPath(src.path)
		}
		byID[id] = src
	}
	if index.hasEdges {
		for id, src := range byID {
			// The edges table is authoritative, including removal of an old edge.
			src.meta.ParentID = ""
			if parent := index.edges[id]; parent != "" && parent != id {
				if _, validParent := byID[parent]; validParent {
					src.meta.ParentID = parent
				}
			}
			byID[id] = src
		}
	}

	// Persist the last emitted metadata for every duplicate source. Rollout
	// Meta in the checkpoint remains raw, so append resume never parses from
	// index values and a later absent index can fall back accurately.
	for path, state := range result.State.Sources {
		var cp checkpoint
		if err := json.Unmarshal(state.Checkpoint, &cp); err != nil {
			return provider.ScanResult{}, fmt.Errorf("codex: decode rollout checkpoint %q: %w", path, err)
		}
		if canonical, ok := byID[cp.finalize(path, time.Unix(0, state.ModTimeNs)).Ref.ID]; ok {
			meta := canonical.meta
			cp.Canonical = &meta
		}
		state.Checkpoint, err = json.Marshal(cp)
		if err != nil {
			return provider.ScanResult{}, fmt.Errorf("codex: encode rollout checkpoint %q: %w", path, err)
		}
		result.State.Sources[path] = state
	}

	previous := make(map[string]sourceScan, len(prev.Sources))
	for path, old := range prev.Sources {
		if !present[path] {
			// Discovery can skip an unreadable file (or a new symlink escape).
			// Only a genuinely missing path proves deletion; otherwise retain
			// prior catalog/state instead of reporting false removals.
			if _, err := os.Lstat(path); err == nil || !os.IsNotExist(err) {
				return provider.ScanResult{}, fmt.Errorf("codex: previously scanned rollout %q is no longer discoverable: %w", path, err)
			}
		}
		var cp checkpoint
		if err := json.Unmarshal(old.Checkpoint, &cp); err != nil {
			result.Diag.Warn(path, 0, "invalid previous scan checkpoint: %v", err)
			continue
		}
		meta := cp.finalize(path, time.Unix(0, old.ModTimeNs))
		if cp.Canonical != nil {
			meta = *cp.Canonical
		}
		if meta.CWD != "" {
			meta.CWDMissing = !pathutil.Exists(meta.CWD)
			if p.git != nil {
				if repo, ok := p.git.Resolve(meta.CWD); ok {
					meta.RepoRoot = repo.MainRoot
				}
			}
		}
		candidate := sourceScan{path: path, meta: meta}
		if existing, ok := previous[meta.Ref.ID]; !ok || preferRollout(candidate, existing) {
			previous[meta.Ref.ID] = candidate
		}
	}

	// Compare canonical sessions, not file events: an archived copy may become
	// canonical when an unchanged active copy is deleted, or vice versa.
	for id, src := range byID {
		old, hadOld := previous[id]
		if !hadOld || src.meta != old.meta {
			result.Changed = append(result.Changed, src.meta)
		}
	}
	for id, src := range previous {
		if _, stillPresent := byID[id]; !stillPresent {
			result.Removed = append(result.Removed, src.meta.Ref)
		}
	}

	sort.Slice(result.Changed, func(i, j int) bool { return result.Changed[i].Ref.Key() < result.Changed[j].Ref.Key() })
	sort.Slice(result.Removed, func(i, j int) bool { return result.Removed[i].Key() < result.Removed[j].Key() })
	return result, nil
}

// preferRollout prefers the newer rollout for duplicate IDs; on tie, prefers active over archived.
func preferRollout(candidate, existing sourceScan) bool {
	if candidate.meta.UpdatedAt.After(existing.meta.UpdatedAt) {
		return true
	}
	if candidate.meta.UpdatedAt.Equal(existing.meta.UpdatedAt) {
		if existing.meta.Archived && !candidate.meta.Archived {
			return true
		}
		if existing.meta.Archived == candidate.meta.Archived && candidate.path < existing.path {
			return true
		}
	}
	return false
}

func (p *Provider) scanSource(ctx context.Context, path string, old provider.SourceState, hasOld bool) sourceScan {
	var out sourceScan
	out.path = path
	if err := ctx.Err(); err != nil {
		out.err = err
		return out
	}

	info, err := os.Stat(path)
	if err != nil {
		out.err = fmt.Errorf("codex: stat rollout %q: %w", path, err)
		return out
	}
	if !info.Mode().IsRegular() {
		out.err = fmt.Errorf("codex: rollout %q is not a regular file", path)
		return out
	}

	var cp checkpoint
	resume := hasOld && len(old.Checkpoint) > 0 && old.Offset >= 0 && old.Offset <= old.Size && old.Size <= info.Size()
	if resume {
		if err := json.Unmarshal(old.Checkpoint, &cp); err != nil {
			out.diag.Warn(path, 0, "invalid scan checkpoint: %v", err)
			resume = false
		} else if cp.Version != checkpointVersion {
			resume = false
		}
	}

	// Skip identical (size, mtime) files.
	if resume && hasOld && old.Size == info.Size() && old.ModTimeNs == info.ModTime().UnixNano() {
		out.state = old
		out.meta = cp.finalize(path, info.ModTime())
		if out.meta.CWD != "" {
			out.meta.CWDMissing = !pathutil.Exists(out.meta.CWD)
			if p.git != nil {
				if repo, ok := p.git.Resolve(out.meta.CWD); ok {
					out.meta.RepoRoot = repo.MainRoot
				}
			}
		}
		out.changed = false
		return out
	}

	// Rescan after shrink or mtime change without append.
	if (resume && old.Size == info.Size()) || (resume && info.Size() < old.Size) {
		resume = false
	}

	if !resume {
		cp = checkpoint{}
	}
	cp.path = path

	start := int64(0)
	if resume {
		start = old.Offset
	}

	r, err := jsonl.Open(path, start)
	if err != nil {
		out.err = fmt.Errorf("codex: open rollout %q: %w", path, err)
		return out
	}
	defer func() { _ = r.Close() }()

	lineBase := cp.LinesRead
	lineCount := 0
	for {
		if lineCount%1000 == 0 {
			if err := ctx.Err(); err != nil {
				out.err = err
				return out
			}
		}
		lineCount++

		line, _, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		cp.lineNo = lineBase + r.LineNo()
		if errors.Is(err, jsonl.ErrLineTooLong) {
			out.diag.ParseErrors++
			out.diag.Warn(path, cp.lineNo, "JSONL record exceeds maximum line length")
			continue
		}
		if err != nil {
			out.err = fmt.Errorf("read rollout %s: %w", path, err)
			return out
		}
		cp.consume(line, &out.diag)
	}

	if err := ctx.Err(); err != nil {
		out.err = err
		return out
	}

	cp.LinesRead = lineBase + r.LineNo()
	cp.Version = checkpointVersion
	out.meta = cp.finalize(path, info.ModTime())
	if out.meta.CWD != "" {
		out.meta.CWDMissing = !pathutil.Exists(out.meta.CWD)
		if p.git != nil {
			if repo, ok := p.git.Resolve(out.meta.CWD); ok {
				out.meta.RepoRoot = repo.MainRoot
			}
		}
	}

	encoded, err := json.Marshal(cp)
	if err != nil {
		out.err = fmt.Errorf("marshal checkpoint %s: %w", path, err)
		return out
	}

	out.changed = true
	out.state = provider.SourceState{
		Size:       info.Size(),
		ModTimeNs:  info.ModTime().UnixNano(),
		Offset:     r.Offset(),
		Checkpoint: encoded,
	}
	return out
}
