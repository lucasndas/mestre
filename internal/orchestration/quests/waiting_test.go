package quests

import "testing"

func TestWhatHoldsAStepIsAMessageWithTheRequestAsParam(t *testing.T) {
	t.Parallel()
	_, m := waitingOf(Step{Blocker: blockApproval + "7"})
	if m.Code != "table.wait_approval" || m.Params["request"] != "7" {
		t.Errorf("approval: %+v", m)
	}
	for blocker, code := range map[string]string{blockBusy: "table.wait_busy", blockSlot + "x": "table.wait_slot", blockSync: "table.wait_sync", blockResume: "table.wait_resume"} {
		if _, m := waitingOf(Step{Blocker: blocker}); m.Code != code {
			t.Errorf("%s: %+v", blocker, m)
		}
	}
	if text, m := waitingOf(Step{}); text != "" || m.Code != "" {
		t.Errorf("nothing holds it: %q %+v", text, m)
	}
}
