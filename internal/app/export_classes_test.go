package app

import "github.com/mestre-ia/mestre/internal/panel"

// NewClassPanelForTest is the classes screen of the panel over an orchestration made by a test.
func NewClassPanelForTest(orc *Orchestration) panel.ClassSource {
	return classPanel{orc: orc, db: orc.Registry.DB}
}
