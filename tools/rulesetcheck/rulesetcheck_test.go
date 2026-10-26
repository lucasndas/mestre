package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const block = `{
  "name": "main",
  "target": "branch",
  "enforcement": "active",
  "conditions": {"ref_name": {"include": ["~DEFAULT_BRANCH"], "exclude": []}},
  "rules": [
    {"type": "deletion"},
    {"type": "non_fast_forward"},
    {"type": "pull_request", "parameters": {
      "required_approving_review_count": 0, "dismiss_stale_reviews_on_push": false,
      "allowed_merge_methods": ["squash"]}},
    {"type": "required_status_checks", "parameters": {
      "strict_required_status_checks_policy": true,
      "required_status_checks": [
        {"context": "test (ubuntu-latest)", "integration_id": 15368},
        {"context": "test (windows-latest)", "integration_id": 15368}]}}
  ]
}`

func doc(body string) []byte {
	return []byte("# Ambiente\n\nTexto.\n\n```bash\ngh api -X POST repos/o/r/rulesets --input - <<'JSON'\n" + body + "\nJSON\n```\n\nDepois.\n")
}

// serverView is the documented ruleset as the server returns it: with ids,
// links, timestamps and the settings the document leaves out, the rules and
// the checks in another order.
func serverView(t *testing.T) map[string]any {
	t.Helper()
	var live map[string]any
	if err := json.Unmarshal([]byte(block), &live); err != nil {
		t.Fatal(err)
	}
	live["id"] = 1234.0
	live["node_id"] = "RRS_abc"
	live["source_type"] = "Repository"
	live["source"] = "o/r"
	live["created_at"] = "2026-10-13T10:00:00Z"
	live["updated_at"] = "2026-10-13T10:00:00Z"
	live["current_user_can_bypass"] = "never"
	live["_links"] = map[string]any{"self": map[string]any{"href": "x"}}
	live["bypass_actors"] = []any{}
	rules := live["rules"].([]any)
	rules[0], rules[3] = rules[3], rules[0] // another order
	for _, r := range rules {
		rm := r.(map[string]any)
		switch rm["type"] {
		case "pull_request":
			p := rm["parameters"].(map[string]any)
			p["required_reviewers"] = []any{}
			p["require_extra_approval_for_unattributed_changes"] = false
			p["require_code_owner_review"] = false // a default the server fills in
		case "required_status_checks":
			p := rm["parameters"].(map[string]any)
			p["do_not_enforce_on_create"] = true
			checks := p["required_status_checks"].([]any)
			checks[0], checks[1] = checks[1], checks[0]
		}
	}
	return live
}

func compare(t *testing.T, md []byte, live any) []string {
	t.Helper()
	b, err := json.Marshal(live)
	if err != nil {
		t.Fatal(err)
	}
	diffs, err := Compare(md, b)
	if err != nil {
		t.Fatal(err)
	}
	return diffs
}

// rule finds a rule of the live view by type.
func rule(live map[string]any, typ string) map[string]any {
	for _, r := range live["rules"].([]any) {
		if r.(map[string]any)["type"] == typ {
			return r.(map[string]any)
		}
	}
	return nil
}

