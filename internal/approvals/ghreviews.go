package approvals

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// ReviewReader is what the intake reads of GitHub, with the App "Grão-Mestre"
// (github.REST).
type ReviewReader interface {
	Reviews(ctx context.Context, repo string, n int) ([]github.Review, error)
	CollaboratorPermission(ctx context.Context, repo, login string) (string, error)
}

var _ ReviewReader = (*github.REST)(nil)

// OpenPRs finds the requests of PR and merge still open for a PR (chain.Store).
type OpenPRs interface {
	OpenPR(ctx context.Context, workspace, repo string, number int) ([]chain.Request, error)
}

// Answerer takes the answer of a user to a request, with the checks of the click
// (the Flow).
type Answerer interface {
	Answer(ctx context.Context, userID string, in Input) (Reply, error)
	RevokeVote(ctx context.Context, request int64, userID string) error
	DropPIN(ctx context.Context, request int64, userID string) error
}

var _ Answerer = (*Flow)(nil)

// PINPrompter asks a user the PIN of an approval that waits for it, by the
// channel (the Telegram bot).
type PINPrompter interface {
	AskPIN(ctx context.Context, userID string, rep Reply) error
}

// GitHubReviews takes the reviews made on GitHub itself as answers to the
// requests that wait for people, under the conditions of
// docs/github/aprovacao-no-github.md#review-feito-direto-no-github. It is a
// github.Sink: the reviews come from the events. A review that fails a condition
// is written in the audit with the reason and changes nothing.
type GitHubReviews struct {
	DB       *store.DB
	Clock    clock.Clock
	Requests OpenPRs
	Flow     Answerer
	Users    Directory
	// Reader gives the reader of the repository of a workspace.
	Reader func(ctx context.Context, workspace string) (ReviewReader, error)
	// PIN asks the PIN of a user whose valid review needs it; nil: the review
	// is not counted (it could not be confirmed).
	PIN    PINPrompter
	Logger *slog.Logger
}

var _ github.Sink = (*GitHubReviews)(nil)

// The outcomes written in the audit.
const (
	outcomeCounted   = "counted"
	outcomeIgnored   = "ignored"
	outcomePIN       = "pin_asked"
	outcomeDismissed = "dismissed"
)

func (g *GitHubReviews) log() *slog.Logger {
	if g.Logger != nil {
		return g.Logger
	}
	return slog.New(slog.DiscardHandler)
}

// Deliver implements github.Sink. Each review is handled once for each request
// and state; an error is a failure to read GitHub, which is tried again.
func (g *GitHubReviews) Deliver(ctx context.Context, w github.Watch, _ string, events []github.Event) error {
	for _, ev := range events {
		if ev.Kind != github.EventReview || ev.ID == 0 {
			continue
		}
		reqs, err := g.Requests.OpenPR(ctx, w.Workspace, ev.Repo, ev.Number)
		if err != nil {
			return err
		}
		if len(reqs) == 0 {
			continue
		}
		rd, err := g.Reader(ctx, w.Workspace)
		if err != nil {
			return err
		}
		reviews, err := rd.Reviews(ctx, ev.Repo, ev.Number)
		if err != nil {
			return err
		}
		var rv *github.Review
		for i := range reviews {
			if reviews[i].ID == ev.ID {
				rv = &reviews[i]
			}
		}
		state := eventState(ev.Key)
		for _, r := range reqs {
			if err := g.one(ctx, rd, r, ev, state, rv); err != nil {
				return err
			}
		}
	}
	return nil
}

// eventState is the state at the end of the key "revisao:<id>:<STATE>".
func eventState(key string) string {
	if i := strings.LastIndexByte(key, ':'); i >= 0 {
		return key[i+1:]
	}
	return ""
}

func (g *GitHubReviews) one(ctx context.Context, rd ReviewReader, r chain.Request, ev github.Event, state string, rv *github.Review) error {
	done, err := g.recorded(ctx, r.ID, ev.ID, state)
	if err != nil || done {
		return err
	}
	switch state {
	case "DISMISSED":
		return g.dismissed(ctx, r, ev, state, commitOf(rv))
	case "APPROVED":
	default:
		return g.record(ctx, r.ID, ev, state, "", commitOf(rv), outcomeIgnored, "o review é "+state+": só um review APPROVED vale como aprovação")
	}
	outcome, userID, reason, err := g.judge(ctx, rd, r, ev, rv)
	if err != nil {
		return err
	}
	return g.record(ctx, r.ID, ev, state, userID, commitOf(rv), outcome, reason)
}

