package chain

import (
	"context"
	"maps"
	"slices"

	"github.com/lucasndas/mestre/internal/classes"
)

// PolicyAdjust is what the active decrees of policy (docs/aprovacoes/politica.md#decretos-ajustam-a-política)
// change for a request: the settings they loosen (an area of the sensitive ones,
// or a type of risk medio) and the ones they tighten (a type, or "*" for all).
type PolicyAdjust struct {
	Loosen, Tighten []string
}

// PolicyDecrees gives the decrees of policy that reach a request: those of its
// workspace, of its table and of its class, still valid. Without it only the
// policy file rules.
type PolicyDecrees interface {
	PolicyFor(ctx context.Context, r Request) (PolicyAdjust, error)
}

// neverLoosened are the areas no decree opens, whatever it says: the engine's
// own and the secrets (the protections of docs/aprovacoes/politica.md).
var neverLoosened = []string{"mestre", "permissoes_e_segredos"}

// WithDecrees returns the rules with the decrees applied, leaving the original
// untouched. A tightening always wins over a loosening of the same setting; a
// setting the engine does not know is ignored, which is the stricter side.
func (r PolicyRules) WithDecrees(a PolicyAdjust) PolicyRules {
	if len(a.Loosen) == 0 && len(a.Tighten) == 0 {
		return r
	}
	r.Areas = maps.Clone(r.Areas)
	r.Alone = maps.Clone(r.Alone)
	r.AskAlways = maps.Clone(r.AskAlways)
	if r.AskAlways == nil {
		r.AskAlways = map[classes.RequestType]bool{}
	}
	tightened := func(s string) bool {
		return slices.Contains(a.Tighten, s) || slices.Contains(a.Tighten, "*") || slices.Contains(a.Tighten, "tudo")
	}
	for _, s := range a.Loosen {
		if tightened(s) || slices.Contains(neverLoosened, s) {
			continue
		}
		if _, ok := r.Areas[s]; ok {
			delete(r.Areas, s)
			continue
		}
		t := classes.RequestType(s)
		if slices.Contains(highTypes, t) || slices.Contains(fixedTypes, t) {
			continue // the decree of policy never opens an alto or a fixed type
		}
		if _, known := r.Alone[t]; known {
			r.Alone[t] = true
		}
	}
	for _, s := range a.Tighten {
		switch s {
		case "*", "tudo":
			r.AskAll = true
		default:
			r.AskAlways[classes.RequestType(s)] = true
		}
	}
	return r
}

// rulesFor is the rules that hold for a request: the policy file of its
// workspace with the decrees of policy that reach it.
func (s *Service) rulesFor(ctx context.Context, r Request) PolicyRules {
	rules, _ := s.rulesOf(r.Workspace)
	if s.o.PolicyDecrees == nil {
		return rules
	}
	a, err := s.o.PolicyDecrees.PolicyFor(ctx, r)
	if err != nil {
		s.log.Warn("chain: the decrees of policy were not read", "request", r.ID, "err", err)
		// the stricter side: ask the people
		rules.AskAll = true
		return rules
	}
	return rules.WithDecrees(a)
}
