package classes_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/providers"
)

func TestParseThePermissionsOfTheFormatDocument(t *testing.T) {
	t.Parallel()
	p := parse(t, qaYAML+`cli:
  allow: ["issue comment"]
operator: false
`)
	if p.Files == nil || len(*p.Files.Write) != 2 || *p.Commands.Default != classes.Ask || !*p.Git.Commit || *p.Tools.Default != classes.Ask {
		t.Fatalf("parsed %+v", p)
	}
}

func TestInvalidPermissionsAreRefused(t *testing.T) {
	t.Parallel()
	for name, yaml := range map[string]string{
		"empty":               "",
		"no version":          "files: {}\n",
		"version 2":           "version: 2\n",
		"unknown field":       "version: 1\nfile: {}\n",
		"unknown nested":      "version: 1\nfiles: {writes: []}\n",
		"two documents":       "version: 1\n---\nversion: 1\n",
		"bad default":         "version: 1\ncommands: {default: maybe}\n",
		"bad tools default":   "version: 1\ntools: {default: sometimes}\n",
		"bad workflows":       "version: 1\ngit: {workflows: always}\n",
		"absolute write":      "version: 1\nfiles: {write: [\"/etc/**\"]}\n",
		"dot dot":             "version: 1\nfiles: {read: [\"../x/**\"]}\n",
		"bad glob":            "version: 1\nfiles: {deny: [\"[\"]}\n",
		"placeholder in file": "version: 1\nfiles: {write: [\"{mesa.branch}/**\"]}\n",
		"unknown placeholder": "version: 1\ngit: {branches: [\"{mesa.nome}\"]}\n",
		"bad domain":          "version: 1\nnetwork: {domains: [\"https://github.com\"]}\n",
		"empty command":       "version: 1\ncommands: {allow: [\" \"]}\n",
		"bad cli":             "version: 1\ncli: {allow: [\"Issue; rm\"]}\n",
		"wrong type":          "version: 1\noperator: maybe\n",
	} {
		if _, err := classes.ParsePermissions([]byte(yaml)); !errors.Is(err, classes.ErrInvalid) {
			t.Errorf("%s: err = %v; want ErrInvalid", name, err)
		}
	}
	// Absolute paths are allowed where the format says.
	parse(t, "version: 1\nfiles: {read: [\"/usr/share/dict/**\"], deny: [\"/etc/**\"]}\n")
}

// CA-PERM-18
func TestTheToolsDefaultLivesInTheToolsBlock(t *testing.T) {
	t.Parallel()
	_, err := classes.ParsePermissions([]byte("version: 1\ndefault: ask\n"))
	if !errors.Is(err, classes.ErrInvalid) || !strings.Contains(err.Error(), "field default not found") {
		t.Fatalf("default at the top: err = %v; want invalid by an unknown field", err)
	}
	p := parse(t, "version: 1\ntools: {default: deny}\n")
	if p.Tools == nil || p.Tools.Default == nil || *p.Tools.Default != classes.Deny {
		t.Fatalf("tools %+v; want default deny", p.Tools)
	}
	m := newMachine()
	r := rules(t, "version: 1\ntools: {default: deny}\n")
	want(t, decide(m.env(), r, toolEvent(providers.KindOther, "generate_image")), classes.Deny, classes.LayerClass, "generate_image", "negar")
}

type vault map[string][2][]string

func (v vault) SiteCredential(name string) ([]string, []string, bool) {
	c, ok := v[name]
	return c[0], c[1], ok
}

func TestCredentialsMustExistListTheClassAndBeReachable(t *testing.T) {
	t.Parallel()
	v := vault{
		"jira":  {{"jira.example.com"}, {"qa"}},
		"admin": {{"admin.example.com"}, {"dev"}},
	}
	ok := parse(t, "version: 1\nnetwork: {internet: true, domains: [\"jira.example.com\"], credentials: [\"jira\"]}\n")
	if err := ok.CheckCredentials("qa", v); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct{ yaml, class string }{
		"missing":       {"version: 1\nnetwork: {internet: true, credentials: [\"nope\"]}\n", "qa"},
		"other class":   {"version: 1\nnetwork: {internet: true, credentials: [\"admin\"]}\n", "qa"},
		"domain absent": {"version: 1\nnetwork: {internet: true, domains: [\"github.com\"], credentials: [\"jira\"]}\n", "qa"},
		"no internet":   {"version: 1\nnetwork: {internet: false, credentials: [\"jira\"]}\n", "qa"},
	} {
		err := parse(t, c.yaml).CheckCredentials(c.class, v)
		if !errors.Is(err, classes.ErrInvalid) {
			t.Errorf("%s: err = %v; want ErrInvalid", name, err)
		}
	}
	// An absent internet is on, any domain (ADR 0025): the credential is reachable.
	if err := parse(t, "version: 1\nnetwork: {credentials: [\"jira\"]}\n").CheckCredentials("qa", v); err != nil {
		t.Fatalf("absent internet: %v", err)
	}
	if err := ok.CheckCredentials("qa", nil); err == nil || !strings.Contains(err.Error(), "vault") {
		t.Fatalf("without the vault: %v", err)
	}
}
