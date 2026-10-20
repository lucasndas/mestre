// Package epicdeps is the dependency between epics, and so between tables
// (docs/orquestracao/dependencia-entre-mesas.md): the Grand Master declares that
// an epic cannot go on before another (Depend), and the engine holds the table
// of the dependent epic until the pull request of the table of the other is
// merged and its commit is in the base. The hold is a wait of the Grand Master
// of the kind epic, which wakes it when it is satisfied. The dependencies live
// in the Guild database, so they survive a restart and a change of Citadel; a
// dependency that would close a ring between epics is refused with the ring
// written.
//
// The tables tell the service what happens to them (tables.Dependencies): that
// one was opened, that its pull request was merged, that it was cancelled.
package epicdeps