// judge applies the conditions, from the live state of the review.
func (g *GitHubReviews) judge(ctx context.Context, rd ReviewReader, r chain.Request, ev github.Event, rv *github.Review) (outcome, userID, reason string, err error) {
	ignore := func(format string, a ...any) (string, string, string, error) {
		return outcomeIgnored, userID, fmt.Sprintf(format, a...), nil
	}
	switch {
	case rv == nil:
		return ignore("o review não está mais no PR")
	case rv.State != "APPROVED":
		return ignore("o review está %s agora", rv.State)
	case github.EndsWithMarker(rv.Body):
		// the review the engine itself posted (the Approver, in the name of a person) is
		// the application of an approval, not another vote
		return ignore("o review foi postado pelo próprio Mestre")
	case r.PR == nil || rv.Commit != r.PR.SHA:
		return ignore("o review é do commit %.8s, não do commit atual do pedido", rv.Commit)
	}
	u, found, err := g.userOf(ctx, rv.By)
	if err != nil {
		return "", "", "", err
	}
	if !found {
		return ignore("o autor %q não é um usuário ativo do Mestre", rv.By)
	}
	userID = u.ID
	if rv.ByType != "User" {
		return ignore("a conta %q é do tipo %s, não User", rv.By, orDash(rv.ByType))
	}
	write, err := g.canWrite(ctx, rd, ev.Repo, rv)
	if err != nil {
		return "", "", "", err
	}
	if !write {
		return ignore("%q não tem permissão de escrita no repositório agora", rv.By)
	}
	rep, aerr := g.Flow.Answer(ctx, u.ID, Input{Request: r.ID, Answer: chain.AnswerApprove, Reason: "review APPROVED no GitHub: " + ev.Link})
	switch {
	case aerr != nil && (errors.Is(aerr, ErrNoVerify) || errors.Is(aerr, context.Canceled) || errors.Is(aerr, context.DeadlineExceeded)):
		return "", "", "", aerr // the target could not be read: tried again with the event
	case aerr != nil:
		return ignore("%s", aerr.Error())
	case rep.PIN != "":
		if g.PIN == nil {
			return ignore("o pedido exige o PIN e o canal não pode pedi-lo")
		}
		if perr := g.PIN.AskPIN(ctx, u.ID, rep); perr != nil {
			return ignore("o pedido exige o PIN e ele não pôde ser pedido: %v", perr)
		}
		return outcomePIN, u.ID, "o review é válido e o pedido exige o PIN: ele foi pedido pelo canal", nil
	}
	return outcomeCounted, u.ID, i18n.Render(i18n.PtBR, rep.Msg), nil
}

// userOf finds the active user whose github_login is the login of the review;
// two with the same login is not a user.
func (g *GitHubReviews) userOf(ctx context.Context, login string) (users.User, bool, error) {
	all, err := g.Users.List(ctx)
	if err != nil {
		return users.User{}, false, err
	}
	var found []users.User
	for _, u := range all {
		if u.State == users.Active && u.GitHubLogin != "" && strings.EqualFold(u.GitHubLogin, login) {
			found = append(found, u)
		}
	}
	if len(found) != 1 {
		return users.User{}, false, nil
	}
	return found[0], true, nil
}

// canWrite reads the permission now. When GitHub does not give it to the App
// (403 or 404) the association of the review stands in, for the repository of a
// personal account: the owner or a collaborator. A failure to read is an error.
func (g *GitHubReviews) canWrite(ctx context.Context, rd ReviewReader, repo string, rv *github.Review) (bool, error) {
	perm, err := rd.CollaboratorPermission(ctx, repo, rv.By)
	var api *github.APIError
	switch {
	case err == nil:
		return perm == "admin" || perm == "maintain" || perm == "write", nil
	case errors.Is(err, github.ErrNotFound) || (errors.As(err, &api) && api.Status == 403):
		return rv.Association == "OWNER" || rv.Association == "COLLABORATOR", nil
	}
	return false, err
}

// dismissed: a review taken back does not count, and the vote it made goes away
// while the request is open.
func (g *GitHubReviews) dismissed(ctx context.Context, r chain.Request, ev github.Event, state, commit string) error {
	var userID string
	err := g.DB.Reader().QueryRowContext(ctx, `SELECT user_id FROM approval_github_reviews WHERE request = ? AND review = ? AND state = 'APPROVED' AND outcome IN (?, ?)`,
		r.ID, ev.ID, outcomeCounted, outcomePIN).Scan(&userID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if userID != "" {
		if err := g.Flow.DropPIN(ctx, r.ID, userID); err != nil {
			return err
		}
		if err := g.Flow.RevokeVote(ctx, r.ID, userID); err != nil {
			return err
		}
	}
	return g.record(ctx, r.ID, ev, state, userID, commit, outcomeDismissed, "o review foi descartado: não conta")
}

func (g *GitHubReviews) recorded(ctx context.Context, request, review int64, state string) (bool, error) {
	var n int
	err := g.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM approval_github_reviews WHERE request = ? AND review = ? AND state = ?`, request, review, state).Scan(&n)
	return n > 0, err
}

func (g *GitHubReviews) record(ctx context.Context, request int64, ev github.Event, state, userID, commit, outcome, reason string) error {
	g.log().Info("approvals: a review made on GitHub", "request", request, "review", ev.ID, "state", state, "outcome", outcome)
	return g.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO approval_github_reviews (request, review, state, login, commit_id, user_id, outcome, reason, at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (request, review, state) DO NOTHING`,
			request, ev.ID, state, ev.Author, commit, userID, outcome, reason, g.Clock.Now().UnixMilli())
		return err
	})
}

func commitOf(rv *github.Review) string {
	if rv == nil {
		return ""
	}
	return rv.Commit
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
