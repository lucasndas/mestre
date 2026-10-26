package app

import (
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// NewCanvasHostForTest is the canvas of the panel over an orchestration made by a test.
func NewCanvasHostForTest(orc *Orchestration, clk clock.Clock) panel.CanvasSource {
	return canvasHost{get: func() *Orchestration { return orc }, clk: clk}
}

// NewViewHostForTest is the workspace view of the panel over an orchestration made by a test.
func NewViewHostForTest(orc *Orchestration, clk clock.Clock) panel.ViewSource {
	return viewHost{get: func() *Orchestration { return orc }, clk: clk}
}
