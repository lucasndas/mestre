package approvals

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/users"
)

// ReviewClient is the part of GitHub the application of an approval uses
// (github.REST, with the token of the App that acts or of the person).
type ReviewClient interface {
	PullData(ctx context.Context, repo string, n int) (github.PullData, error)
	CreateReview(ctx context.Context, repo string, n int, sha, event, body string) (int64, error)
}

var _ ReviewClient = (*github.REST)(nil)

// ApprovedSource finds the approvals that stand for a commit of a PR
// (chain.Store.ApprovedPR).
type ApprovedSource interface {
	ApprovedPR(ctx context.Context, workspace, repo string, number int, sha string) ([]chain.Request, error)
}

// ErrNoCredential is what Personal answers for a person without a personal
// credential, or whose credential expired: nothing is done in her name.
var ErrNoCredential = github.ErrNoCredential

// Personal gives the client of a person acting as herself on GitHub, with her
// user access token (docs/github/aprovacao-no-github.md#credencial-pessoal-user-access-token-pelo-device-flow).
// A suspended or removed user, a missing or expired credential is ErrNoCredential.
type Personal interface {
	Client(ctx context.Context, userID string) (ReviewClient, error)
}

// PersonalSource is the personal credentials of the users (*github.Personal).
type PersonalSource interface {
	Client(ctx context.Context, userID string) (*github.REST, error)
}

var _ PersonalSource = (*github.Personal)(nil)

// PersonalCredentials is the Personal over the credentials of the users.
type PersonalCredentials struct{ Source PersonalSource }

var _ Personal = PersonalCredentials{}

// Client implements Personal.
func (p PersonalCredentials) Client(ctx context.Context, userID string) (ReviewClient, error) {
	c, err := p.Source.Client(ctx, userID)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Reviewers tells who the repository requires to approve a PR: the logins on
// GitHub of the code owners of its files, when the branch asks for a code owner
// review (github.CodeOwners). An error is a decision for a person.
type Reviewers interface {
	Required(ctx context.Context, workspace, repo string, n int) ([]string, error)
}

// Delegations says whether a person delegated to the Grão-Mestre, by decree,
// the approval of a request ([decretos](docs/telegram/decretos.md)).
type Delegations interface {
	// Delegation is the user who delegated, when a decree of delegation covers
	// the request.
	Delegation(ctx context.Context, r chain.Request) (author string, ok bool, err error)
	// Used registers the use of the decree.
	Used(ctx context.Context, r chain.Request, by string) error
}

// Opener opens a request of approval (chain.Service).
type Opener interface {
	Request(ctx context.Context, o chain.OpenRequest) (int64, error)
}

// Tell says something to a user by the channel (the Telegram bot).
type Tell interface {
	Tell(ctx context.Context, userID, text string) error
}

// Approver applies on GitHub the approvals decided in the Mestre, under the
// rules of docs/github/aprovacao-no-github.md: it never goes around a rule of
// the repository and never uses the credential of a person without her
// approval of that action or her delegation.
type Approver struct {
	// Approved finds the approvals that stand; Opener makes the request to a
	// person.
	Approved ApprovedSource
	Opener   Opener
	// GrandMaster gives the client of the App "Grão-Mestre" for a workspace and
	// the login of its bot.
	GrandMaster func(ctx context.Context, workspace string) (ReviewClient, string, error)
	Personal    Personal
	Required    Reviewers
	Delegations Delegations
	Users       Directory
	Modes       *Modes
	Tell        Tell
	// Repo gives the repository of a workspace: an order for another one is
	// refused. Nil: not checked.
	Repo   func(ctx context.Context, workspace string) (string, error)
	Logger *slog.Logger
}

// ApproveOrder is `mestre gh approve`, or the application of an approval of
// people.
type ApproveOrder struct {
	Workspace string
	Repo      string
	Number    int
	// SHA is the commit the approval is for (head_sha).
	SHA string
	// Reason becomes the text of the review when the Grão-Mestre approves alone.
	Reason string
	// OnBehalf is the user the Grão-Mestre approves for, with the credential of
	// that person (`--on-behalf`).
	OnBehalf string
}

// How the review was made.
type ReviewAs string

// The ways.
const (
	// AsGrandMaster: the App "Grão-Mestre" reviewed.
	AsGrandMaster ReviewAs = "grand_master"
	// AsPerson: the person reviewed, with her credential.
	AsPerson ReviewAs = "person"
	// AsDelegate: the Grão-Mestre reviewed with the credential of a person who
	// delegated.
	AsDelegate ReviewAs = "delegation"
	// AsLink: the person was sent the link of the PR to approve on GitHub; the
	// review is seen by the events and concludes the request.
	AsLink ReviewAs = "link"
)

// Approval is what Approve did.
type Approval struct {
	As     ReviewAs
	Review int64
	// Request is the request made to a person, when there is one.
	Request int64
}

// The refusals.
var (
	// ErrHeadChanged: the PR moved since the approval was decided.
	ErrHeadChanged = errors.New("approvals: the head of the PR changed")
	// ErrPRClosed: the PR is not open.
	ErrPRClosed = errors.New("approvals: the PR is not open")
)

// CodeNoDelegation is the refusal of `--on-behalf` with no decree of delegation that covers it.
const CodeNoDelegation = "no_delegation"

// noDelegation turns the human_required refusal of the branch of `--on-behalf` into the one
// that says there is no delegation (any other error is left as it is).
func noDelegation(err error) error {
	var ref *chain.Refusal
	if errors.As(err, &ref) && ref.Code == chain.CodeHumanRequired {
		return &chain.Refusal{Code: CodeNoDelegation, Message: ref.Message}
	}
	return err
}

func human(format string, a ...any) error {
	return &chain.Refusal{Code: chain.CodeHumanRequired, Message: fmt.Sprintf(format, a...)}
}

func (a *Approver) log() *slog.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return slog.New(slog.DiscardHandler)
}

