package chain_test

import (
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/chain"
)

// The `resumo` of the policy file: absent keys are the default, and anything out
// of range makes the whole file invalid.
func TestTheSummarySectionOfThePolicyFile(t *testing.T) {
	t.Parallel()
	def, err := chain.ParseSummary(nil)
	if err != nil || def.Frequency != "diario" || def.At != "18:00" || len(def.For) != 1 || def.For[0] != "dono" || def.Sample != 5 || def.SuggestAfter != 3 {
		t.Fatalf("default = %+v, %v", def, err)
	}
	got, err := chain.ParseSummary([]byte("frequencia: semanal\nhorario: \"07:30\"\npara: [dono, administradores]\namostra: 2\nsugerir_apos: 4\n"))
	if err != nil || got.Frequency != "semanal" || got.At != "07:30" || len(got.For) != 2 || got.Sample != 2 || got.SuggestAfter != 4 {
		t.Fatalf("parsed = %+v, %v", got, err)
	}
	for name, yaml := range map[string]string{
		"frequency":  "frequencia: mensal\n",
		"time":       "horario: \"25:00\"\n",
		"recipients": "para: []\n",
		"sample":     "amostra: 99\n",
		"suggest":    "sugerir_apos: 1\n",
		"unknown":    "frequencia: diario\nextra: 1\n",
	} {
		if _, err := chain.ParseSummary([]byte(yaml)); err == nil {
			t.Errorf("%s: accepted %q", name, yaml)
		}
	}
	st, _, err := chain.ParsePolicyFile([]byte("versao: 1\nresumo:\n  frequencia: desligado\n"))
	if err != nil || st.Summary.Frequency != "desligado" {
		t.Fatalf("file = %+v, %v", st.Summary, err)
	}
	if st, _, err = chain.ParsePolicyFile([]byte("versao: 1\n")); err != nil || st.Summary.Frequency != "diario" || st.Summary.Sample != 5 {
		t.Fatalf("file without resumo = %+v, %v", st.Summary, err)
	}
}
