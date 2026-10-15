// Package tablerules keeps the temporary table rules
// (docs/orquestracao/regras-temporarias-de-mesa.md): what an agent of the
// approval chain makes by approving an action for a time or a pattern. It is
// the chain.TempRules that creates them, the source of the decrees of kind
// table_rule that enter the effective rules of the characters of the table
// (Service.Decrees), and the classes.TableRuleGate the engine asks at each use
// (Service.Use), which checks the rule again, the conditions of the table for
// the medio of the Mestre da mesa, and records what the rule let through.
//
// A rule goes active, then expired (the validity, the end of the mission, of
// the table, of the author's place), revoked (by the author, by the Grão-Mestre,
// by an Administrator) or suspended (the lock of the system, resumed when it
// ends). Sweep applies the endings; the gate refuses a rule that should have
// ended even before the sweep runs.
package tablerules
