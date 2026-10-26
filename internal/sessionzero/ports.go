package sessionzero

import (
	"context"
	"errors"

	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
)

// The fixed Apps of every repository (docs/github/identidades-e-apps.md).
const (
	AppGrandMaster = "Grão-Mestre"
	AppMestre      = "Mestre"
)

// ErrNoPersonalCredential is what Installations.AddRepository answers when
// the owner has no personal credential to add the repository to the
// installation with: the owner then gets the link.
var ErrNoPersonalCredential = errors.New("the owner has no personal GitHub credential")

// Installations is GitHub's side of the stage apps
// (docs/github/criacao-de-apps.md#repositórios-novos-e-permissões-novas).
type Installations interface {
	// Missing lists the fixed Apps that are not installed in the repository
	// (empty: both are).
	Missing(ctx context.Context, repo workspaces.Repo) ([]string, error)
	// AddRepository adds the repository to the installation of the Apps
	// through the API, with the personal credential of the owner
	// (ErrNoPersonalCredential if there is none). It changes nothing in
	// the other repositories of the installation.
	AddRepository(ctx context.Context, repo workspaces.Repo, apps []string) error
	// InstallLink is the link for the owner to install an App (first
	// installation) or to tick the repository in the installation.
	InstallLink(ctx context.Context, repo workspaces.Repo, app string) (string, error)
}

// InstallLink is a link the owner is sent.
type InstallLink struct {
	App, URL string
}

// Notice is a message for the users of the workspace (Telegram and panel).
type Notice struct {
	// Kind says what it is about, for the panel to group: "apps_missing",
	// "claude_md", "agents_md_suggestions", "mestre_dir", "provider_files".
	Kind string
	// Text is the text for the users, in Portuguese.
	Text string
}

// Users reaches the people of the workspace (docs/telegram/bot.md,
// docs/painel/administracao.md). The answers come back through the Service
// (AnswerVersioning, AgentsMDDecided).
type Users interface {
	// AskOwnerToInstall sends the owner the links to install the Apps the
	// repository lacks.
	AskOwnerToInstall(ctx context.Context, workspace string, links []InstallLink) error
	// Notify tells the users of the workspace (Administrator and above).
	Notify(ctx context.Context, workspace string, n Notice) error
	// AskVersioning asks the users (Administrator and above) whether
	// .mestre/, except the execution files, is versioned in the project.
	// The question stays pending until AnswerVersioning.
	AskVersioning(ctx context.Context, workspace string) error
}

// Approvals is the flow of approval (docs/aprovacoes/fluxo.md, milestone 5).
type Approvals interface {
	// RequestAgentsMD asks the users to approve the AGENTS.md proposed for
	// the project, showing the whole text (type proposta_agents_md), and
	// returns the id of the request. It does not wait: the decision comes
	// back through Service.AgentsMDDecided.
	RequestAgentsMD(ctx context.Context, workspace, text string) (id string, err error)
}

// ChecklistItem is an item the checklist shows (docs/plataforma/checklist.md).
type ChecklistItem struct {
	// ID is the id of the item in the checklist ("ALL-14").
	ID        string
	Workspace string
	Text      string
}

// Checklist raises the items of the checklist (milestone 7 shows them).
type Checklist interface {
	Raise(ctx context.Context, item ChecklistItem) error
}

// GrandMaster starts the fixed and protected Grand Master of the workspace
// (docs/orquestracao/grao-mestre.md, milestone 5). Start can be called again:
// a Grand Master that is running stays.
type GrandMaster interface {
	Start(ctx context.Context, workspace string) error
}

// AnalysisInput is what the Grand Master analyses.
type AnalysisInput struct {
	Workspace string
	// Dir is the main clone.
	Dir string
	// HasAgentsMD says whether the project has an AGENTS.md.
	HasAgentsMD bool
}

