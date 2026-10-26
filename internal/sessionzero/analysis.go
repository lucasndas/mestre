package sessionzero

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/internal/safefile"
)

// The checklist item of a project with a CLAUDE.md
// (docs/plataforma/checklist.md, ALL-14).
const checklistClaudeMD = "ALL-14"

// hasFile says whether the project has the file: versioned at HEAD of the
// main clone, or there in the folder (a CLAUDE.local.md is never versioned).
// A link by that name counts: it is something the person put there.
func hasFile(ctx context.Context, repo gitx.Repo, dir, name string) (bool, error) {
	if _, err := repo.ReadFile(ctx, "HEAD", name); err == nil {
		return true, nil
	} else if !errors.Is(err, gitx.ErrFileNotFound) && !errors.Is(err, gitx.ErrRefNotFound) {
		return false, fmt.Errorf("read %s at HEAD: %w", name, err)
	}
	info, err := safefile.Stat(filepath.Join(dir, name))
	switch {
	case err == nil:
		return info.Exists, nil
	case safefile.Refused(err):
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, fmt.Errorf("stat %s: %w", name, err)
}

// runAnalysis is the stage analysis: the Grand Master analyses the project.
// Without an AGENTS.md it proposes one, as an approval the users answer when
// they can; the stage ends with the proposal sent and the workspace does not
// wait for the answer. With an AGENTS.md it touches nothing and may suggest
// improvements. With a CLAUDE.md or CLAUDE.local.md it warns that the Claude
// agents read that file instead (docs/sessao-zero/workspace.md).
func (s *Service) runAnalysis(ctx context.Context, w workspaces.Workspace) error {
	dir := s.o.Workspaces.Layout().Principal(w.Slug)
	repo, err := s.o.Workspaces.OpenPrincipal(ctx, w.Slug)
	if err != nil {
		return code("worktree_failed", err)
	}
	hasAgents, err := hasFile(ctx, repo, dir, "AGENTS.md")
	if err != nil {
		return code("analysis_failed", err)
	}
	claude := false
	for _, name := range []string{"CLAUDE.md", "CLAUDE.local.md"} {
		has, err := hasFile(ctx, repo, dir, name)
		if err != nil {
			return code("analysis_failed", err)
		}
		claude = claude || has
	}
	st, err := s.store.Get(ctx, w.Slug)
	if err != nil {
		return err
	}
	if claude && st.ClaudeMDWarned.IsZero() {
		if err := s.warnClaudeMD(ctx, w.Slug); err != nil {
			return code("notify_failed", err)
		}
	}
	a, err := s.o.Analyst.Analyze(ctx, AnalysisInput{Workspace: w.Slug, Dir: dir, HasAgentsMD: hasAgents})
	if err != nil {
		return code("analysis_failed", err)
	}
	if s.o.Verification != nil && len(a.Verification) > 0 {
		if err := s.o.Verification.Propose(ctx, w.Slug, dir, a.Verification); err != nil {
			return code("verification_proposal_failed", err)
		}
	}
	if hasAgents {
		return s.keepAgentsMD(ctx, w.Slug, st, a)
	}
	return s.proposeAgentsMD(ctx, w.Slug, st, a)
}

func (s *Service) warnClaudeMD(ctx context.Context, slug string) error {
	text := "O projeto tem um CLAUDE.md (ou CLAUDE.local.md): os agentes Claude vão ler esse arquivo no lugar do AGENTS.md, " +
		"que os agentes Codex e Antigravity leem. Sugestão: unificar tudo no AGENTS.md."
	if err := s.o.Users.Notify(ctx, slug, Notice{Kind: "claude_md", Text: text}); err != nil {
		return err
	}
	if err := s.o.Checklist.Raise(ctx, ChecklistItem{ID: checklistClaudeMD, Workspace: slug, Text: text}); err != nil {
		return err
	}
	return s.store.ClaudeMDWarned(ctx, slug, s.o.Clock.Now())
}

// keepAgentsMD: the AGENTS.md of the project is never edited; the
// improvements the Grand Master sees go to the users, once.
func (s *Service) keepAgentsMD(ctx context.Context, slug string, st State, a Analysis) error {
	if st.AgentsMD == AgentsMDPresent {
		return nil
	}
	if len(a.Suggestions) > 0 {
		text := "Sugestões para o AGENTS.md do projeto (o Mestre não o altera; vocês aplicam ou não):\n- " + strings.Join(a.Suggestions, "\n- ")
		if err := s.o.Users.Notify(ctx, slug, Notice{Kind: "agents_md_suggestions", Text: text}); err != nil {
			return code("notify_failed", err)
		}
	}
	return s.store.AgentsMDPresent(ctx, slug, s.o.Clock.Now())
}

// proposeAgentsMD sends the proposal as an approval, once.
func (s *Service) proposeAgentsMD(ctx context.Context, slug string, st State, a Analysis) error {
	if st.AgentsMD != AgentsMDUnseen {
		return nil // already proposed (and maybe decided): never twice
	}
	if strings.TrimSpace(a.ProposedAgentsMD) == "" {
		return code("analysis_failed", errors.New("a análise não trouxe a proposta de AGENTS.md"))
	}
	id, err := s.o.Approvals.RequestAgentsMD(ctx, slug, a.ProposedAgentsMD)
	if err != nil {
		return code("approval_request_failed", err)
	}
	return s.store.AgentsMDProposed(ctx, slug, s.o.Clock.Now(), id, a.ProposedAgentsMD)
}
