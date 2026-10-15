// Package chain is the chain of approval of the Citadel
// (docs/orquestracao/cadeia-de-aprovacao.md, ADR 0024): every request that
// needs an approval climbs from the superior of who asked to the people, and
// each level decides only up to the risk the engine allows it.
//
// A Service takes a request (Open for the permissions the rules left in ask,
// OpenMissionPoint for the points of approval of a quest, Request for the
// rest), reads its risk and groups from the classification, and puts it at the
// first level of the path that can decide it: the agent superiors decide baixo;
// the Mestre da mesa, baixo and the medio of the groups of cadeia.mesa_decide
// whose conditions the engine checks; the Grão-Mestre, baixo and medio, and alto
// only with a delegation of the Owner; the people, anything but fixo. The level
// gets an `approval` item in its inbox (or, the people, a question through
// UserAsker), and the requester waits, "aguardando aprovação do <nível>".
//
// A request climbs by itself when the risk is over the ceiling, a condition or
// a guardrail stops the level, the time of the level runs out (2 min for the
// agents, 10 min for the Grão-Mestre) or the level answers "subir agora".
// Climbing is not resending: the request keeps its id, its action and its
// fingerprint. At the last level the time running out is a denial.
//
// Decide is the decision of an agent: the engine checks the level, the
// reasons, the attenuation (an agent approves only what its own effective rules
// would not deny), that nobody decides in causa própria and that the Mestre da
// mesa does not decide the points it designed, and writes every decision, even
// the refused ones, to the audit. Three denials in a row pause the character
// (the circuit breaker; Held keeps the inbox from delivering to it).
//
// The effects of an approval (the one-time exception, the temporary table rule,
// the decrees of the people), the flow of the people (UserAsker), the policy
// file and the supervision of stage 12 are small interfaces; milestone 5
// brings the Telegram side.
package chain
