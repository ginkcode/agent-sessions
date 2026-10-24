package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

const (
	maxProofSources       = 4096
	maxProofBytes   int64 = 512 << 20
)

type sourceEvidence struct {
	info os.FileInfo
	sum  [sha256.Size]byte
}
type sourceProof struct {
	indexPath string
	index     threadIndex
	files     map[string]sourceEvidence
	validated map[string]string // ID -> accepted path; missing means invalid row
}
type sourceSnapshot struct {
	proof     *sourceProof
	canonical map[model.SessionRef]model.SessionMeta
}

// sourceProof reads bytes, not rollout records. A digest of ALL selectable
// sources excludes same-stat rewrites and competing duplicates. SQLite is read
// afresh, including WAL; database mtimes are never revision evidence. Discovery
// and index path validation are repeated even on hits. This is deliberately a
// conservative full-byte reconciliation, not an incremental scan/checkpoint cache.
func (p *Provider) proveSources(ctx context.Context) *sourceProof {
	work := sourceWork(ctx)
	if work != nil {
		work.Passes.Add(1)
		work.Enumerations.Add(1)
	}
	paths, err := p.discover(ctx)
	if err != nil || len(paths) > maxProofSources {
		return nil
	}
	indexPath, err := p.indexDBPath()
	if err != nil {
		return nil
	}
	index := threadIndex{threads: make(map[string]indexedThread)}
	if indexPath != "" {
		if work != nil {
			work.IndexReads.Add(1)
		}
		index, err = p.loadIndex(ctx, indexPath)
		if err != nil {
			return nil
		} // advisory fallback is valid but not proof
	}
	if len(index.threads) > maxProofSources || len(index.edges) > maxProofSources {
		return nil
	}
	known := make(map[string]bool, len(paths))
	for _, path := range paths {
		known[path] = true
	}
	validated := make(map[string]string, len(index.threads))
	for id, row := range index.threads {
		path, err := p.indexRolloutPath(row.path)
		if err == nil {
			validated[id] = path
		}
		if err == nil && path != "" && !known[path] {
			paths = append(paths, path)
			known[path] = true
		}
	}
	if len(paths) > maxProofSources {
		return nil
	}
	sort.Strings(paths)
	proof := &sourceProof{indexPath: indexPath, index: index, validated: validated, files: make(map[string]sourceEvidence, len(paths))}
	// Cap retained metadata as well as file count; index strings are untrusted.
	retained := 0
	for id, row := range index.threads {
		encoded, err := json.Marshal(row.meta)
		if err != nil {
			return nil
		}
		retained += len(id) + len(row.path) + len(encoded)
		if retained > 8<<20 {
			return nil
		}
	}
	for child, parent := range index.edges {
		retained += len(child) + len(parent)
		if retained > 8<<20 {
			return nil
		}
	}
	var total int64
	for _, path := range paths {
		if ctx.Err() != nil {
			return nil
		}
		if work != nil {
			work.Opens.Add(1)
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxProofBytes-total {
			_ = f.Close()
			return nil
		}
		h := sha256.New()
		// Bounded reads also protect against a concurrently growing file. Check
		// cancellation between chunks rather than waiting for a complete large file.
		buf := make([]byte, 64<<10)
		var n int64
		for {
			if ctx.Err() != nil {
				err = ctx.Err()
				break
			}
			var count int
			count, err = f.Read(buf)
			if work != nil {
				work.Bytes.Add(int64(count))
			}
			n += int64(count)
			if n > info.Size() {
				break
			}
			_, _ = h.Write(buf[:count])
			if err != nil {
				break
			}
		}
		after, statErr := f.Stat()
		closeErr := f.Close()
		if !errors.Is(err, io.EOF) || statErr != nil || closeErr != nil || n != info.Size() || !sameSourceInfo(info, after) {
			return nil
		}
		var sum [sha256.Size]byte
		copy(sum[:], h.Sum(nil))
		proof.files[path] = sourceEvidence{info: info, sum: sum}
		total += n
	}
	return proof
}

func sameSourceInfo(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime())
}

func sameProof(a, b *sourceProof) bool {
	if a == nil || b == nil || a.indexPath != b.indexPath || !reflect.DeepEqual(a.index, b.index) || !reflect.DeepEqual(a.validated, b.validated) || len(a.files) != len(b.files) {
		return false
	}
	for path, old := range a.files {
		cur, ok := b.files[path]
		if !ok || old.sum != cur.sum || !sameSourceInfo(old.info, cur.info) {
			return false
		}
	}
	return true
}

