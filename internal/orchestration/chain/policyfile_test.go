package chain_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	if risk == nil || risk.Adjustments == nil || len(risk.Adjustments.Items) != 1 || !slices.Equal(risk.Mestre, []string{"docs/interno/**"}) || risk.Secrets != nil {
		t.Fatalf("risk policy = %+v", risk)
	}
	// a workspace without a folder has the default content and the default table
	if st, v, _ := r.file.Chain(context.Background(), "outro"); v != "default" || st.PeopleDeadline != 24*time.Hour || r.file.Risk("outro") != nil {
		t.Fatalf("another workspace: %+v %q", st, v)
	}
}

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
	if err := os.Symlink(target, r.path); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if st, _ := r.chain(); st.PeopleDeadline != 24*time.Hour {
		t.Fatalf("the engine followed a link: %+v", st)
	}
	if len(r.alerts) != 2 {
		t.Fatalf("alerts %v; want the link told", r.alerts)
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
