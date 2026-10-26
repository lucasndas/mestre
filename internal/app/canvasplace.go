package app

import (
	"context"

	"github.com/mestre-ia/mestre/internal/orchestration/canvas"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
)

// canvasPlacer is the characters.Placer over the layout of the canvas.
type canvasPlacer struct{ st canvas.Store }

func (p canvasPlacer) Replaced(ctx context.Context, from, to characters.Character) error {
	if to.Table == "" {
		return nil
	}
	return p.st.Inherit(ctx, to.Workspace, to.Table, canvas.Key(canvas.KindCharacter, from.ID), canvas.Key(canvas.KindCharacter, to.ID))
}
