package app

import (
	"errors"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/quests"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/safefile"
)

// The panel gets the code and the params of what it says, not a sentence in pt-BR.
func TestTheReasonsOfTheViewAreCodesAndNotSentences(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		quests.ReasonReturnLimit: "table.pause_return_limit",
		quests.ReasonSameFailure: "table.pause_same_failure",
		usageReason:              "table.pause_usage_limit",
	}
	for reason, code := range cases {
		w := reasonWait(panel.WaitOther, reason)
		if w.ReasonMsg == nil || w.ReasonMsg.Code != code || w.Reason != "" {
			t.Errorf("%q: %+v", reason, w)
		}
	}
	// what a person wrote stays as text
	if w := reasonWait(panel.WaitPaused, "Pausada por Ana"); w.ReasonMsg != nil || w.Reason != "Pausada por Ana" {
		t.Errorf("a person's reason: %+v", w)
	}
	if m := fileProblem(safefile.ErrLink); m.Code != "workspace.file_link" {
		t.Errorf("a link: %+v", m)
	}
	if m := fileProblem(errors.New("x")); m.Code != "workspace.file_unreadable" {
		t.Errorf("unreadable: %+v", m)
	}
}