// Approve sends the review that applies the approval standing for the PR at the
// commit. Without an approval of the Grão-Mestre under the policy or of a user
// for that exact commit it refuses with human_required and sends nothing.
func (a *Approver) Approve(ctx context.Context, o ApproveOrder) (Approval, error) {
	if err := checkRepo(ctx, a.Repo, o.Workspace, o.Repo); err != nil {
		return Approval{}, err
	}
	gm, gmLogin, err := a.GrandMaster(ctx, o.Workspace)
	if err != nil {
		return Approval{}, err
	}
	pr, err := gm.PullData(ctx, o.Repo, o.Number)
	if err != nil {
		return Approval{}, err
	}
	switch {
	case pr.State != "open" || pr.Merged:
		return Approval{}, ErrPRClosed
	case pr.HeadSHA != o.SHA:
		return Approval{}, fmt.Errorf("%w: it is %.8s now", ErrHeadChanged, pr.HeadSHA)
	}
	standing, err := a.Approved.ApprovedPR(ctx, o.Workspace, o.Repo, o.Number, o.SHA)
	if err != nil {
		return Approval{}, err
	}
	if len(standing) == 0 {
		return Approval{}, human("não há aprovação vigente para o PR #%d no commit %.8s", o.Number, o.SHA)
	}
	best := standing[len(standing)-1]
	byPerson := ""
	for _, r := range slicesReverse(standing) {
		if r.Decision != nil && strings.HasPrefix(r.Decision.By, "user:") {
			best, byPerson = r, strings.TrimPrefix(r.Decision.By, "user:")
			break
		}
	}
	humanMode := false
	if a.Modes != nil {
		mode, _, err := a.Modes.Get(ctx, o.Workspace)
		if err != nil {
			return Approval{}, err
		}
		humanMode = mode == ModeHuman
	}
	if humanMode && byPerson == "" {
		return Approval{}, human("o workspace está no modo de aprovação humana: só a aprovação de uma pessoa vale")
	}
	if gmLogin == "" {
		return Approval{}, errors.New("approvals: the login of the App Grão-Mestre is not known")
	}
	openedByGM := strings.EqualFold(pr.Author, gmLogin)
	required, err := a.required(ctx, o)
	if err != nil {
		return Approval{}, human("não foi possível saber quem o repositório exige para aprovar o PR #%d: %v", o.Number, err)
	}
	if !openedByGM && !humanMode && len(required) == 0 && o.OnBehalf == "" {
		return a.asGrandMaster(ctx, gm, o, best, byPerson)
	}
	return a.asPerson(ctx, o, best, byPerson, required, openedByGM)
}

func slicesReverse(rs []chain.Request) []chain.Request {
	out := make([]chain.Request, len(rs))
	for i, r := range rs {
		out[len(rs)-1-i] = r
	}
	return out
}

func (a *Approver) required(ctx context.Context, o ApproveOrder) ([]string, error) {
	if a.Required == nil {
		return nil, nil
	}
	return a.Required.Required(ctx, o.Workspace, o.Repo, o.Number)
}

