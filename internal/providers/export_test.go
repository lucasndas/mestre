package providers

// KnownScreens is the list of known screens of an adapter, for the tests.
func KnownScreens(a Adapter) []KnownScreen {
	switch a := a.(type) {
	case *claudeCode:
		return a.screens.known
	case *codex:
		return a.screens.known
	case *antigravity:
		return a.screens.known
	}
	return nil
}
