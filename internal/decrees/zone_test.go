package decrees_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/users"
)

func (w *world) setZone(zone string) {
	w.t.Helper()
	if _, err := w.db.Writer().ExecContext(w.ctx, `INSERT OR REPLACE INTO guild (id, guild_id, ca_cert, epoch, epoch_certificate, timezone, recovery_public_key, created_at)
		VALUES (1, '01M4ZPXYG0PNPDZ5JN7697ZSCH', x'00', 1, '{}', ?, x'07', 0)`, zone); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) confirmationUntil(until time.Time) string {
	w.t.Helper()
	ana := w.person("Ana"+until.Format("150405.000"), users.Administrator, "loja")
	p := allowCmd(ana, "dev", "npm install *")
	p.Until = until
	return mustPropose(w.t, w, p).Text
}

// CA-DEC-13
func TestTheConfirmationShowsTheLimitInTheGuildZone(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.setZone("America/Sao_Paulo")
	until, err := time.Parse(time.RFC3339, "2026-10-25T23:59:59-03:00")
	if err != nil {
		t.Fatal(err)
	}
	contains(t, w.confirmationUntil(until), "até 25/10/2026 23:59:59 (America/Sao_Paulo, UTC-03:00)")

	// daylight saving: the same wall clock, an hour apart, and two offsets
	w.setZone("America/New_York")
	contains(t, w.confirmationUntil(time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)), "até 01/11/2026 01:30 (America/New_York, UTC-04:00)")
	contains(t, w.confirmationUntil(time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)), "até 01/11/2026 01:30 (America/New_York, UTC-05:00)")

	// a half-hour offset
	w.setZone("Asia/Kolkata")
	contains(t, w.confirmationUntil(time.Date(2026, 10, 25, 20, 0, 0, 0, time.UTC)), "até 26/10/2026 01:30 (Asia/Kolkata, UTC+05:30)")
}

// CA-DEC-13
func TestTheConfirmationFallsBackToUTCWithoutAUsableGuildZone(t *testing.T) {
	t.Parallel()
	until := time.Date(2026, 10, 26, 2, 59, 59, 0, time.UTC)
	for _, zone := range []string{"", "Mars/Olympus", "Local", "UTC"} {
		w := newWorld(t)
		if zone != "" {
			w.setZone(zone)
		}
		contains(t, w.confirmationUntil(until), "até 26/10/2026 02:59:59 UTC ")
	}
}

// CA-DEC-13
func TestASubSecondLimitIsKeptInWholeSecondsSoTheShownMomentIsTheEnd(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.setZone("America/Sao_Paulo")
	until := time.Date(2026, 10, 26, 2, 59, 59, 700_000_000, time.UTC)
	contains(t, w.confirmationUntil(until), "até 25/10/2026 23:59:59 (America/Sao_Paulo, UTC-03:00)")
}

// CA-DEC-13
func TestTheZoneOnlyChangesTheTextNotTheMomentTheDecreeEnds(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.setZone("America/Sao_Paulo")
	ana := w.person("Ana", users.Administrator, "loja")
	p := allowCmd(ana, "dev", "npm install *")
	p.Until = time.Date(2026, 10, 26, 2, 59, 59, 0, time.UTC)
	d := w.confirm(p)
	w.clk.Advance(p.Until.Add(-time.Second).Sub(w.clk.Now()))
	if err := w.svc.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.svc.Get(w.ctx, d.Number); got.State != decrees.Active {
		t.Fatalf("one second before: %s", got.State)
	}
	w.clk.Advance(time.Second)
	if err := w.svc.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.svc.Get(w.ctx, d.Number); got.State != decrees.Expired {
		t.Fatalf("at the instant: %s", got.State)
	}
}

// Part of CA-DEC-13: the zone of the Guild does not depend on the zoneinfo of
// the host (an installed Windows has none): the package embeds the database of
// the zones, and with ZONEINFO pointing nowhere the confirmation still says it.
func TestTheGuildZoneComesFromTheEmbeddedDatabase(t *testing.T) {
	// not parallel: it sets ZONEINFO for the process
	t.Setenv("ZONEINFO", filepath.Join(t.TempDir(), "sem-zoneinfo.zip"))
	f, err := parser.ParseFile(token.NewFileSet(), "zone.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	embedded := false
	for _, imp := range f.Imports {
		embedded = embedded || imp.Path.Value == `"time/tzdata"`
	}
	if !embedded {
		t.Fatal("the package that loads the zone of the Guild does not embed time/tzdata")
	}
	w := newWorld(t)
	w.setZone("America/Sao_Paulo")
	until, err := time.Parse(time.RFC3339, "2026-10-25T23:59:59-03:00")
	if err != nil {
		t.Fatal(err)
	}
	contains(t, w.confirmationUntil(until), "até 25/10/2026 23:59:59 (America/Sao_Paulo, UTC-03:00)")
}