// asGrandMaster is the case where any approval will do: the App "Grão-Mestre"
// reviews, with the reason or the name of the person in the text.
func (a *Approver) asGrandMaster(ctx context.Context, gm ReviewClient, o ApproveOrder, r chain.Request, byPerson string) (Approval, error) {
	text := strings.TrimSpace(o.Reason)
	if byPerson != "" {
		text = fmt.Sprintf("Aprovado pelo Grão-Mestre após aprovação de %s (pedido %d)", a.nameOf(ctx, byPerson), r.ID)
	}
	if text == "" {
		text = fmt.Sprintf("Aprovado pelo Grão-Mestre (pedido %d)", r.ID)
	}
	id, err := gm.CreateReview(ctx, o.Repo, o.Number, o.SHA, github.ReviewApprove, github.WithMarker(text, github.NewOp()))
	if err != nil {
		return Approval{}, a.refusedReview(err)
	}
	return Approval{As: AsGrandMaster, Review: id}, nil
}

// refusedReview turns the refusal of GitHub into the answer the Grão-Mestre
// reads: a 422 (the author approving his own PR, a rule the review can not
// satisfy) goes to a person.
func (a *Approver) refusedReview(err error) error {
	var api *github.APIError
	if errors.As(err, &api) && api.Status == 422 {
		return human("o GitHub recusou o review: %s", api.Message)
	}
	return err
}

// asPerson is the case where a person must review: the repository requires one
// (a code owner), or the PR was opened by the App "Grão-Mestre".
func (a *Approver) asPerson(ctx context.Context, o ApproveOrder, r chain.Request, byPerson string, required []string, openedByGM bool) (Approval, error) {
	targets, err := a.targets(ctx, o, byPerson, required)
	if err != nil {
		return Approval{}, err
	}
	if len(targets) == 0 {
		return Approval{}, human("o PR precisa da aprovação de uma pessoa e nenhuma pessoa cadastrada a dá")
	}
	var lastWaiting Approval
	for _, p := range targets {
		switch {
		case p.ID == byPerson:
			// the person approved this action: the review is hers, with her credential
			ap, err := a.withCredential(ctx, o, p, fmt.Sprintf("Aprovado por %s pelo Mestre (pedido %d)", plain(p.Name), r.ID), AsPerson)
			if err == nil {
				return ap, nil
			}
			if !errors.Is(err, ErrNoCredential) {
				return Approval{}, err
			}
			lastWaiting, err = a.sendLink(ctx, o, p)
			if err != nil {
				return Approval{}, err
			}
		case o.OnBehalf != "" && !openedByGM && a.sameUser(p, o.OnBehalf):
			author, ok, err := a.delegation(ctx, r)
			if err != nil {
				return Approval{}, err
			}
			if !ok || author != p.ID {
				ap, err := a.askPerson(ctx, o, r, p)
				return ap, noDelegation(err)
			}
			ap, err := a.withCredential(ctx, o, p, fmt.Sprintf("Aprovado pelo Grão-Mestre em nome de %s, por delegação", plain(p.Name)), AsDelegate)
			if err != nil {
				if errors.Is(err, ErrNoCredential) {
					ap, err := a.askPerson(ctx, o, r, p)
					return ap, noDelegation(err)
				}
				return Approval{}, err
			}
			if a.Delegations != nil {
				if err := a.Delegations.Used(ctx, r, "grand_master"); err != nil {
					a.log().Warn("approvals: the use of the delegation was not registered", "request", r.ID, "err", err)
				}
			}
			return ap, nil
		}
	}
	if lastWaiting.As == AsLink {
		return lastWaiting, nil
	}
	if o.OnBehalf != "" {
		return Approval{}, noDelegation(human("%s não está entre as pessoas que o PR #%d exige", o.OnBehalf, o.Number))
	}
	// nobody of the required ones approved it yet: ask the first
	return a.askPerson(ctx, o, r, targets[0])
}

func (a *Approver) delegation(ctx context.Context, r chain.Request) (string, bool, error) {
	if a.Delegations == nil {
		return "", false, nil
	}
	return a.Delegations.Delegation(ctx, r)
}

// targets are the people whose review the PR needs: the ones the repository
// requires, and, when it requires none, the person who approved it.
func (a *Approver) targets(ctx context.Context, o ApproveOrder, byPerson string, required []string) ([]users.User, error) {
	all, err := a.Users.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []users.User
	for _, u := range all {
		if u.State != users.Active {
			continue
		}
		switch {
		case len(required) > 0:
			for _, login := range required {
				if u.GitHubLogin != "" && strings.EqualFold(u.GitHubLogin, login) {
					out = append(out, u)
				}
			}
		case u.ID == byPerson, o.OnBehalf != "" && a.sameUser(u, o.OnBehalf):
			out = append(out, u)
		}
	}
	if len(required) > 0 && len(out) < len(required) {
		return nil, human("o repositório exige a revisão de %s, que não tem cadastro ativo no Mestre", strings.Join(required, ", "))
	}
	return out, nil
}

