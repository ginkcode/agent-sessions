package provider

import (
	"context"
	"errors"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// ErrStaleSource means the hint could not be validated, not that the session is
// absent. Optional source methods may return it (possibly wrapped) without data
// or resolve canonically themselves. Other errors must not trigger rediscovery.
var ErrStaleSource = errors.New("provider: stale or unvalidated source hint")

// LoadWithSource uses optional source loading, with at most one standalone Load
// fallback on ErrStaleSource. The base Provider and remote protocols are unchanged.
//
// source must be an unmodified canonical Scan/catalog snapshot for ref, not
// metadata reconstructed from a filename or supplied by a public caller. Even so,
// it is only a hint: implementations must check provider/ref identity, configured
// root containment, symlinks, file identity and current canonical selection before
// using its path or overlays. Matching paths, size/mtime, or UpdatedAt alone do not
// prove freshness. Missing, replaced or ambiguous dependencies require canonical
// resolution or ErrStaleSource; a hint never authorizes a noncanonical duplicate.
// Returned transcript metadata must be canonical, not blindly copied from source.
// Validation and I/O honor ctx, do not mutate source, and preserve the standalone
// read's best-effort snapshot semantics (not an atomic filesystem snapshot).
//
// The engine owns catalog metadata and cache invalidation tokens. FTS instead
// owns metadata read consistently with its queued DB revision; it must not borrow
// the engine's catalog/cache, relabel a newer row with an older job revision, or
// replace DB metadata with Load.Meta. Transactional current/queued revision checks
// before publishing FTS documents remain mandatory, including after fallback.
func LoadWithSource(ctx context.Context, p Provider, ref model.SessionRef, source model.SessionMeta) (*model.Transcript, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if loader, ok := p.(SourceLoader); ok && source.Ref == ref && ref.Agent == p.ID() && ref.ID != "" {
		tr, err := loader.LoadFromSource(ctx, ref, source)
		if !errors.Is(err, ErrStaleSource) {
			return tr, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p.Load(ctx, ref)
}

// BlobWithSource follows LoadWithSource's validation and bounded fallback rules.
// A blob key remains provider-defined and must be validated independently. Source
// hints do not pin old blob offsets: canonical fallback may reject a key after a
// source change, just as standalone Blob does; no cross-revision byte guarantee is
// implied. It never fetches arbitrary paths/URLs on the strength of a hint.
func BlobWithSource(ctx context.Context, p Provider, ref model.SessionRef, source model.SessionMeta, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if loader, ok := p.(SourceBlobLoader); ok && source.Ref == ref && ref.Agent == p.ID() && ref.ID != "" {
		data, err := loader.BlobFromSource(ctx, ref, source, key)
		if !errors.Is(err, ErrStaleSource) {
			return data, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p.Blob(ctx, ref, key)
}
