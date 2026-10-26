package sessionzero

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mestre-ia/mestre/internal/store"
)

// Versioning is the answer to whether .mestre/ goes into the project.
type Versioning string

// The answers. Until the users answer, .mestre/ is local.
const (
	VersioningUnset     Versioning = ""
	VersioningLocal     Versioning = "local"
	VersioningVersioned Versioning = "versioned"
)

// AgentsMDState is what the session zero did about the AGENTS.md.
type AgentsMDState string

// The states of the AGENTS.md.
const (
	AgentsMDUnseen   AgentsMDState = ""
	AgentsMDPresent  AgentsMDState = "present" // the project has one: never touched
	AgentsMDProposed AgentsMDState = "proposed"
	AgentsMDApproved AgentsMDState = "approved"
	AgentsMDRejected AgentsMDState = "rejected"
)

// State is what the session zero keeps of a workspace (table session_zero),
// for the panel and for the stages to be run again without repeating
// themselves.
type State struct {
	Slug string
	// Versioning is the choice about .mestre/; VersioningUnset while the
	// question is pending (AskedAt is when it was asked).
	Versioning           Versioning
	VersioningAskedAt    time.Time
	VersioningAnsweredAt time.Time
	// AppsNotifiedAt is when the owner was sent the links of the Apps.
	AppsNotifiedAt time.Time
	AgentsMD       AgentsMDState
	// AgentsMDRequest is the id of the approval request, AgentsMDText the
	// text proposed.
	AgentsMDRequest string
	AgentsMDText    string
	ClaudeMDWarned  time.Time
	// PRURL is the pull request of the session zero.
	PRURL string
	// ApprovalTest is the result (ResultNoRequirement, ResultCounts,
	// ResultDoesNotCount; empty: not run), when it ran and which rules it
	// read.
	ApprovalTest        string
	ApprovalTestAt      time.Time
	ApprovalTestSummary string
}

// Store keeps State in the Guild database.
type Store struct{ DB *store.DB }

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixMilli()
}

func fromMs(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

const stateColumns = `slug, versioning, versioning_asked_at, versioning_answered_at, apps_notified_at, agents_md_state,
	agents_md_request, agents_md_text, claude_md_warned_at, pr_url, approval_test_result, approval_test_at, approval_test_summary`

// Get returns the state of the workspace; a workspace the session zero has
// not touched yet has the zero State.
func (s Store) Get(ctx context.Context, slug string) (State, error) {
	var (
		st                                        State
		versioning, md                            string
		asked, answered, notified, warned, testAt int64
	)
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT `+stateColumns+` FROM session_zero WHERE slug = ?`, slug).Scan(
		&st.Slug, &versioning, &asked, &answered, &notified, &md, &st.AgentsMDRequest, &st.AgentsMDText, &warned,
		&st.PRURL, &st.ApprovalTest, &testAt, &st.ApprovalTestSummary)
	if errors.Is(err, sql.ErrNoRows) {
		return State{Slug: slug}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("session zero %s: %w", slug, err)
	}
	st.Versioning, st.AgentsMD = Versioning(versioning), AgentsMDState(md)
	st.VersioningAskedAt, st.VersioningAnsweredAt = fromMs(asked), fromMs(answered)
	st.AppsNotifiedAt, st.ClaudeMDWarned, st.ApprovalTestAt = fromMs(notified), fromMs(warned), fromMs(testAt)
	return st, nil
}

// set changes columns of the row of the workspace, creating the row.
func (s Store) set(ctx context.Context, slug string, now time.Time, set string, args ...any) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO session_zero (slug, updated_at) VALUES (?, ?)`, slug, ms(now)); err != nil {
			return err
		}
		args = append(append(args, ms(now)), slug)
		_, err := tx.ExecContext(ctx, `UPDATE session_zero SET `+set+`, updated_at = ? WHERE slug = ?`, args...)
		return err
	})
}

// AppsNotified records that the owner was sent the links.
func (s Store) AppsNotified(ctx context.Context, slug string, now time.Time) error {
	return s.set(ctx, slug, now, `apps_notified_at = ?`, ms(now))
}

// VersioningAsked records that the users were asked.
func (s Store) VersioningAsked(ctx context.Context, slug string, now time.Time) error {
	return s.set(ctx, slug, now, `versioning_asked_at = ?`, ms(now))
}

// VersioningAnswered records the choice.
func (s Store) VersioningAnswered(ctx context.Context, slug string, now time.Time, v Versioning) error {
	return s.set(ctx, slug, now, `versioning = ?, versioning_answered_at = ?`, string(v), ms(now))
}

// AgentsMDPresent records that the project has an AGENTS.md.
func (s Store) AgentsMDPresent(ctx context.Context, slug string, now time.Time) error {
	return s.set(ctx, slug, now, `agents_md_state = ?`, string(AgentsMDPresent))
}

// AgentsMDProposed records the proposal sent to the users.
func (s Store) AgentsMDProposed(ctx context.Context, slug string, now time.Time, request, text string) error {
	return s.set(ctx, slug, now, `agents_md_state = ?, agents_md_request = ?, agents_md_text = ?`, string(AgentsMDProposed), request, text)
}

// AgentsMDDecided records the decision on the proposal.
func (s Store) AgentsMDDecided(ctx context.Context, slug string, now time.Time, st AgentsMDState) error {
	return s.set(ctx, slug, now, `agents_md_state = ?`, string(st))
}

// ClaudeMDWarned records that the users were warned about the CLAUDE.md.
func (s Store) ClaudeMDWarned(ctx context.Context, slug string, now time.Time) error {
	return s.set(ctx, slug, now, `claude_md_warned_at = ?`, ms(now))
}

// PROpened records the pull request of the session zero.
func (s Store) PROpened(ctx context.Context, slug string, now time.Time, url string) error {
	return s.set(ctx, slug, now, `pr_url = ?`, url)
}

// ApprovalTested records the result of the approval test.
func (s Store) ApprovalTested(ctx context.Context, slug string, now time.Time, result, summary string) error {
	return s.set(ctx, slug, now, `approval_test_result = ?, approval_test_at = ?, approval_test_summary = ?`, result, ms(now), summary)
}