func (a *Approver) sameUser(u users.User, ref string) bool {
	return u.ID == ref || strings.EqualFold(u.Name, ref) || (u.GitHubLogin != "" && strings.EqualFold(u.GitHubLogin, strings.TrimPrefix(ref, "@")))
}

func (a *Approver) nameOf(ctx context.Context, id string) string {
	if u, err := a.Users.Get(ctx, id); err == nil {
		return plain(u.Name)
	}
	return id
}

// withCredential reviews as the person, with her token, on the commit.
func (a *Approver) withCredential(ctx context.Context, o ApproveOrder, p users.User, text string, as ReviewAs) (Approval, error) {
	if a.Personal == nil {
		return Approval{}, ErrNoCredential
	}
	c, err := a.Personal.Client(ctx, p.ID)
	if err != nil {
		return Approval{}, err
	}
	id, err := c.CreateReview(ctx, o.Repo, o.Number, o.SHA, github.ReviewApprove, github.WithMarker(text, github.NewOp()))
	if err != nil {
		return Approval{}, a.refusedReview(err)
	}
	return Approval{As: as, Review: id}, nil
}

// sendLink gives the person the link of the PR to approve on GitHub.
func (a *Approver) sendLink(ctx context.Context, o ApproveOrder, p users.User) (Approval, error) {
	if a.Tell == nil {
		return Approval{}, human("%s precisa aprovar o PR #%d no GitHub e o Mestre não tem como avisá-la", plain(p.Name), o.Number)
	}
	text := fmt.Sprintf("O PR #%d precisa do seu review no GitHub: https://github.com/%s/pull/%d\nO Mestre percebe o seu review e conclui sozinho.", o.Number, strings.ToLower(o.Repo), o.Number)
	if err := a.Tell.Tell(ctx, p.ID, text); err != nil {
		return Approval{}, human("o link do PR #%d não chegou a %s: %v", o.Number, plain(p.Name), err)
	}
	return Approval{As: AsLink}, nil
}

// askPerson makes the request to the person whose review the PR needs and
// refuses the Grão-Mestre: nothing is done in her name without her.
func (a *Approver) askPerson(ctx context.Context, o ApproveOrder, from chain.Request, p users.User) (Approval, error) {
	if a.Opener == nil {
		return Approval{}, human("%s precisa aprovar o PR #%d", plain(p.Name), o.Number)
	}
	typ := from.Type
	if typ == "" {
		typ = classes.RequestMerge
	}
	id, err := a.Opener.Request(ctx, chain.OpenRequest{Type: typ, Workspace: o.Workspace, Table: from.Table, About: fmt.Sprintf("aprovação de %s para o PR #%d", plain(p.Name), o.Number),
		PR: &chain.PRRef{Repo: o.Repo, Number: o.Number, SHA: o.SHA}, Require: []string{p.ID}, Target: from.Target, Risk: from.Risk})
	if err != nil {
		return Approval{}, err
	}
	return Approval{Request: id}, human("o pedido #%d foi para %s: o PR #%d só vale com a aprovação dela", id, plain(p.Name), o.Number)
}

// Decided implements chain.Sink for the requests of PR and merge: an approval
// that people decided is applied on GitHub, as docs/github/aprovacao-no-github.md
// lists it. A denial, or an approval of the Grão-Mestre (he applies it with
// `mestre gh approve`), does nothing here.
func (a *Approver) Decided(ctx context.Context, r chain.Result) error {
	q := r.Request
	if !r.Approved || q.PR == nil || q.Decision == nil || !strings.HasPrefix(q.Decision.By, "user:") {
		return nil
	}
	_, err := a.Approve(ctx, ApproveOrder{Workspace: q.Workspace, Repo: q.PR.Repo, Number: q.PR.Number, SHA: q.PR.SHA})
	var ref *chain.Refusal
	if errors.As(err, &ref) && ref.Code == chain.CodeHumanRequired {
		a.log().Warn("approvals: an approval of people was not applied on GitHub", "request", q.ID, "reason", ref.Message)
	}
	return err
}

// checkRepo refuses, closed, a repository other than the one of the workspace, or
// a workspace whose repository cannot be read.
func checkRepo(ctx context.Context, repoOf func(context.Context, string) (string, error), workspace, repo string) error {
	if repoOf == nil {
		return nil
	}
	want, err := repoOf(ctx, workspace)
	if err != nil {
		return fmt.Errorf("%w: the repository of %s could not be read: %v", chain.ErrWrongRepo, workspace, err)
	}
	if !chain.SameRepo(want, repo) {
		return fmt.Errorf("%w: %q is not %q", chain.ErrWrongRepo, repo, want)
	}
	return nil
}