// Analysis is what the Grand Master found.
type Analysis struct {
	// Summary is the language, the structure and how to install, run, test
	// and build, for the users.
	Summary string
	// ProposedAgentsMD is the AGENTS.md proposed when the project has
	// none: stack, commands and conventions.
	ProposedAgentsMD string
	// Suggestions are the improvements to an existing AGENTS.md; the users
	// apply them or not.
	Suggestions []string
	// Verification are the commands that prove the project is up (build and
	// tests), proposed for .mestre/regras/verificacao.yaml: they hold only after
	// an Administrator confirms them (docs/sessao-zero/workspace.md#comandos-de-verificação).
	Verification []string
}

// Analyst is the Grand Master analysing the project
// (docs/orquestracao/grao-mestre.md, milestone 5).
type Analyst interface {
	Analyze(ctx context.Context, in AnalysisInput) (Analysis, error)
}

// VerificationProposer keeps the proposal of the commands of verification in
// .mestre/regras/verificacao.yaml of the main clone dir, without confirming it
// (orchestration/verification.Keeper).
type VerificationProposer interface {
	Propose(ctx context.Context, workspace, dir string, commands []string) error
}

// PullRequest is the pull request of the session zero.
type PullRequest struct {
	// Workspace is the slug of the workspace: it names the mirror that holds
	// the branch.
	Workspace string
	Repo      workspaces.Repo
	// Branch is the branch in the mirror that holds the commit
	// (mestre/sessao-zero); Base the main branch of the repository.
	Branch, Base string
	Title, Body  string
}

// PullRequests sends the branch of the session zero and opens the pull
// request (docs/sessao-zero/workspace.md#pr-da-sessão-zero). Milestone 5
// implements it with the App "Grão-Mestre" and the lock on GitHub.
type PullRequests interface {
	// Author is the identity of the App "Grão-Mestre" as the author of
	// the commit.
	Author(ctx context.Context) (gitx.Signature, error)
	// Publish pushes the branch (the engine rebuilds it from the main
	// branch each time, so the push replaces it) and opens the pull request
	// if none is open for it. It returns the URL.
	Publish(ctx context.Context, pr PullRequest) (url string, err error)
}

// Rules is what the engine read of the rules of the main branch.
type Rules struct {
	// RequiresReview says whether a rule requires an approving review.
	RequiresReview bool
	// Summary says which rules were read (kept with the result).
	Summary string
}

// TestPR is the pull request of the approval test.
type TestPR struct {
	Number int
	// Branch is mestre/teste-aprovacao.
	Branch string
}

// The results of the approval test
// (docs/github/aprovacao-no-github.md#teste-na-sessão-zero-o-review-do-app-conta).
const (
	ResultNoRequirement = "sem_exigencia"
	ResultCounts        = "conta"
	ResultDoesNotCount  = "nao_conta"
)

// AppReview is GitHub's side of the approval test, step by step as the
// document of the approval on GitHub lists it (milestone 5 implements it).
type AppReview interface {
	// ReadRules reads the rules of the main branch (rules of the branch and
	// the classic protection).
	ReadRules(ctx context.Context, repo workspaces.Repo, branch string) (Rules, error)
	// OpenTestPR has the App "Mestre" create the branch mestre/teste-aprovacao
	// with a test file and open the pull request "[Mestre] Teste de aprovação
	// (pode ignorar)". When it fails halfway it still returns what it
	// created, for CloseTestPR.
	OpenTestPR(ctx context.Context, repo workspaces.Repo, base string) (TestPR, error)
	// ApproveAsGrandMaster has the App "Grão-Mestre" send a review APPROVE.
	ApproveAsGrandMaster(ctx context.Context, repo workspaces.Repo, pr TestPR) error
	// ReviewDecision is the reviewDecision of the pull request (GraphQL).
	ReviewDecision(ctx context.Context, repo workspaces.Repo, pr TestPR) (string, error)
	// CloseTestPR closes the pull request without merging and deletes the
	// branch. It tolerates what is already gone.
	CloseTestPR(ctx context.Context, repo workspaces.Repo, pr TestPR) error
}
