package machinesheet_test

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/testkit/sheetfake"
)

func sampleSheet(t *testing.T) machinesheet.Sheet {
	t.Helper()
	b, err := machinesheet.NewBuilder(sheetfake.Windows("windows-pc").Sources(), "m1", "")
	if err != nil {
		t.Fatal(err)
	}
	s, err := b.Build(t.Context(), machinesheet.Sheet{})
	if err != nil {
		t.Fatal(err)
	}
	s.Rev = 10
	return s
}

func raw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAPatchRoundTripsThroughDiffAndMerge(t *testing.T) {
	t.Parallel()
	prev := sampleSheet(t)
	cur := prev.Clone()
	cur.Rev = 11
	cur.GUI = false
	cur.IPs = []string{"10.0.0.2"}
	cur.Browser = nil
	cur.Tools = map[string]string{"git": "2.51.0"}
	cur.Providers[0].Profiles[0].LoggedIn = false

	patch, changed := machinesheet.Diff(prev, cur)
	if !changed {
		t.Fatal("no change found")
	}
	if want := []string{"browser", "gui", "ips", "providers", "tools"}; !slices.Equal(keys(patch), want) {
		t.Errorf("patch fields = %v, want %v", keys(patch), want)
	}
	got, err := machinesheet.Merge(prev, patch, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rev != 11 || !machinesheet.Equal(got, cur) {
		t.Errorf("merged = %+v, want %+v", got, cur)
	}
	if _, changed := machinesheet.Diff(cur, cur); changed {
		t.Error("a sheet differs from itself")
	}
	cur2 := cur.Clone()
	cur2.Rev = 99
	if _, changed := machinesheet.Diff(cur, cur2); changed {
		t.Error("a change of the revision alone is a change")
	}
}

func TestAWholeSheetReplacesAndOnlyAPatchChecksTheRevision(t *testing.T) {
	t.Parallel()
	kept := sampleSheet(t)
	whole := kept.Clone()
	whole.Rev = 3 // older than the one kept: a hello is always the newest word
	whole.GUI = false
	got, err := machinesheet.Merge(kept, whole.Object(), true)
	if err != nil || got.GUI || got.Rev != 3 {
		t.Fatalf("hello merge = %+v, %v", got, err)
	}
	_, err = machinesheet.Merge(kept, protocol.Object{"rev": raw(t, 10), "gui": raw(t, false)}, false)
	if !errors.Is(err, machinesheet.ErrStale) {
		t.Errorf("a patch with the revision kept = %v, want ErrStale", err)
	}
	_, err = machinesheet.Merge(kept, protocol.Object{"gui": raw(t, false)}, false)
	if !errors.Is(err, machinesheet.ErrInvalid) {
		t.Errorf("a patch without a revision = %v, want ErrInvalid", err)
	}
}

func TestASheetWithoutTheRequiredFieldsIsInvalid(t *testing.T) {
	t.Parallel()
	whole := sampleSheet(t).Object()
	for _, field := range []string{"os", "arch", "providers"} {
		t.Run("without "+field, func(t *testing.T) {
			t.Parallel()
			o := protocol.Object{}
			for k, v := range whole {
				if k != field {
					o[k] = v
				}
			}
			if _, err := machinesheet.Merge(machinesheet.Sheet{}, o, true); !errors.Is(err, machinesheet.ErrInvalid) {
				t.Errorf("Merge = %v, want ErrInvalid", err)
			}
		})
	}
	t.Run("providers null", func(t *testing.T) {
		t.Parallel()
		o := protocol.Object{}
		for k, v := range whole {
			o[k] = v
		}
		o["providers"] = json.RawMessage("null")
		if _, err := machinesheet.Merge(machinesheet.Sheet{}, o, true); !errors.Is(err, machinesheet.ErrInvalid) {
			t.Errorf("Merge = %v, want ErrInvalid", err)
		}
	})
	t.Run("an unknown system", func(t *testing.T) {
		t.Parallel()
		o := protocol.Object{}
		for k, v := range whole {
			o[k] = v
		}
		o["os"] = raw(t, "plan9")
		if _, err := machinesheet.Merge(machinesheet.Sheet{}, o, true); !errors.Is(err, machinesheet.ErrInvalid) {
			t.Errorf("Merge = %v, want ErrInvalid", err)
		}
	})
	t.Run("a field of the wrong type", func(t *testing.T) {
		t.Parallel()
		o := protocol.Object{}
		for k, v := range whole {
			o[k] = v
		}
		o["gui"] = raw(t, "yes")
		if _, err := machinesheet.Merge(machinesheet.Sheet{}, o, true); !errors.Is(err, machinesheet.ErrInvalid) {
			t.Errorf("Merge = %v, want ErrInvalid", err)
		}
	})
}

func TestAnUnknownFieldIsIgnoredAndTheFieldsOfTheGuildAreNotTaken(t *testing.T) {
	t.Parallel()
	o := sampleSheet(t).Object()
	o["cor"] = raw(t, "azul")
	o["online"] = raw(t, true)
	o["excluded"] = raw(t, true)
	o["name"] = raw(t, "impostor")
	o["machine_id"] = raw(t, "other")
	got, err := machinesheet.Merge(machinesheet.Sheet{}, o, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Online || got.Excluded || got.Name != "" || got.MachineID != "" {
		t.Errorf("the Tower set a field of the Guild: %+v", got)
	}
	if b, _ := json.Marshal(got); slices.Contains(keysOf(t, b), "cor") {
		t.Error("the unknown field was kept")
	}
}

func keysOf(t *testing.T, b []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestTheOperatorIsReadyOnlyWhenItIsEnabled(t *testing.T) {
	t.Parallel()
	m := sheetfake.Windows("windows-pc")
	m.SetOperator(machinesheet.Operator{Ready: true, Reason: machinesheet.ReasonUAC}) // not enabled
	b, err := machinesheet.NewBuilder(m.Sources(), "m1", "")
	if err != nil {
		t.Fatal(err)
	}
	s, err := b.Build(t.Context(), machinesheet.Sheet{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Operador != (machinesheet.Operator{}) {
		t.Errorf("operador = %+v for an Operator that is not enabled", s.Operador)
	}
	m.SetOperator(machinesheet.Operator{Enabled: true, Ready: true, Reason: machinesheet.ReasonUAC})
	if s, _ = b.Build(t.Context(), s); s.Operador.Reason != "" {
		t.Errorf("a ready Operator has the reason %q", s.Operador.Reason)
	}
}

func TestTheSheetNeverCarriesASecret(t *testing.T) {
	t.Parallel()
	// the profiles show only the name and the state of the login
	b, err := json.Marshal(sampleSheet(t))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	prof := m["providers"].([]any)[0].(map[string]any)["profiles"].([]any)[0].(map[string]any)
	for k := range prof {
		if !slices.Contains([]string{"name", "version", "logged_in", "conformant", "default"}, k) {
			t.Errorf("a profile carries %q", k)
		}
	}
}
