package chain_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

type policyRig struct {
	t      *testing.T
	path   string
	file   *chain.PolicyFile
	alerts []error
}

func newPolicyRig(t *testing.T) *policyRig {
	t.Helper()
	r := &policyRig{t: t, path: filepath.Join(t.TempDir(), "politica-de-aprovacao.yaml")}
	r.file = &chain.PolicyFile{
		Path:  func(ws string) string { return map[string]string{"meu-app": r.path}[ws] },
		Alert: func(_ string, err error) { r.alerts = append(r.alerts, err) },
	}
	return r
}

func (r *policyRig) write(content string) {
	r.t.Helper()
	if err := os.WriteFile(r.path, []byte(content), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

func (r *policyRig) chain() (chain.Settings, string) {
	r.t.Helper()
	st, v, err := r.file.Chain(context.Background(), "meu-app")
	if err != nil {
		r.t.Fatal(err)
	}
	return st, v
}

// CA-APROV-POL-21
func TestThePolicyFileGivesTheDeadlinesTheSectionAndTheRiskOfTheOwner(t *testing.T) {
	t.Parallel()
	r := newPolicyRig(t)
	if st, v := r.chain(); v != "default" || st.PeopleDeadline != 24*time.Hour {
		t.Fatalf("no file yet: %+v %q; want the default content", st, v)
	}
	if len(r.alerts) != 0 {
		t.Fatalf("a missing file raised %v", r.alerts)
	}

	// the default content of the session zero holds as it is
	r.write(sessionzero.DefaultPolicyYAML)
	def, v := r.chain()
	if !strings.HasPrefix(v, "sha256:") || def.PeopleDeadline != 24*time.Hour || def.PeopleDeadlines[classes.RequestClassPermission] != 2*time.Hour {
		t.Fatalf("default content: %+v %q", def, v)
	}

	// what the Owner changes
	r.write(`versao: 1
expiracao: 6h
tipos:
  pr: { sozinho: se_condicoes, nivel_minimo: aprovador, expiracao: 90m }
  custo_extra: { sozinho: se_condicoes, nivel_minimo: aprovador, expiracao: 3h }
cadeia:
  prazo_agente: 3m
  mesa_decide: [rede]
areas_sensiveis:
  mestre: ["docs/interno/**"]
  permissoes_e_segredos: ["**/*.pem"]
risco:
  ajustes:
    - { comando: "make deploy-teste", risco: alto }
`)
	st, v2 := r.chain()
	if v2 == v || st.PeopleDeadline != 6*time.Hour || st.PeopleDeadlines[classes.RequestPR] != 90*time.Minute ||
		st.PeopleDeadlines[classes.RequestExtraCost] != 3*time.Hour || st.PeopleDeadlines[classes.RequestClassPermission] != 2*time.Hour ||
		st.AgentDeadline != 3*time.Minute || !slices.Equal(st.MesaDecides, []string{"rede"}) {
		t.Fatalf("settings = %+v (version %q)", st, v2)
	}
	risk := r.file.Risk("meu-app")
	if risk == nil || risk.Adjustments == nil || len(risk.Adjustments.Items) != 1 || !slices.Equal(risk.Mestre, []string{"docs/interno/**"}) || !slices.Equal(risk.Secrets, []string{"**/*.pem"}) {
		t.Fatalf("risk policy = %+v", risk)
	}
	// the version is the hash of the content: stable for the same content, another for another
	r.write("versao: 1\nexpiracao: 6h\n")
	_, vOther := r.chain()
	r.write("versao: 1\nexpiracao: 6h\n")
	if _, vAgain := r.chain(); vAgain != vOther || vOther == v2 || !strings.HasPrefix(vOther, "sha256:") {
		t.Fatalf("versions %q, %q, %q; want a stable sha256 per content", v2, vOther, vAgain)
	}
	// a workspace without a folder has the default content and the default table
	if st, v, _ := r.file.Chain(context.Background(), "outro"); v != "default" || st.PeopleDeadline != 24*time.Hour || r.file.Risk("outro") != nil {
		t.Fatalf("another workspace: %+v %q", st, v)
	}
}

// CA-APROV-POL-21 (the invalid file: the last valid version holds, nothing loosens, the Owner is told once)
func TestAnInvalidPolicyFileNeverLoosensWhatHeldBeforeAndTheOwnerIsToldOnce(t *testing.T) {
	t.Parallel()
	r := newPolicyRig(t)
	good := "versao: 1\nexpiracao: 6h\ncadeia:\n  prazo_agente: 3m\n"
	r.write(good)
	st, v := r.chain()
	if st.PeopleDeadline != 6*time.Hour {
		t.Fatalf("good file: %+v", st)
	}
	for name, bad := range map[string]string{
		"unknown section":     "versao: 1\ncadiea: {}\n",
		"no version":          "expiracao: 6h\n",
		"bad duration":        "versao: 1\nexpiracao: depressa\n",
		"zero duration":       "versao: 1\nexpiracao: 0s\n",
		"unknown type":        "versao: 1\ntipos:\n  inventado: { expiracao: 1h }\n",
		"a fixed type":        "versao: 1\ntipos:\n  promocao_cidadela: { expiracao: 1h }\n",
		"bad type deadline":   "versao: 1\ntipos:\n  pr: { expiracao: 0 }\n",
		"bad cadeia":          "versao: 1\ncadeia:\n  mesa_decide: [alto]\n",
		"bad risk adjustment": "versao: 1\nrisco:\n  ajustes:\n    - { comando: x, risco: enorme }\n",
		"two documents":       "versao: 1\n---\nversao: 1\n",
		"not yaml":            "versao: [\n",
		"area not a list":     "versao: 1\nareas_sensiveis:\n  mestre: nada\n",
		"empty area":          "versao: 1\nareas_sensiveis:\n  permissoes_e_segredos: []\n",
		"bad glob":            "versao: 1\nareas_sensiveis:\n  mestre: [\"[x\"]\n",
		"empty glob":          "versao: 1\nareas_sensiveis:\n  mestre: [\"\"]\n",
		"typo in cadeia":      "versao: 1\ncadeia:\n  mesa_decid: [rede]\n",
		"high type alone":     "versao: 1\ntipos:\n  acao_destrutiva: { sozinho: se_condicoes }\n",
		"bad sozinho":         "versao: 1\ntipos:\n  pr: { sozinho: as_vezes }\n",
		"bad minimum level":   "versao: 1\ntipos:\n  pr: { nivel_minimo: todos }\n",
	} {
		n := len(r.alerts)
		r.write(bad)
		got, gotV := r.chain()
		if gotV != v || got.PeopleDeadline != 6*time.Hour || got.AgentDeadline != 3*time.Minute {
			t.Errorf("%s: %+v %q; want the last valid version to hold", name, got, gotV)
		}
		r.chain() // read again: the same content is told once
		if len(r.alerts) != n+1 || !errors.Is(r.alerts[len(r.alerts)-1], chain.ErrInvalidPolicy) {
			t.Errorf("%s: alerts %v; want one more, an invalid policy", name, r.alerts[n:])
		}
	}
	// fixed: it holds again, and a later failure is told again
	r.write("versao: 1\nexpiracao: 8h\n")
	if got, _ := r.chain(); got.PeopleDeadline != 8*time.Hour {
		t.Fatalf("after the fix: %+v", got)
	}
	n := len(r.alerts)
	r.write(good + "x: 1\n")
	r.chain()
	if len(r.alerts) != n+1 {
		t.Fatalf("a new failure after a fix was not told: %v", r.alerts[n:])
	}
}

// CA-APROV-POL-21 (unreadable or a link: the default content holds and the Owner is told)
func TestAnInvalidFirstPolicyFileHoldsTheDefaultAndALinkIsNotFollowed(t *testing.T) {
	t.Parallel()
	r := newPolicyRig(t)
	r.write("versao: 1\nexpiracao: depressa\n")
	if st, v := r.chain(); v != "default" || st.PeopleDeadline != 24*time.Hour {
		t.Fatalf("invalid before any valid: %+v %q; want the default content", st, v)
	}
	if len(r.alerts) != 1 {
		t.Fatalf("alerts %v", r.alerts)
	}

	// the folder is reachable by the agents: a link to another file is not read
	target := filepath.Join(t.TempDir(), "outra.yaml")
	if err := os.WriteFile(target, []byte("versao: 1\nexpiracao: 1h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(r.path); err != nil {
		t.Fatal(err)
	}
	before := len(r.alerts)
	if err := os.Symlink(target, r.path); err == nil {
		if st, _ := r.chain(); st.PeopleDeadline != 24*time.Hour {
			t.Fatalf("the engine followed a link: %+v", st)
		}
		if len(r.alerts) != before+1 {
			t.Fatalf("alerts %v; want the link told", r.alerts)
		}
	} else if runtime.GOOS != "windows" {
		t.Fatalf("no symbolic link on %s: %v", runtime.GOOS, err)
	}
	// a folder on the way that is a link is not followed either; where a symbolic
	// link takes a privilege (Windows), the junction of policyfile_full_test.go
	// proves it
	linkedFolderIsNotRead(t, func(target, link string) error { return os.Symlink(target, link) })
}

// linkedFolderIsNotRead puts the policy file behind a folder that is a link (made
// by link) and checks that the engine keeps the default and tells it.
func linkedFolderIsNotRead(t *testing.T, link func(target, link string) error) {
	t.Helper()
	r := newPolicyRig(t)
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "politica-de-aprovacao.yaml"), []byte("versao: 1\nexpiracao: 1h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(t.TempDir(), "pasta")
	if err := link(elsewhere, folder); err != nil {
		if runtime.GOOS == "windows" {
			return // a symbolic link takes a privilege there: the full suite makes a junction
		}
		t.Fatal(err)
	}
	r.path = filepath.Join(folder, "politica-de-aprovacao.yaml")
	if st, _ := r.chain(); st.PeopleDeadline != 24*time.Hour {
		t.Fatalf("the engine went through a linked folder: %+v", st)
	}
	if len(r.alerts) != 1 {
		t.Fatalf("alerts %v; want the linked folder told", r.alerts)
	}
}

func TestTheAlertMayComeBackAndTheSnapshotIsTheCallersOwn(t *testing.T) {
	t.Parallel()
	r := newPolicyRig(t)
	r.file.Alert = func(string, error) { r.file.Snapshot("meu-app") } // would wait for itself under the lock
	r.write("versao: 1\nexpiracao: 6h\n")
	snap := r.file.Snapshot("meu-app")
	snap.Settings.PeopleDeadlines[classes.RequestPR] = time.Nanosecond
	snap.Settings.MesaDecides[0] = "mexido"
	r.write("versao: [\n")
	again := r.file.Snapshot("meu-app")
	if again.Settings.PeopleDeadlines[classes.RequestPR] == time.Nanosecond || again.Settings.MesaDecides[0] == "mexido" || again.Version != snap.Version {
		t.Fatalf("what a caller changed reached what holds: %+v", again.Settings)
	}
}

// CA-APROV-POL-04
func TestAPolicyFileWithASyntaxErrorKeepsThePreviousRulesAndTellsTheOwner(t *testing.T) {
	t.Parallel()
	r := newPolicyRig(t)
	r.write("versao: 1\ncondicoes:\n  max_arquivos: 3\n")
	before, v := r.file.Rules("meu-app")
	if before.MaxFiles != 3 {
		t.Fatalf("rules = %+v", before)
	}
	r.write("versao: 1\ncondicoes: [oops\n")
	after, v2 := r.file.Rules("meu-app")
	if after.MaxFiles != 3 || v2 != v {
		t.Fatalf("after the broken file the rules are %+v (version %q, was %q), want the previous", after, v2, v)
	}
	if len(r.alerts) != 1 || !errors.Is(r.alerts[0], chain.ErrInvalidPolicy) {
		t.Fatalf("alerts = %v, want one invalid policy", r.alerts)
	}
}

// CA-APROV-POL-16
func TestAPolicyThatLetsADestructiveActionBeDecidedAloneIsInvalid(t *testing.T) {
	t.Parallel()
	r := newPolicyRig(t)
	r.write("versao: 1\nexpiracao: 6h\n")
	r.chain()
	r.write("versao: 1\nexpiracao: 12h\ntipos:\n  acao_destrutiva: { sozinho: se_condicoes }\n")
	if st, _ := r.chain(); st.PeopleDeadline != 6*time.Hour {
		t.Fatalf("settings = %+v, want the previous version", st)
	}
	if len(r.alerts) != 1 || !strings.Contains(r.alerts[0].Error(), "acao_destrutiva") ||
		!strings.Contains(r.alerts[0].Error(), "tipos.acao_destrutiva.sozinho") || !strings.Contains(r.alerts[0].Error(), "risco alto não se decide sozinho") {
		t.Fatalf("alerts = %v, want one naming the item", r.alerts)
	}
	if _, err := chain.ParsePolicyRules([]byte("versao: 1\ntipos:\n  acao_destrutiva: { sozinho: se_condicoes }\n")); err == nil {
		t.Fatal("the rules of the invalid file were read")
	}
}

func TestTheRulesOfTheDefaultFileAreTheDefaultRules(t *testing.T) {
	t.Parallel()
	got, err := chain.ParsePolicyRules([]byte(sessionzero.DefaultPolicyYAML))
	if err != nil {
		t.Fatal(err)
	}
	want := chain.DefaultRules()
	if !reflect.DeepEqual(got.Areas, want.Areas) || !reflect.DeepEqual(got.Alone, want.Alone) || !reflect.DeepEqual(got.MinLevel, want.MinLevel) ||
		!reflect.DeepEqual(got.RiskMinLevel, want.RiskMinLevel) || !slices.Equal(got.Words, want.Words) || !slices.Equal(got.Commands, want.Commands) ||
		!slices.Equal(got.IgnoredLines, want.IgnoredLines) || got.MaxFiles != want.MaxFiles || got.MaxLines != want.MaxLines || got.MaxDeleted != want.MaxDeleted ||
		got.CIGreen != want.CIGreen || got.ReviewByOther != want.ReviewByOther || got.Quorum != want.Quorum || len(got.QuorumRules) != 0 {
		t.Fatalf("default file = %+v\nDefaultRules = %+v", got, want)
	}
}

func TestTheRulesOfTheFileAreCheckedAndTheQuorumRulesAreRead(t *testing.T) {
	t.Parallel()
	good := `versao: 1
condicoes: { max_linhas: 50, linhas_ignoradas: ["**/*.lock"] }
areas_sensiveis: { palavras_em_linhas_adicionadas: ["EXEC"], nova: ["x/**"] }
comandos_sensiveis: ["make deploy*"]
tipos: { merge: { sozinho: nunca } }
quorum:
  padrao: { aprovacoes: 2 }
  regras:
    - { tipo: merge, mesa: "epico/42-login", aprovacoes: 2, exigir_usuarios: [lucas] }
`
	got, err := chain.ParsePolicyRules([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxLines != 50 || !slices.Equal(got.Words, []string{"EXEC"}) || got.Areas["nova"] == nil || got.Areas["migracoes"] == nil || got.Alone[classes.RequestMerge] ||
		got.Quorum != 2 || len(got.QuorumRules) != 1 || got.QuorumRules[0].Require[0] != "lucas" || !slices.Equal(got.Commands, []string{"make deploy*"}) {
		t.Fatalf("rules = %+v", got)
	}
	for name, bad := range map[string]string{
		"unknown condition": "versao: 1\ncondicoes: { max_coisas: 1 }\n",
		"negative size":     "versao: 1\ncondicoes: { max_linhas: -1 }\n",
		"bad glob":          "versao: 1\ncondicoes: { linhas_ignoradas: [\"[\"] }\n",
		"empty word":        "versao: 1\nareas_sensiveis: { palavras_em_linhas_adicionadas: [\" \"] }\n",
		"quorum no target":  "versao: 1\nquorum: { regras: [{ aprovacoes: 2 }] }\n",
		"quorum no count":   "versao: 1\nquorum: { regras: [{ tipo: merge }] }\n",
		"quorum bad type":   "versao: 1\nquorum: { regras: [{ tipo: coisa, aprovacoes: 2 }] }\n",
		"quorum too many":   "versao: 1\nquorum: { padrao: { aprovacoes: 11 } }\n",
	} {
		if _, err := chain.ParsePolicyRules([]byte(bad)); err == nil {
			t.Errorf("%s: the file was accepted", name)
		}
	}
}

// The `delegacao` of the file gives the validity a delegation gets by default
// and the reminder before its end; absent, the 30 and 3 days; out of range, the
// file is invalid (docs/aprovacoes/arquivo-de-politica.md).
func TestThePolicyFileGivesTheValidityAndTheReminderOfDelegations(t *testing.T) {
	t.Parallel()
	r := newPolicyRig(t)
	st, _ := r.chain()
	if st.DelegationValidity != 30*24*time.Hour || st.DelegationReminder != 3*24*time.Hour {
		t.Fatalf("default: %v and %v", st.DelegationValidity, st.DelegationReminder)
	}
	r.write("versao: 1\ndelegacao:\n  validade_padrao: 10d\n  aviso_antes: 2d\n")
	st, _ = r.chain()
	if st.DelegationValidity != 10*24*time.Hour || st.DelegationReminder != 2*24*time.Hour {
		t.Fatalf("from the file: %v and %v", st.DelegationValidity, st.DelegationReminder)
	}
	for name, section := range map[string]string{
		"more than 30 days":         "validade_padrao: 31d",
		"zero validity":             "validade_padrao: 0s",
		"not a duration":            "aviso_antes: logo",
		"reminder past validity":    "validade_padrao: 5d\n  aviso_antes: 5d",
		"reminder past the default": "aviso_antes: 30d",
		"unknown key":               "validade: 5d",
	} {
		if _, _, err := chain.ParsePolicyFile([]byte("versao: 1\ndelegacao:\n  " + section + "\n")); err == nil {
			t.Errorf("%s: the file was accepted", name)
		}
	}
}
