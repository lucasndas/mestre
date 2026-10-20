package quests_test

// stoppedNames lists the names of the characters the machines were told to end.
func (w *world) stoppedNames() []string {
	w.t.Helper()
	var out []string
	for _, id := range w.machines.StoppedIDs() {
		out = append(out, w.get(id).Name)
	}
	return out
}
