package quests

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The acceptance of the Master of the table before the pull request
// (docs/orquestracao/mestre-da-mesa.md#o-pedido-que-chega-o-mestre-da-mesa-como-po):
// `mestre pr open` is refused with `criteria_unproven` while a completed
// mission of the epic has a criterion the engine did not record a proof for.

// DefaultMaxCorrections is how many fix missions an epic may have; the fourth
// is refused and the Master of the table asks the Grand Master.
const DefaultMaxCorrections = 3

// ErrCorrectionLimit: the epic already has all the fix missions it may have.
var ErrCorrectionLimit = errors.New("the epic already has the fix missions it may have")

// ErrOtherIssue: the issue of a mission is not one of the table.
var ErrOtherIssue = errors.New("the issue is not one of the table")

// Acceptance tells the criteria of the epic with no proof. Its Unproven is the
// `github.Criteria` of `mestre pr open`.
type Acceptance struct {
	Store *Store
	// Body reads the body of an issue as it is now, so a criterion the Master
	// of the table took out of the issue (with its reason in a comment) stops
	// being asked. Nil: the criteria are the ones frozen when the last
	// mission of the issue ran.
	Body func(ctx context.Context, workspace string, number int) (string, error)
}

// Unproven lists, as "C3 (issue #41)", the criteria with no proof of the
// issues of the completed missions of the table, in the order of the issues
// and of the criteria. The key is "<workspace>/<table>". A proof recorded in
// any mission of the issue, in its current round, counts: a fix mission
// proves what the earlier one left. The body of the issue is not a source: it
// is edited by anyone. Any error stops the opening.
func (a Acceptance) Unproven(ctx context.Context, tableKey string) ([]string, error) {
	workspace, table, ok := strings.Cut(tableKey, "/")
	if !ok || workspace == "" || table == "" || a.Store == nil {
		return nil, fmt.Errorf("quests: %q is not the key of a table", tableKey)
	}
	qs, err := a.Store.OfTable(ctx, workspace, table)
	if err != nil {
		return nil, err
	}
	type group struct {
		completed bool
		proved    map[string]bool
		latest    *Quest // the last quest of the issue that froze criteria
		frozen    []Criterion
	}
	groups := map[int]*group{}
	for i := range qs {
		q := qs[i]
		if q.Issue == 0 {
			continue
		}
		g := groups[q.Issue]
		if g == nil {
			g = &group{proved: map[string]bool{}}
			groups[q.Issue] = g
		}
		g.completed = g.completed || q.State == Completed
		proofs, err := a.Store.Proofs(ctx, q.ID, q.Round)
		if err != nil {
			return nil, err
		}
		for _, p := range proofs {
			g.proved[p.Criterion] = true
		}
		cs, err := a.Store.Criteria(ctx, q.ID)
		if err != nil {
			return nil, err
		}
		if len(cs) > 0 && (g.latest == nil || !q.CreatedAt.Before(g.latest.CreatedAt)) {
			g.latest, g.frozen = &q, cs
		}
	}
	issues := make([]int, 0, len(groups))
	for n, g := range groups {
		if g.completed {
			issues = append(issues, n)
		}
	}
	slices.Sort(issues)
	var out []string
	for _, n := range issues {
		g := groups[n]
		cs := g.frozen
		if a.Body != nil {
			body, err := a.Body(ctx, workspace, n)
			if err != nil {
				return nil, fmt.Errorf("the issue #%d could not be read for its criteria: %w", n, err)
			}
			cs = parseCriteria(body)
			if len(cs) == 0 && len(g.frozen) > 0 {
				// an issue emptied of its criteria is not an acceptance: ask the Grand Master
				return nil, fmt.Errorf("the issue #%d has no criteria left although its missions had some", n)
			}
		}
		slices.SortStableFunc(cs, func(x, y Criterion) int { return criterionNumber(x.ID) - criterionNumber(y.ID) })
		for _, c := range cs {
			if !g.proved[c.ID] {
				out = append(out, fmt.Sprintf("%s (issue #%d)", c.ID, n))
			}
		}
	}
	return out, nil
}

// criterionNumber is the n of "C<n>".
func criterionNumber(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "C"))
	return n
}

// IssueOwner says whether an issue is one of a table (its epic or one the
// Master of the table made under it): a mission writes its proofs in the
// issue, so it never takes one of another table.
type IssueOwner interface {
	Owns(ctx context.Context, workspace, table string, issue int) (bool, error)
}

// checkIssue is the check of the `issue` of a new mission: it is the table's,
// and a fix mission (an issue an earlier mission of the table already has) is
// one of at most MaxCorrections per epic.
func (s *Service) checkIssue(ctx context.Context, workspace, table string, issue int) error {
	if issue == 0 {
		return nil
	}
	if s.o.Issues != nil {
		ok, err := s.o.Issues.Owns(ctx, workspace, table, issue)
		if err != nil {
			return fmt.Errorf("the issue #%d could not be checked: %w", issue, err)
		}
		if !ok {
			return fmt.Errorf("%w: #%d", ErrOtherIssue, issue)
		}
	}
	qs, err := s.st.OfTable(ctx, workspace, table)
	if err != nil {
		return err
	}
	return s.checkCorrections(qs, issue)
}

// checkCorrections refuses the fix mission past the limit, given the missions
// of the table: it runs again inside the transaction that inserts the mission,
// so two creations at once cannot both take the last place.
func (s *Service) checkCorrections(qs []Quest, issue int) error {
	slices.SortStableFunc(qs, func(a, b Quest) int { return a.CreatedAt.Compare(b.CreatedAt) })
	seen := map[int]bool{}
	fixes := 0
	for _, q := range qs {
		if q.Issue == 0 {
			continue
		}
		if seen[q.Issue] {
			fixes++
		}
		seen[q.Issue] = true
	}
	limit := s.o.MaxCorrections
	if limit <= 0 {
		limit = DefaultMaxCorrections
	}
	if seen[issue] && fixes >= limit {
		return fmt.Errorf("%w: %d fix missions", ErrCorrectionLimit, fixes)
	}
	return nil
}
