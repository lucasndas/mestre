package cli_test

import (
	"encoding/json"
	"testing"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/testkit/localrig"
)

// CA-MSG-06
func TestARefusalOfTheEngineIsOnTheJSONWithCodeParamsMessageAndHintInEnglish(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *localrig.Rig) {
		resp := chain.RefusalResponse(&chain.Refusal{
			Code: chain.CodeNotAtLevel,
			Msg:  i18n.M("approval.not_at_your_level", "id", 7, "level", i18n.M("approval.level_grand_master")),
		})
		respondWith(r, "list", resp)
		r.Character("tA", "alice")
		p := newProc(r, "alice-proc", "tA", "alice")
		got := p.run(t.Context(), "list", "--json")
		var v struct {
			OK    bool
			Error struct {
				Code    string
				Params  map[string]any
				Message string
				Hint    string
			}
		}
		if err := json.Unmarshal([]byte(got.stdout), &v); err != nil {
			t.Fatalf("%v: %s", err, got.stdout)
		}
		if v.OK || got.code != 3 {
			t.Fatalf("ok %v, exit %d", v.OK, got.code)
		}
		// the code is the one of before; the message is the catalog in English, with the parameters
		if v.Error.Code != "not_at_level" || v.Error.Hint == "" {
			t.Errorf("error = %+v", v.Error)
		}
		if want := "The request #7 is not at your level: it waits on the Grand Master."; v.Error.Message != want {
			t.Errorf("message = %q, want %q", v.Error.Message, want)
		}
		if v.Error.Params["id"] != float64(7) || v.Error.Params["level"] != "the Grand Master" {
			t.Errorf("params = %v", v.Error.Params)
		}
		// every error has the four fields, even one that has no parameters
		respondWith(r, "list", localchan.Fail(localchan.CodeNotFound, "none", ""))
		plain := p.run(t.Context(), "list", "--json")
		var raw struct{ Error map[string]json.RawMessage }
		if err := json.Unmarshal([]byte(plain.stdout), &raw); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"code", "params", "message", "hint"} {
			if _, ok := raw.Error[f]; !ok {
				t.Errorf("error without %q: %s", f, plain.stdout)
			}
		}
	})
}
