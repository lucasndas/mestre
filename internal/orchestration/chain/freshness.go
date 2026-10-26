package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mestre-ia/mestre/internal/classes"
)

// Freshness is the gate of freshness of the requests of a PR
// (docs/github/acoes-por-nivel.md#merge): when the repository does not ask the
// branch to be up to date, the CI of the head says nothing of the merge, so
// the engine proves the base integrated before the request is asked of anyone.
// Nil: no gate (the repository is read by the people alone).
type Freshness interface {
	// Open runs before a request of type pr or merge is stored. An error
	// stops the request: a gate that could not be checked never opens it.
	Open(ctx context.Context, r Request) (FreshnessVerdict, error)
	// Unverifiable is why the Grão-Mestre may not decide the request alone
	// ("" when nothing stops him): a workspace with no verification command
	// confirmed. An error is a decision for a person.
	Unverifiable(ctx context.Context, r Request) (string, error)
	// BaseTip is the tip of the base of the table of the request now: a gate
	// that ran on another tip while the request was being stored is ended.
	BaseTip(ctx context.Context, r Request) (string, error)
}

// FreshnessVerdict is what the gate found when a request opens.
type FreshnessVerdict struct {
	// BaseSHA is the tip of the base the gate ran on; empty when it did not
	// run (the repository asks the branch to be up to date, or there is no
	// command to run). The target of the request carries it.
	BaseSHA string
	// Refused says the verification failed: no request is made. Reason is what
	// the Grão-Mestre reads (the command, the end of the output and what the
	// table was told).
	Refused bool
	Reason  string
}

// FreshnessError is the answer of Request to a gate that refused the request.
type FreshnessError struct{ Reason string }

func (e *FreshnessError) Error() string {
	return "chain: the gate of freshness refused the request: " + e.Reason
}

// baseSHAKey is the field of the target of a pr or merge request that holds the
// tip of the base the gate ran on.
const baseSHAKey = "base_sha"

// WithBaseSHA is the target with the tip of the base the gate ran on.
func WithBaseSHA(target json.RawMessage, sha string) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(target, &m); err != nil || m == nil {
		return nil, fmt.Errorf("chain: the target of a PR is not an object: %v", err)
	}
	b, _ := json.Marshal(sha)
	m[baseSHAKey] = b
	return json.Marshal(m)
}

// BaseSHAOf is the tip of the base in the target ("" when the gate did not run).
func BaseSHAOf(target json.RawMessage) string {
	var v struct {
		Base string `json:"base_sha"`
	}
	if json.Unmarshal(target, &v) != nil {
		return ""
	}
	return v.Base
}

func isPR(t classes.RequestType) bool { return t == classes.RequestPR || t == classes.RequestMerge }

// gateOpen applies the gate to a request about to be stored.
func (s *Service) gateOpen(ctx context.Context, r *Request) error {
	if s.o.Freshness == nil || !isPR(r.Type) || r.PR == nil {
		return nil
	}
	v, err := s.o.Freshness.Open(ctx, *r)
	if err != nil {
		return fmt.Errorf("chain: the gate of freshness could not be checked: %w", err)
	}
	if v.Refused {
		return &FreshnessError{Reason: v.Reason}
	}
	if v.BaseSHA == "" || len(r.Target) == 0 {
		return nil
	}
	t, err := WithBaseSHA(r.Target, v.BaseSHA)
	if err != nil {
		return err
	}
	imp, err := Impress(t)
	if err != nil {
		return err
	}
	r.Target, r.Impression = t, imp
	return nil
}

// BaseMoved is told that the tip of the base of a table is now tip: an open
// request of a PR whose gate ran on another tip is invalidated, so the gate
// runs again on the request that replaces it. A request whose
// target has no base_sha (the repository asks the branch to be up to date) is
// not touched.
func (s *Service) BaseMoved(ctx context.Context, workspace, table, tip string) error {
	open, err := s.st.OpenOfTable(ctx, workspace, table)
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range open {
		if !isPR(r.Type) {
			continue
		}
		if sha := BaseSHAOf(r.Target); sha == "" || sha == tip {
			continue
		}
		// a request that closed meanwhile is not an error of the move
		var ref *Refusal
		err := s.Invalidate(ctx, r.ID, "a branch principal andou depois do gate de frescor")
		if err != nil && (!errors.As(err, &ref) || ref.Code != CodeNoRequest) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// gateRecheck ends a request just stored whose gate ran on a tip of the base that
// moved while the gate ran: the fetch that moved it came before the request
// existed, so BaseMoved did not see it.
func (s *Service) gateRecheck(ctx context.Context, r Request) error {
	if s.o.Freshness == nil || !isPR(r.Type) {
		return nil
	}
	gated := BaseSHAOf(r.Target)
	if gated == "" {
		return nil
	}
	tip, err := s.o.Freshness.BaseTip(ctx, r)
	if err == nil && tip == gated {
		return nil
	}
	why := "a branch principal andou enquanto o gate de frescor rodava"
	if err != nil {
		why = "a ponta da base não pôde ser conferida depois do gate de frescor"
	}
	if ierr := s.Invalidate(ctx, r.ID, why); ierr != nil {
		s.log.Warn("chain: a request of a stale gate was not invalidated", "request", r.ID, "err", ierr)
	}
	return &FreshnessError{Reason: why + ": peça de novo"}
}
