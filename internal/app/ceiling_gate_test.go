package app_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
)

// A class approved when the ceiling was wider, and above the ceiling now, cannot
// be given to a new character (recruit: 3 ceiling_denied) nor to a step of a
// quest (quest create: 2 invalid_quest naming the ceiling); the production
// Ceiling is over the libraries of classes.
// CA-TETO-09
func TestAClassAboveTheNarrowedCeilingIsRefusedByRecruitAndByQuestCreate(t *testing.T) {
	t.Parallel()
	w := newCitadelWorld(t)
	w.approveClass("amplo", "version: 1\nfiles:\n  read: [\"**\"]\n  write: [\"**\"]\ncommands:\n  allow: [\"npm test\"]\n  default: ask\nnetwork: {internet: true}\n")
	if r := w.run(w.master, "recruit", "Livre", "--sheet", "Dev Claude", "--class", "amplo", "--json"); r.code != 0 {
		t.Fatalf("recruit under the default ceiling = %+v", r)
	}
	c, err := classes.ParseCeiling([]byte("version: 1\npermissions: {network: {internet: false}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	w.lib.Ceiling = commCeiling{c}

	r := w.run(w.master, "recruit", "Barrado", "--sheet", "Dev Claude", "--class", "amplo", "--json")
	v := decodeJSON(t, r)
	if r.code != 3 || v.Error.Code != "ceiling_denied" || !strings.Contains(v.Error.Message, "network.internet") {
		t.Fatalf("recruit above the ceiling = %d %s", r.code, r.stdout)
	}
	if _, err := w.orc.Registry.ByName(w.ctx, w.slug, w.table.ID, "Barrado"); err == nil {
		t.Error("a character was made above the ceiling")
	}

	_, err = w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: w.slug, Table: w.table.ID, Designer: w.master.ID,
		Data: []byte("name: Barrada\nsteps:\n  - {id: a, sheet: Dev Claude, class: amplo}\n")})
	var ve *quests.ValidationError
	if !errors.As(err, &ve) || !strings.Contains(err.Error(), "above the ceiling") {
		t.Fatalf("quest create with a class above the ceiling = %v", err)
	}
}
