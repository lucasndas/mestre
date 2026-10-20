package chain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/chain"
)

// CA-CADEIA-16
func TestTheGroupsOfMedioTheTableMasterDecidesAreReadFromThePolicyFileAndABadListKeepsTheVersionBeforeAndTellsTheOwner(t *testing.T) {
	t.Parallel()
	r := newPolicyRig(t)
	w := newWorld(t, worldOpts{policyFile: r.file})
	dev := w.recruit("Dev")

	// the file is loaded with the three groups: docker compose up is of a group out of the list
	r.write("versao: 1\ncadeia:\n  mesa_decide: [rede, apagar, instalacao_e_sistema]\n")
	compose, global := w.shell(dev, "docker compose up"), w.shell(dev, "npm install -g x")
	if w.level(compose) != chain.LevelGrandMaster {
		t.Errorf("docker compose up, of a group out of the list, is at %+v; want the Grand Master", w.request(compose).Level)
	}
	if w.level(global) != chain.LevelMesa {
		t.Errorf("npm install -g, of a group in the list, is at %+v; want the table master", w.request(global).Level)
	}

	// the empty list: the table master only gets what is baixo
	r.write("versao: 1\ncadeia:\n  mesa_decide: []\n")
	if id := w.shell(dev, "pip install y"); w.level(id) != chain.LevelGrandMaster {
		t.Errorf("with the empty list a medio is at %+v; want the Grand Master", w.request(id).Level)
	}
	if id := w.shell(dev, "npm install z"); w.level(id) != chain.LevelMesa {
		t.Errorf("with the empty list a baixo is at %+v; want the table master", w.request(id).Level)
	}

	// a list with alto, fixo or git_push is invalid: the version before holds, with the item cited, and the Owner is told
	r.write("versao: 1\ncadeia:\n  mesa_decide: [rede, apagar, instalacao_e_sistema]\n")
	if id := w.shell(dev, "npm install -g again"); w.level(id) != chain.LevelMesa {
		t.Fatalf("after the list came back, npm install -g is at %+v; want the table master", w.request(id).Level)
	}
	for _, bad := range []string{"alto", "fixo", "git_push"} {
		n := len(r.alerts)
		r.write("versao: 1\ncadeia:\n  mesa_decide: [rede, " + bad + "]\n")
		if id := w.shell(dev, "npm install -g "+bad); w.level(id) != chain.LevelMesa {
			t.Errorf("with %s in the list, npm install -g (a group of the version before) is at %+v; want the table master, as before", bad, w.request(id).Level)
		}
		if id := w.shell(dev, "docker compose up --"+bad); w.level(id) != chain.LevelGrandMaster {
			t.Errorf("with %s in the list, docker compose up is at %+v; want the Grand Master, as before", bad, w.request(id).Level)
		}
		if len(r.alerts) != n+1 {
			t.Fatalf("with %s in the list the Owner was told %d times; want once", bad, len(r.alerts)-n)
		}
		last := r.alerts[len(r.alerts)-1]
		var se *chain.SectionError
		if !errors.Is(last, chain.ErrInvalidPolicy) || !errors.As(last, &se) || !strings.Contains(se.Item, bad) {
			t.Errorf("the alert for %s is %v; want an invalid policy citing the item", bad, last)
		}
	}
}
