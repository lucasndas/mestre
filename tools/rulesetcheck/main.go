// Command rulesetcheck compares the live ruleset of main with the one written
// in docs/desenvolvimento/ambiente.md, the single source
// (docs/desenvolvimento/ambiente.md#ci-e-proteção-da-main). The nightly workflow
// feeds it the JSON of `gh api repos/<repo>/rulesets/<id>`:
//
//	go run ./tools/rulesetcheck [-doc ambiente.md] -live live.json
//
// Exit codes: 0 same ruleset, 1 drift (one difference per line on stdout),
// 2 could not compare.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

const defaultDoc = "docs/desenvolvimento/ambiente.md"

func main() {
	doc := flag.String("doc", defaultDoc, "document with the `gh api -X POST …/rulesets` block")
	live := flag.String("live", "", "JSON of the live ruleset of main (gh api repos/<repo>/rulesets/<id>)")
	flag.Parse()
	os.Exit(run(*doc, *live, os.Stdout, os.Stderr))
}

func run(docPath, livePath string, out, errOut io.Writer) int {
	md, err := os.ReadFile(docPath)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	liveJSON, err := os.ReadFile(livePath)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	diffs, err := Compare(md, liveJSON)
	if err != nil {
		fmt.Fprintln(errOut, "rulesetcheck:", err)
		return 2
	}
	for _, d := range diffs {
		fmt.Fprintln(out, d)
	}
	if len(diffs) > 0 {
		return 1
	}
	return 0
}

// Compare returns the differences between the ruleset documented in the
// markdown and the live one.
func Compare(md, liveJSON []byte) ([]string, error) {
	want, err := ExtractRuleset(md)
	if err != nil {
		return nil, err
	}
	var got any
	if err := json.Unmarshal(liveJSON, &got); err != nil {
		return nil, fmt.Errorf("the live JSON does not parse: %w", err)
	}
	if _, ok := got.(map[string]any); !ok {
		return nil, errors.New("the live JSON is not one ruleset object (use gh api repos/<repo>/rulesets/<id>)")
	}
	var out []string
	diff("", want, got, &out)
	return out, nil
}

var heredocRe = regexp.MustCompile(`<<\s*'?"?([A-Za-z_]+)"?'?\s*$`)

// ExtractRuleset reads the JSON that follows `gh api -X POST …/rulesets` in
// the markdown: the here-document of the command.
func ExtractRuleset(md []byte) (map[string]any, error) {
	lines := strings.Split(string(md), "\n")
	for i, l := range lines {
		if !strings.Contains(l, "gh api -X POST") || !strings.Contains(l, "/rulesets") {
			continue
		}
		m := heredocRe.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			return nil, fmt.Errorf("line %d: no here-document after the gh api command", i+1)
		}
		var body []string
		for _, b := range lines[i+1:] {
			if strings.TrimSpace(b) == m[1] {
				var v map[string]any
				if err := json.Unmarshal([]byte(strings.Join(body, "\n")), &v); err != nil {
					return nil, fmt.Errorf("the documented ruleset is not JSON: %w", err)
				}
				return v, nil
			}
			body = append(body, b)
		}
		return nil, fmt.Errorf("line %d: the here-document %s never ends", i+1, m[1])
	}
	return nil, errors.New("no `gh api -X POST …/rulesets` command in the document")
}

// ignored are the fields the server adds or defaults and the document leaves
// out: identifiers, links, timestamps and the settings of features the
// project does not use.
var ignored = map[string]bool{
	"id": true, "node_id": true, "_links": true, "created_at": true, "updated_at": true,
	"source": true, "source_type": true, "current_user_can_bypass": true,
	"do_not_enforce_on_create": true, "required_reviewers": true,
	"require_extra_approval_for_unattributed_changes": true,
}

// keyOf names an element of a list that is compared by identity, not by
// position: a rule by its type, a required check by its context.
func keyOf(v any) (string, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return "", false
	}
	for _, k := range []string{"type", "context"} {
		if s, ok := m[k].(string); ok {
			return s, true
		}
	}
	return "", false
}

// isZero says whether a value the document leaves out is at its zero value: an
// object counts as zero when every field of it is (a setting the server adds
// switched off, like {"enabled": false, "allowed_actors": []}).
func isZero(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case bool:
		return !x
	case float64:
		return x == 0
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		for _, f := range x {
			if !isZero(f) {
				return false
			}
		}
		return true
	}
	return false
}

func show(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// diff appends the differences of got from want. Everything the document
// says must be as documented; what the server added is drift unless it is
// ignored or empty.
func diff(path string, want, got any, out *[]string) {
	at := path
	if at == "" {
		at = "ruleset"
	}
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: documented %s, live %s", at, show(want), show(got)))
			return
		}
		for _, k := range sortedKeys(w) {
			if ignored[k] {
				continue
			}
			gv, present := g[k]
			if !present {
				*out = append(*out, fmt.Sprintf("%s: documented %s, missing in the live ruleset", join(path, k), show(w[k])))
				continue
			}
			diff(join(path, k), w[k], gv, out)
		}
		for _, k := range sortedKeys(g) {
			if _, documented := w[k]; documented || ignored[k] || isZero(g[k]) {
				continue
			}
			*out = append(*out, fmt.Sprintf("%s: live %s, not in the documented ruleset", join(path, k), show(g[k])))
		}
	case []any:
		g, ok := got.([]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: documented %s, live %s", at, show(want), show(got)))
			return
		}
		diffList(path, w, g, out)
	default:
		if !reflect.DeepEqual(want, got) {
			*out = append(*out, fmt.Sprintf("%s: documented %s, live %s", at, show(want), show(got)))
		}
	}
}

func diffList(path string, want, got []any, out *[]string) {
	byKey := func(l []any) (map[string]any, bool) {
		m := map[string]any{}
		for _, e := range l {
			k, ok := keyOf(e)
			if !ok {
				return nil, false
			}
			m[k] = e
		}
		return m, true
	}
	wm, wok := byKey(want)
	gm, gok := byKey(got)
	if !wok || !gok {
		if !reflect.DeepEqual(want, got) {
			*out = append(*out, fmt.Sprintf("%s: documented %s, live %s", path, show(want), show(got)))
		}
		return
	}
	for _, k := range sortedKeys(wm) {
		g, present := gm[k]
		if !present {
			*out = append(*out, fmt.Sprintf("%s[%s]: documented, missing in the live ruleset", path, k))
			continue
		}
		diff(fmt.Sprintf("%s[%s]", path, k), wm[k], g, out)
	}
	for _, k := range sortedKeys(gm) {
		if _, documented := wm[k]; !documented {
			*out = append(*out, fmt.Sprintf("%s[%s]: live, not in the documented ruleset", path, k))
		}
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