// establishSource resolves once on an optional cold call. Ordinary Scan/Load
// never hash sources. Even when proof fails, this canonical result serves the
// current request without causing a second standalone scan.
func (p *Provider) establishSource(ctx context.Context, ref model.SessionRef) (model.SessionMeta, error) {
	p.sourceMu.Lock()
	p.sourceGeneration++
	generation := p.sourceGeneration
	p.sourceMu.Unlock()
	before := p.proveSources(ctx)
	result, err := p.Scan(ctx, provider.ScanState{})
	if err != nil {
		return model.SessionMeta{}, err
	}
	canonical := make(map[model.SessionRef]model.SessionMeta, len(result.Changed))
	for _, meta := range result.Changed {
		canonical[meta.Ref] = meta
	}
	if before != nil {
		after := p.proveSources(ctx)
		if sameProof(before, after) {
			snapshot := &sourceSnapshot{proof: after, canonical: canonical}
			p.publishSource(generation, snapshot)
		}
	}
	if err := ctx.Err(); err != nil {
		return model.SessionMeta{}, err
	}
	meta, ok := canonical[ref]
	if !ok {
		return model.SessionMeta{}, fmt.Errorf("codex: rollout for session %q not found", ref.ID)
	}
	if _, err := p.indexRolloutPath(meta.SourcePath); err != nil {
		return model.SessionMeta{}, err
	}
	return meta, nil
}

// Only discard the evidence this caller actually checked. A concurrent cold
// request may already have published a newer snapshot.
func (p *Provider) invalidateSource(snapshot *sourceSnapshot) {
	p.sourceMu.Lock()
	defer p.sourceMu.Unlock()
	if p.sourceSnapshot == snapshot {
		p.sourceSnapshot = nil
		p.sourceGeneration++
	}
}

func (p *Provider) publishSource(generation uint64, snapshot *sourceSnapshot) {
	p.sourceMu.Lock()
	defer p.sourceMu.Unlock()
	if p.sourceGeneration == generation {
		p.sourceSnapshot = snapshot
	}
}

func (p *Provider) sourceMeta(ctx context.Context, ref model.SessionRef, hint model.SessionMeta, allowCold bool) (model.SessionMeta, error) {
	if err := ctx.Err(); err != nil {
		return model.SessionMeta{}, err
	}
	// Full-byte proof is deliberately disabled for production, regardless of
	// root size or prior evidence. Return before ANY discovery/index/hash work;
	// the contract dispatcher performs one ordinary canonical fallback.
	if !p.sourceExperiment || ref.Agent != p.ID() || ref.ID == "" || hint.Ref != ref {
		return model.SessionMeta{}, provider.ErrStaleSource
	}
	p.sourceMu.Lock()
	snapshot := p.sourceSnapshot
	p.sourceMu.Unlock()
	if snapshot == nil {
		if !allowCold {
			return model.SessionMeta{}, provider.ErrStaleSource
		}
		return p.establishSource(ctx, ref)
	}
	if !sameProof(snapshot.proof, p.proveSources(ctx)) {
		if err := ctx.Err(); err != nil {
			return model.SessionMeta{}, err
		}
		p.invalidateSource(snapshot)
		return model.SessionMeta{}, provider.ErrStaleSource
	}
	meta, ok := snapshot.canonical[ref]
	if !ok || meta != hint {
		return model.SessionMeta{}, provider.ErrStaleSource
	}
	if _, err := p.indexRolloutPath(meta.SourcePath); err != nil {
		return model.SessionMeta{}, provider.ErrStaleSource
	}
	// CWD existence and Git resolution are external to the rollout/index proof.
	if meta.CWD != "" {
		meta.CWDMissing = !pathutil.Exists(meta.CWD)
		meta.RepoRoot = ""
		if p.git != nil {
			if repo, ok := p.git.Resolve(meta.CWD); ok {
				meta.RepoRoot = repo.MainRoot
			}
		}
	}
	return meta, nil
}

func (p *Provider) LoadFromSource(ctx context.Context, ref model.SessionRef, hint model.SessionMeta) (*model.Transcript, error) {
	meta, err := p.sourceMeta(ctx, ref, hint, true)
	if err != nil {
		return nil, err
	}
	if work := provider.LoadWorkFromContext(ctx); work != nil {
		work.Calls.Add(1)
	}
	tr, _, err := p.loadRollout(ctx, meta)
	return tr, err
}

func (p *Provider) BlobFromSource(ctx context.Context, ref model.SessionRef, hint model.SessionMeta, key string) ([]byte, error) {
	// Even in the experiment a Blob must not pay two proof passes plus Scan
	// merely to extract one part. Only Load can establish experimental evidence.
	meta, err := p.sourceMeta(ctx, ref, hint, false)
	if err != nil {
		return nil, err
	}
	return p.blob(ctx, ref, key, &meta)
}

var (
	_ provider.SourceLoader     = (*Provider)(nil)
	_ provider.SourceBlobLoader = (*Provider)(nil)
)
