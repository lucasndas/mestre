package classes

import (
	"context"

	"github.com/lucasndas/mestre/internal/providers"
)

// MaxAnalysisWork is the bound of the units one decision may spend.
const MaxAnalysisWork = maxAnalysisWork

// DecideWork is Decide that also reports the units the analysis spent: the
// tests of the bound count work, which does not change with the load of the
// machine, instead of timing it.
func DecideWork(ctx context.Context, env Env, rules *Rules, ev providers.Event) (Decision, int) {
	var work int
	d := decideCounting(ctx, env, rules, ev, false, &work)
	return d, work
}