// CA-TESTES-13
func TestDriftFromTheDocumentedRulesetIsReported(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		mutate func(live map[string]any)
		want   []string // pieces, one per difference
	}{
		"a required check removed": {func(l map[string]any) {
			p := rule(l, "required_status_checks")["parameters"].(map[string]any)
			p["required_status_checks"] = p["required_status_checks"].([]any)[:1]
		}, []string{"rules[required_status_checks].parameters.required_status_checks[test (ubuntu-latest)]: documented, missing in the live ruleset"}},
		"an extra check": {func(l map[string]any) {
			p := rule(l, "required_status_checks")["parameters"].(map[string]any)
			p["required_status_checks"] = append(p["required_status_checks"].([]any), map[string]any{"context": "lint", "integration_id": 15368.0})
		}, []string{"required_status_checks[lint]: live, not in the documented ruleset"}},
		"another integration": {func(l map[string]any) {
			p := rule(l, "required_status_checks")["parameters"].(map[string]any)
			p["required_status_checks"].([]any)[0].(map[string]any)["integration_id"] = 1.0
		}, []string{"integration_id: documented 15368, live 1"}},
		"strict policy off": {func(l map[string]any) {
			rule(l, "required_status_checks")["parameters"].(map[string]any)["strict_required_status_checks_policy"] = false
		}, []string{"strict_required_status_checks_policy: documented true, live false"}},
		"a rule removed": {func(l map[string]any) {
			var kept []any
			for _, r := range l["rules"].([]any) {
				if r.(map[string]any)["type"] != "deletion" {
					kept = append(kept, r)
				}
			}
			l["rules"] = kept
		}, []string{"rules[deletion]: documented, missing in the live ruleset"}},
		"an extra rule": {func(l map[string]any) {
			l["rules"] = append(l["rules"].([]any), map[string]any{"type": "update"})
		}, []string{"rules[update]: live, not in the documented ruleset"}},
		"merge methods": {func(l map[string]any) {
			rule(l, "pull_request")["parameters"].(map[string]any)["allowed_merge_methods"] = []any{"squash", "merge"}
		}, []string{`allowed_merge_methods: documented ["squash"], live ["squash","merge"]`}},
		"approvals": {func(l map[string]any) {
			rule(l, "pull_request")["parameters"].(map[string]any)["required_approving_review_count"] = 1.0
		}, []string{"required_approving_review_count: documented 0, live 1"}},
		"enforcement": {func(l map[string]any) { l["enforcement"] = "evaluate" },
			[]string{`enforcement: documented "active", live "evaluate"`}},
		"another branch": {func(l map[string]any) {
			l["conditions"].(map[string]any)["ref_name"].(map[string]any)["include"] = []any{"refs/heads/dev"}
		}, []string{`conditions.ref_name.include: documented ["~DEFAULT_BRANCH"], live ["refs/heads/dev"]`}},
		"a bypass actor": {func(l map[string]any) {
			l["bypass_actors"] = []any{map[string]any{"actor_id": 5.0, "actor_type": "RepositoryRole", "bypass_mode": "always"}}
		}, []string{"bypass_actors: live"}},
		"a new setting turned on": {func(l map[string]any) {
			rule(l, "pull_request")["parameters"].(map[string]any)["some_new_setting"] = true
		}, []string{"some_new_setting: live true, not in the documented ruleset"}},
		"a documented setting missing": {func(l map[string]any) {
			delete(rule(l, "pull_request")["parameters"].(map[string]any), "allowed_merge_methods")
		}, []string{`allowed_merge_methods: documented ["squash"], missing in the live ruleset`}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			live := serverView(t)
			c.mutate(live)
			got := compare(t, doc(block), live)
			if len(got) != len(c.want) {
				t.Fatalf("differences = %q, want %d", got, len(c.want))
			}
			for i, w := range c.want {
				if !strings.Contains(got[i], w) {
					t.Fatalf("difference %d = %q, want %q", i, got[i], w)
				}
			}
		})
	}
	t.Run("not one ruleset", func(t *testing.T) {
		t.Parallel()
		for _, in := range []string{`[{"id": 1, "name": "main"}]`, `nope`} {
			if _, err := Compare(doc(block), []byte(in)); err == nil {
				t.Errorf("accepted %q", in)
			}
		}
	})
	t.Run("no command in the document", func(t *testing.T) {
		t.Parallel()
		if _, err := Compare([]byte("# Nada\n"), []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "no `gh api -X POST") {
			t.Errorf("error = %v", err)
		}
		bad := []byte("gh api -X POST repos/o/r/rulesets --input - <<'JSON'\n{not json\nJSON\n")
		if _, err := Compare(bad, []byte(`{}`)); err == nil {
			t.Error("accepted a block that is not JSON")
		}
		open := []byte("gh api -X POST repos/o/r/rulesets --input - <<'JSON'\n{}\n")
		if _, err := Compare(open, []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "never ends") {
			t.Errorf("error = %v", err)
		}
	})
}

