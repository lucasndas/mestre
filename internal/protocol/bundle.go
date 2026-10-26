package protocol

import (
	"context"
	"errors"
	"fmt"

	"github.com/mestre-ia/mestre/internal/gitx"
)

// BundleRepo is the repository that receives a bundle; gitx.Repo satisfies
// it.
type BundleRepo interface {
	VerifyBundle(ctx context.Context, file string) error
}

// BundleReceiver is the Finalizer of the transfers of kind git-bundle: it
// runs `git bundle verify` in the repository of the destination. A missing
// prerequisite commit is `missing_prerequisite`, which makes the sender go
// again with the whole branch; a bundle that git does not accept is
// `hash_mismatch`. How the verified bundle is applied (advance, conflict) is
// the business of the tables (docs/orquestracao/mesas.md): Apply.
type BundleReceiver struct {
	// Repo opens the repository that meta names (the table of the transfer).
	Repo func(ctx context.Context, meta Object) (BundleRepo, error)
	// Apply, when set, applies the bundle after it was verified.
	Apply func(ctx context.Context, repo BundleRepo, meta Object, file string) error
}

// Finalize implements Finalizer.
func (r BundleReceiver) Finalize(ctx context.Context, b XferBegin, file string) error {
	repo, err := r.Repo(ctx, b.Meta)
	if err != nil {
		return asError(err)
	}
	if err := repo.VerifyBundle(ctx, file); err != nil {
		switch {
		case errors.Is(err, gitx.ErrMissingPrerequisite):
			return NewError(CodeMissingPrerequisite, "the repository lacks the prerequisite commits of the bundle")
		case errors.Is(err, gitx.ErrInvalidBundle):
			return NewError(CodeHashMismatch, "git bundle verify: %v", err)
		}
		return NewError(CodeInternal, "git bundle verify: %v", err)
	}
	if r.Apply != nil {
		return r.Apply(ctx, repo, b.Meta, file)
	}
	return nil
}

// OpenSource opens the content of a transfer; the func it returns releases
// it.
type OpenSource func(ctx context.Context) (Source, func(), error)

// BundleSource says how to build the bundles of a branch: the incremental one,
// from the last commit that both sides have, and the whole branch.
type BundleSource struct {
	// Incremental may be nil when there is no common commit to start from.
	Incremental OpenSource
	Full        OpenSource
}

// SendBundle sends the incremental bundle; if the receiver answers
// `missing_prerequisite`, it sends the whole branch, as another transfer.
// meta is what the receiver needs to find the repository (docs/conectividade/protocolo.md#transferência-em-partes).
func (l *Link) SendBundle(ctx context.Context, meta Object, src BundleSource) error {
	send := func(open OpenSource) error {
		s, release, err := open(ctx)
		if err != nil {
			return fmt.Errorf("protocol: building the bundle: %w", err)
		}
		defer release()
		return l.Transfer(ctx, Transfer{Kind: KindGitBundle, Meta: meta, Source: s})
	}
	if src.Incremental != nil {
		err := send(src.Incremental)
		var pe *Error
		if !errors.As(err, &pe) || pe.Code != CodeMissingPrerequisite {
			return err
		}
	}
	return send(src.Full)
}
