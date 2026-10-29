package workspaces_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
)

// The phrase of the engine for a failed stage comes from its code and is read in
// the language of the reader; what the program said goes in untranslated.
func TestStageFailureReasonIsAMessageWithTheProgramErrorAsText(t *testing.T) {
	t.Parallel()
	f := workspaces.StageFailure{Code: "clone_failed", Reason: "fatal: repositório não encontrado"}
	en, pt := i18n.Render(i18n.En, f.ReasonMsg()), i18n.Render(i18n.PtBR, f.ReasonMsg())
	if want := "The clone of the repository failed: fatal: repositório não encontrado"; en != want {
		t.Errorf("en = %q, want %q", en, want)
	}
	if want := "O clone do repositório falhou: fatal: repositório não encontrado"; pt != want {
		t.Errorf("pt-BR = %q, want %q", pt, want)
	}
	other := workspaces.StageFailure{Code: "unheard_of", Reason: "boom"}
	if got := i18n.Render(i18n.PtBR, other.ReasonMsg()); !strings.HasPrefix(got, "A etapa falhou: boom") {
		t.Errorf("unknown code = %q, want the generic phrase", got)
	}
}
