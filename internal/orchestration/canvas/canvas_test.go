package canvas_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/canvas"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/pkg/clock"
)

func newStore(t *testing.T) *canvas.Store {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(t.Context(), filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &canvas.Store{DB: db, Clock: clk}
}

func overlap(a, b canvas.Item) bool {
	aw, ah := a.W, a.H
	bw, bh := b.W, b.H
	return a.X < b.X+bw && b.X < a.X+aw && a.Y < b.Y+bh && b.Y < a.Y+ah
}

// The elements the engine makes are placed near the one that made them, one beside
// the other, never on top of each other, and the place is kept.
func TestElementsAreAutoPlacedNearTheirCreatorWithoutOverlap(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := t.Context()
	creator := canvas.Key(canvas.KindCharacter, "master")
	if _, err := s.Ensure(ctx, "loja", "m1", []canvas.Wanted{{Key: creator}}); err != nil {
		t.Fatal(err)
	}
	var wanted []canvas.Wanted
	wanted = append(wanted, canvas.Wanted{Key: creator})
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		wanted = append(wanted, canvas.Wanted{Key: canvas.Key(canvas.KindCharacter, id), Near: creator})
	}
	wanted = append(wanted, canvas.Wanted{Key: canvas.Key(canvas.KindScroll, "loja/m1/p0001"), Near: creator})
	items, err := s.Ensure(ctx, "loja", "m1", wanted)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != len(wanted) {
		t.Fatalf("%d items for %d elements", len(items), len(wanted))
	}
	sized := make([]canvas.Item, len(items))
	for i, it := range items {
		w, h := 640.0, 420.0
		if it.Kind() == canvas.KindScroll {
			w, h = 320, 240
		}
		sized[i] = canvas.Item{Key: it.Key, X: it.X, Y: it.Y, W: w, H: h}
	}
	for i := range sized {
		for j := i + 1; j < len(sized); j++ {
			if overlap(sized[i], sized[j]) {
				t.Errorf("%s and %s overlap: %+v %+v", sized[i].Key, sized[j].Key, sized[i], sized[j])
			}
		}
	}
	// reading again changes nothing
	again, _ := s.Ensure(ctx, "loja", "m1", wanted)
	if len(again) != len(items) {
		t.Fatalf("%d items after reading again, were %d", len(again), len(items))
	}
	for i := range items {
		if again[i] != items[i] { // both come ordered by key
			t.Errorf("%s moved on its own: %+v -> %+v", items[i].Key, items[i], again[i])
		}
	}
}

// What the user moved stays; what is no longer in the table is forgotten; a free
// group stays until the user removes it.
func TestTheLayoutKeepsWhatTheUserSetAndForgetsWhatIsGone(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := t.Context()
	a, b := canvas.Key(canvas.KindCharacter, "a"), canvas.Key(canvas.KindCharacter, "b")
	if err := s.Put(ctx, "loja", "m1", []canvas.Item{{Key: a, X: 5, Y: 6, W: 700, H: 500, Parent: "group:g"}, {Key: "group:g", Label: "Time", W: 900, H: 700}}); err != nil {
		t.Fatal(err)
	}
	items, err := s.Ensure(ctx, "loja", "m1", []canvas.Wanted{{Key: a}, {Key: b}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]canvas.Item{}
	for _, it := range items {
		got[it.Key] = it
	}
	if got[a] != (canvas.Item{Key: a, X: 5, Y: 6, W: 700, H: 500, Parent: "group:g"}) || got["group:g"].Label != "Time" || got[b].Key == "" {
		t.Errorf("layout = %+v", got)
	}
	items, _ = s.Ensure(ctx, "loja", "m1", []canvas.Wanted{{Key: b}})
	if len(items) != 2 { // b and the group
		t.Errorf("after a left: %+v", items)
	}
	// another table has its own layout
	if other, _ := s.Items(ctx, "loja", "m2"); len(other) != 0 {
		t.Errorf("the other table has %+v", other)
	}
}

// Part of CA-CLI-COORD-04, the layout side: the element that takes the place of another
// gets its position, its size and its group.
func TestAnElementInheritsThePlaceOfTheOneItReplaces(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := t.Context()
	old, now := canvas.Key(canvas.KindCharacter, "old"), canvas.Key(canvas.KindCharacter, "new")
	if err := s.Put(ctx, "loja", "m1", []canvas.Item{{Key: old, X: 100, Y: -40, W: 800, H: 600, Parent: "group:g"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Inherit(ctx, "loja", "m1", old, now); err != nil {
		t.Fatal(err)
	}
	items, _ := s.Ensure(ctx, "loja", "m1", []canvas.Wanted{{Key: now}})
	if len(items) != 1 || items[0] != (canvas.Item{Key: now, X: 100, Y: -40, W: 800, H: 600, Parent: "group:g"}) {
		t.Errorf("items = %+v", items)
	}
}
