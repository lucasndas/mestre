// Package decrees keeps the decrees (docs/telegram/decretos.md): the orders of
// the authorized users that stand above the rules. The Grand Master only
// proposes (Propose); nothing is a decree before the click of the author
// (Confirm), with the PIN when the effect loosens the security. The service
// checks the level and the workspaces of the author against what is stored,
// refuses before the confirmation what the engine can not do (the invariants,
// the class `orquestrador`), numbers the decree, detects the conflicts, ends
// the ones whose validity is over and revokes by level.
//
// It is the chain.Grants of the engine (the exceptions and the decrees that the
// answers of the people make), the chain.Delegations (the decrees of
// delegation of the Owner), the classes.ExceptionLedger of the exceptions, and
// the source of the decrees that enter the effective rules of a character
// (Decrees) and of the notes that reach its Mestre layer (Notes).
package decrees
