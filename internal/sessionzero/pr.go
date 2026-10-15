package sessionzero

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/safefile"
)

// PRBranch is the branch of the pull request of the session zero.
const PRBranch = "mestre/sessao-zero"

// prWorktree is the folder, beside the mirror, where the commit is made.
const prWorktree = "sessao-zero-pr"

// errNothingToSend is a build whose files the main branch already has.
var errNothingToSend = errors.New("the main branch already has the files")

// maxMestreFile bounds a file of .mestre/ that goes into the pull request.
const maxMestreFile = 1 << 20

// openPR puts what the project must receive into one pull request: the
// AGENTS.md the users approved and/or .mestre/ without the execution folders
// when they chose to version it. The engine builds the branch from the main
// branch of the origin each time, with the Grão-Mestre App as the author, in
// a worktree of the mirror that no agent ever sees, and has the pull request
// sent and opened (PullRequests, milestone 5). Calling it again with more to
// send replaces the branch: it stays a single pull request.
func (s *Service) openPR(ctx context.Context, w workspaces.Workspace) error {
	s.prMu.Lock()
	defer s.prMu.Unlock()
	st, err := s.store.Get(ctx, w.Slug)
	if err != nil {
		return err
	}
	mirror, err := s.o.Workspaces.OpenMirror(ctx, w.Slug)
	if err != nil {
		return err
	}
	base := "refs/remotes/origin/" + w.DefaultBranch
	files := map[string][]byte{}
	if st.AgentsMD == AgentsMDApproved {
		// An AGENTS.md that appeared meanwhile is the project's: never edited.
		if _, err := mirror.ReadFile(ctx, base, "AGENTS.md"); err == nil {
			if nerr := s.o.Users.Notify(ctx, w.Slug, Notice{Kind: "agents_md_suggestions", Text: "O projeto ganhou um AGENTS.md depois da proposta; o Mestre não o altera e a proposta aprovada não foi enviada."}); nerr != nil {
				return nerr
			}
		} else if errors.Is(err, gitx.ErrFileNotFound) {
			files["AGENTS.md"] = []byte(st.AgentsMDText)
		} else {
			return fmt.Errorf("read AGENTS.md at %s: %w", base, err)
		}
	}
	principal := s.o.Workspaces.Layout().Principal(w.Slug)
	if st.Versioning == VersioningVersioned {
		list, err := MestreFiles(principal)
		if err != nil {
			return fmt.Errorf("list .mestre/: %w", err)
		}
		for _, rel := range list {
			data, _, err := safefile.Read(filepath.Join(principal, filepath.FromSlash(rel)), maxMestreFile)
			if err != nil {
				return fmt.Errorf("read %s: %w", rel, err)
			}
			// A secret in a file of .mestre/ never goes into a pull request
			// (the content is not repeated, only the file is named).
			if text := string(data); s.o.Masker.Mask(text) != text {
				return fmt.Errorf("%s looks like it holds a secret; the pull request of the session zero was not sent", rel)
			}
			files[rel] = data
		}
	}
	if len(files) == 0 {
		return nil
	}
	author, err := s.o.PullRequests.Author(ctx)
	if err != nil {
		return fmt.Errorf("author of the commit: %w", err)
	}
	if err := s.buildBranch(ctx, mirror, w, base, files, author); errors.Is(err, errNothingToSend) {
		return nil
	} else if err != nil {
		return err
	}
	url, err := s.o.PullRequests.Publish(ctx, PullRequest{
		Repo: w.Repo, Branch: PRBranch, Base: w.DefaultBranch,
		Title: "Sessão zero do Mestre",
		Body:  prBody(slices.Sorted(maps.Keys(files))),
	})
	if err != nil {
		return fmt.Errorf("open the pull request: %w", err)
	}
	return s.store.PROpened(ctx, w.Slug, s.o.Clock.Now(), url)
}

// buildBranch makes the branch PRBranch in the mirror: from base, one commit
// with the files. It reuses nothing from an earlier build.
func (s *Service) buildBranch(ctx context.Context, mirror gitx.Repo, w workspaces.Workspace, base string, files map[string][]byte, author gitx.Signature) (err error) {
	dir := filepath.Join(s.o.Workspaces.Layout().Dir(w.Slug), prWorktree)
	// Leftovers of a build that was interrupted.
	_ = mirror.RemoveWorktree(ctx, dir, true)
	_ = os.RemoveAll(dir)
	if _, rerr := mirror.ResolveRef(ctx, "refs/heads/"+PRBranch); rerr == nil {
		if derr := mirror.DeleteBranch(ctx, PRBranch); derr != nil {
			return fmt.Errorf("delete the old branch %s: %w", PRBranch, derr)
		}
	}
	wt, err := mirror.AddWorktree(ctx, dir, gitx.WorktreeOptions{Branch: PRBranch, Create: true, StartPoint: base})
	if err != nil {
		return fmt.Errorf("worktree of %s: %w", PRBranch, err)
	}
	defer func() {
		if rerr := mirror.RemoveWorktree(context.WithoutCancel(ctx), dir, true); rerr != nil {
			err = errors.Join(err, fmt.Errorf("remove the worktree of %s: %w", PRBranch, rerr))
		}
	}()
	paths := slices.Sorted(maps.Keys(files))
	for _, rel := range paths {
		if err := safefile.Write(filepath.Join(dir, filepath.FromSlash(rel)), files[rel], safefile.Options{}); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	if err := wt.Stage(ctx, paths...); err != nil {
		return fmt.Errorf("stage: %w", err)
	}
	if changes, err := wt.Status(ctx); err != nil {
		return fmt.Errorf("status: %w", err)
	} else if len(changes) == 0 {
		return errNothingToSend
	}
	if _, err := wt.Commit(ctx, gitx.CommitOptions{Message: "chore: add the Mestre session zero files", Author: author}); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func prBody(paths []string) string {
	body := "Arquivos da sessão zero do Mestre neste repositório:\n"
	for _, p := range paths {
		body += "\n- `" + p + "`"
	}
	return body + "\n\nO Mestre não altera o seu `AGENTS.md` nem o `.gitignore`."
}
