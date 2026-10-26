package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/mestre-ia/mestre/internal/classes"
)

// doneRules gives the quests the done_requires of the approved version of the
// class of a character, from the library of its workspace. No library or no
// class: nothing is asked.
type doneRules struct {
	libs func(workspace string) *classes.Library
}

func (d doneRules) DoneRequires(ctx context.Context, workspace, table, class string) (classes.DoneRequires, error) {
	none := classes.DoneRequires{Proof: classes.ProofNone}
	if d.libs == nil || class == "" {
		return none, nil
	}
	lib := d.libs(workspace)
	if lib == nil {
		return none, nil
	}
	c, err := lib.Approved(ctx, class, table)
	if errors.Is(err, classes.ErrNoApproved) {
		return none, nil // nothing approved says nothing about what a done needs
	}
	if err != nil {
		return none, fmt.Errorf("the class %s: %w", class, err)
	}
	return c.Meta.Done(), nil
}
