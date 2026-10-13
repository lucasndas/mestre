package localchan

// DefaultAccess on Windows leaves the ACL of the socket to internal/winsvc
// (owner NT SERVICE\Mestre, mestre-agente read and write, SYSTEM), which
// replaces this when the service is installed; until then the socket has
// the default ACL of its folder.
func DefaultAccess() Access { return NoAccess{} }
