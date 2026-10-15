package tables

// BundleSources is bundleSources, for the tests.
var BundleSources = bundleSources

// LockTable takes the lock of a table, as the operations on it do.
func (s *Service) LockTable(workspace, table string) func() { return s.lock(workspace, table) }
