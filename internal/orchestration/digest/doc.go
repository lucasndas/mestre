// Package digest is the periodic summary of what the agents of the chain of
// approval decided, the rule suggestions made of approvals that repeat, and the
// sample of approved actions the people review later
// (docs/aprovacoes/resumo-e-sugestoes.md).
//
// The summary list goes to the inbox of the Grão-Mestre, who writes it short and
// sends it; the suggestions (only to the Owner) and the sample (to everyone the
// policy names) are made by the engine and go straight to the people, so the
// agent chooses nothing of what reviews its own decisions. The clock and the
// random source are injected, and the text of everything passes through the
// masker of secrets.
package digest
