package codediagnostics

// DocumentIdentity reserves the v1 publication guard for Stage 2. It owns no
// document text, session, watcher or execution; Stage 1 never accepts LSP sets.
type DocumentIdentity struct {
	Context    Context
	Path       string
	DocumentID string
	Version    int64
}

// MatchesPublication is deliberately strict: an unversioned result cannot
// establish verified draft freshness, nor can close/reopen reuse a version.
func (d DocumentIdentity) MatchesPublication(c Context, path string, basis SavedBasis) bool {
	return d.Context == c && bounded(d.Context.ConnectionID, 256) && bounded(d.Context.AttachmentID, 256) && bounded(d.Context.Generation, 256) && ValidatePath(d.Path) == nil && path == d.Path && bounded(d.DocumentID, 256) && d.Version >= 1 && d.Version <= maxSequence && basis.Kind == "document" && basis.DocumentID == d.DocumentID && basis.Version != nil && *basis.Version == d.Version
}