// CA-TESTES-14
// What the server adds does not count, and the real document describes the
// checks the CI produces.
func TestServerFieldsAndOrderAreNotDrift(t *testing.T) {
	t.Parallel()
	t.Run("the same ruleset as the server returns it", func(t *testing.T) {
		t.Parallel()
		if got := compare(t, doc(block), serverView(t)); len(got) != 0 {
			t.Fatalf("differences = %q", got)
		}
	})
	t.Run("a new setting at its zero value", func(t *testing.T) {
		t.Parallel()
		live := serverView(t)
		p := rule(live, "pull_request")["parameters"].(map[string]any)
		p["some_new_setting"], p["another"], p["list"] = false, "", []any{}
		if got := compare(t, doc(block), live); len(got) != 0 {
			t.Fatalf("differences = %q", got)
		}
	})
	t.Run("a new setting that is an object switched off", func(t *testing.T) {
		t.Parallel()
		live := serverView(t)
		p := rule(live, "pull_request")["parameters"].(map[string]any)
		p["dismissal_restriction"] = map[string]any{"enabled": false, "allowed_actors": []any{}}
		if got := compare(t, doc(block), live); len(got) != 0 {
			t.Fatalf("differences = %q", got)
		}
		p["dismissal_restriction"] = map[string]any{"enabled": true, "allowed_actors": []any{}}
		if got := compare(t, doc(block), live); len(got) != 1 {
			t.Fatalf("an object switched on is drift; differences = %q", got)
		}
	})
	t.Run("the repository's document and CI", func(t *testing.T) {
		t.Parallel()
		md, err := os.ReadFile("../../docs/desenvolvimento/ambiente.md")
		if err != nil {
			t.Fatal(err)
		}
		want, err := ExtractRuleset(md)
		if err != nil {
			t.Fatal(err)
		}
		if want["name"] != "main" {
			t.Fatalf("name = %v", want["name"])
		}
		ci, err := os.ReadFile("../../.github/workflows/ci.yml")
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range []string{"test", "web", "e2e"} {
			for _, runner := range []string{"ubuntu-latest", "windows-latest"} {
				context := job + " (" + runner + ")"
				if !strings.Contains(string(md), `"context": "`+context+`"`) {
					t.Errorf("the documented ruleset does not require %q", context)
				}
				if !strings.Contains(string(ci), "name: "+job+" (${{ matrix.os }})") || !strings.Contains(string(ci), "os: [ubuntu-latest, windows-latest]") {
					t.Errorf("ci.yml does not produce the check %q", context)
				}
			}
		}
	})
}

// Part of CA-TESTES-13 (the exit codes: 1 with the differences, 2 for a JSON
// that is not a ruleset)
func TestExitCodes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	md := write("doc.md", doc(block))
	same, _ := json.Marshal(serverView(t))
	drifted := serverView(t)
	drifted["enforcement"] = "disabled"
	other, _ := json.Marshal(drifted)
	cases := map[string]struct {
		live     string
		wantCode int
		wantOut  string
	}{
		"same":          {write("same.json", same), 0, ""},
		"drift":         {write("drift.json", other), 1, `enforcement: documented "active", live "disabled"`},
		"not a file":    {filepath.Join(dir, "missing.json"), 2, ""},
		"not an object": {write("list.json", []byte("[]")), 2, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var out, errOut bytes.Buffer
			if code := run(md, c.live, &out, &errOut); code != c.wantCode || !strings.Contains(out.String(), c.wantOut) {
				t.Fatalf("code %d, out %q, err %q", code, out.String(), errOut.String())
			}
		})
	}
}
