package github

import "fmt"

// RulesetName is the ruleset of the plan B.
const RulesetName = "mestre-aprovacao"

// PlanBRuleset is the body of the ruleset of the plan B: only the rule of review,
// with the number of approvals the repository asks today, and the App
// "Grão-Mestre" as the only one in the list of exceptions, to merge the pull
// request (docs/github/aprovacao-no-github.md#plano-b-ruleset-com-o-grão-mestre-na-lista-de-exceção).
// Who is on the list ignores every rule of that ruleset, so no other rule goes
// in it: the other rules stay where they are, with no exception.
func PlanBRuleset(r ReviewRules, branch string, grandMasterAppID int64) map[string]any {
	return map[string]any{
		"name":        RulesetName,
		"target":      "branch",
		"enforcement": "active",
		"conditions":  map[string]any{"ref_name": map[string]any{"include": []string{"refs/heads/" + branch}, "exclude": []string{}}},
		"bypass_actors": []map[string]any{
			{"actor_id": grandMasterAppID, "actor_type": "Integration", "bypass_mode": "pull_request"},
		},
		"rules": []map[string]any{{
			"type": "pull_request",
			"parameters": map[string]any{
				"required_approving_review_count":   max(r.Approvals, 1),
				"dismiss_stale_reviews_on_push":     r.DismissStale,
				"require_code_owner_review":         r.CodeOwners,
				"require_last_push_approval":        r.LastPush,
				"required_review_thread_resolution": false,
			},
		}},
	}
}

// PlanBChange describes the whole change of the plan B for the Owner: the new
// ruleset and, where the review is asked today, what to take out of there, so
// the review is not asked twice.
func PlanBChange(r ReviewRules) string {
	text := fmt.Sprintf("Criar o ruleset %q na branch principal com só a regra de %d aprovação(ões), e o App Grão-Mestre na lista de exceção.", RulesetName, max(r.Approvals, 1))
	if r.Classic {
		text += " Tirar a exigência de revisão da proteção clássica da branch."
	}
	for _, rs := range r.Rulesets {
		if rs.Shared {
			text += fmt.Sprintf(" Tirar a regra de revisão do ruleset #%d (ele tem outras regras, que ficam).", rs.ID)
		} else {
			text += fmt.Sprintf(" Apagar o ruleset #%d, que só pede a revisão.", rs.ID)
		}
	}
	return text
}
