package app

import (
	"context"
	"errors"

	"github.com/mestre-ia/mestre/internal/blocking"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// ApprovalPanelSource is what an IntegrationRun may also be: the flow of approval
// and the feed of notices that the central and the bell of the panel read.
type ApprovalPanelSource interface {
	PanelApprovals() (*panel.ApprovalSource, *panel.NoticeSource)
}

// panelReach counts the users the panel shows a request to, from what asked them
// (the flow of approval); nil when the asker is not the flow.
func panelReach(asker chain.UserAsker) func(ctx context.Context, request int64) int {
	f, ok := asker.(interface {
		Eligible(ctx context.Context, request int64) ([]users.User, error)
	})
	if !ok {
		return nil
	}
	return func(ctx context.Context, request int64) int {
		to, err := f.Eligible(ctx, request)
		if err != nil {
			return 0
		}
		return len(to)
	}
}

// flowChanged is what the flow tells when a request changes: the panel of the
// period, if there is one, updates the screens.
func (r *stdRun) flowChanged(ctx context.Context, request int64) {
	if fn := r.onFlow.Load(); fn != nil {
		(*fn)(ctx, request)
	}
}

// PanelApprovals implements ApprovalPanelSource.
func (r *stdRun) PanelApprovals() (*panel.ApprovalSource, *panel.NoticeSource) {
	src := &panel.ApprovalSource{
		Flow:     r.flow,
		Blocking: &blocking.Store{DB: r.env.DB, Clock: r.env.Clock},
		Listen: func(fn func(ctx context.Context, request int64)) func() {
			r.onFlow.Store(&fn)
			return func() { r.onFlow.CompareAndSwap(&fn, nil) }
		},
		Mask: r.env.Masker.Mask,
		Diff: r.requestDiff,
	}
	return src, &panel.NoticeSource{Feed: r.feed}
}

var errNoDiff = errors.New("app: the request is not about a PR")

// requestDiff is the whole diff of the PR a request is about, masked.
func (r *stdRun) requestDiff(ctx context.Context, req chain.Request) ([]panel.ApprovalDiffFile, error) {
	if req.PR == nil {
		return nil, errNoDiff
	}
	files, err := r.source().Diff(ctx, req.Workspace, *req.PR)
	if err != nil {
		return nil, err
	}
	out := make([]panel.ApprovalDiffFile, 0, len(files))
	for _, f := range files {
		out = append(out, panel.ApprovalDiffFile{
			Path: f.Path, PreviousPath: f.PreviousPath, Status: f.Status, Added: f.Additions, Removed: f.Deletions,
			Patch: r.env.Masker.Mask(f.Patch), PatchMissing: f.Patch == "" && f.Additions+f.Deletions > 0 && f.Status != "removed",
		})
	}
	return out, nil
}
