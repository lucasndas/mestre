package classes

import "testing"

// Part of CA-PERM-32: a failure of the analysis is a denial
func TestAFailureOfTheAnalysisIsADenialAndNeverAnAllow(t *testing.T) {
	t.Parallel()
	d := failClosed(func() Decision { var s []int; _ = s[0]; return Decision{Verdict: Allow} })
	if d.Verdict != Deny || d.Layer != LayerFixed || d.Reason != ReasonInternalError {
		t.Fatalf("decision of a panic = %+v; want a fixed denial", d)
	}
	if d := failClosed(func() Decision { return Decision{Verdict: Ask} }); d.Verdict != Ask {
		t.Fatalf("a decision that does not panic changed: %+v", d)
	}
}
