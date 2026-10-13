package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/manage"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
)

// BundleRestoreRequest imports an opened bundle's native session records into
// the source agent's store, under a directory the user chose on this machine.
type BundleRestoreRequest struct {
	BundleID string `json:"bundleId"`
	CWD      string `json:"cwd"`
	// Copy restores under a new session id when the original id already
	// exists. CopyID carries the id the preview minted for it.
	Copy   bool   `json:"copy,omitempty"`
	CopyID string `json:"copyId,omitempty"`
}

// RestorePreview mirrors manage.RestorePreview for the frontend.
type RestorePreview = manage.RestorePreview

// RestoreReport mirrors manage.RestoreReport for the frontend.
type RestoreReport = manage.RestoreReport

// PreviewBundleRestore plans importing an opened bundle into req.CWD.
func (e *Engine) PreviewBundleRestore(ctx context.Context, req BundleRestoreRequest) (RestorePreview, error) {
	mreq, mgr, err := e.restoreRequest(req)
	if err != nil {
		return RestorePreview{}, err
	}
	p, err := mgr.PreviewRestore(ctx, mreq, e.svc.Catalog().All())
	if err != nil {
		return RestorePreview{}, err
	}
	return *p, nil
}

// RestoreBundle executes a previewed import.
func (e *Engine) RestoreBundle(ctx context.Context, req BundleRestoreRequest, token string) (RestoreReport, error) {
	if strings.TrimSpace(token) == "" {
		return RestoreReport{}, ErrPreviewStale
	}
	mreq, mgr, err := e.restoreRequest(req)
	if err != nil {
		return RestoreReport{}, err
	}
	rep, err := mgr.Restore(ctx, mreq, e.svc.Catalog().All(), token)
	if err != nil {
		return RestoreReport{}, err
	}
	return *rep, nil
}

func (e *Engine) restoreRequest(req BundleRestoreRequest) (manage.RestoreRequest, *manage.Manager, error) {
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		return manage.RestoreRequest{}, nil, errors.New("choose a directory to import the session into")
	}
	if !pathutil.IsAbs(cwd) {
		return manage.RestoreRequest{}, nil, errors.New("the import directory must be an absolute path")
	}
	if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
		return manage.RestoreRequest{}, nil, fmt.Errorf("directory %s does not exist", cwd)
	}
	// Record the path the scanner will report, so the imported session lands
	// in the same directory group as the sessions already there.
	cwd = pathutil.NormalizeDir(cwd)
	if e.svc == nil || e.svc.Catalog() == nil {
		return manage.RestoreRequest{}, nil, errors.New("manage: catalog not initialized")
	}
	b, err := e.svc.GetBundle(req.BundleID)
	if err != nil {
		return manage.RestoreRequest{}, nil, err
	}
	mgr, err := e.ensureManage()
	if err != nil {
		return manage.RestoreRequest{}, nil, err
	}
	collision := manage.CollisionBlock
	if req.Copy {
		collision = manage.CollisionCopy
	}
	return manage.RestoreRequest{
		Bundle:      b,
		TargetAgent: b.Manifest.Source.Agent,
		TargetCWD:   cwd,
		Collision:   collision,
		CopyID:      req.CopyID,
	}, mgr, nil
}
