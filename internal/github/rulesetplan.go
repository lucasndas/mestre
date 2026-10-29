package github

import "github.com/mestre-ia/mestre/internal/i18n"

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
				"require_code_owner_review":         false, // a rule the exception skips is no rule: plan B is refused when the repository asks it
				"require_last_push_approval":        false,
				"required_review_thread_resolution": false,
			},
		}},
	}
}

// PlanBChange describes the whole change of the plan B for the Owner: the new
// ruleset and, where the review is asked today, what to take out of there, so
// the review is not asked twice. It is a message, for each reader to translate.
func PlanBChange(r ReviewRules) i18n.Msg {
	parts := []i18n.Msg{i18n.M("approval.plan_b_create", "ruleset", RulesetName, "count", max(r.Approvals, 1))}
	if r.Classic {
		parts = append(parts, i18n.M("approval.plan_b_classic"))
	}
	for _, rs := range r.Rulesets {
		if rs.Shared {
			parts = append(parts, i18n.M("approval.plan_b_ruleset_shared", "id", rs.ID))
		} else {
			parts = append(parts, i18n.M("approval.plan_b_ruleset_delete", "id", rs.ID))
		}
	}
	return i18n.Join(i18n.Text(" "), parts...)
}
