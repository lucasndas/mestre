package canvas

import (
	"context"
	"math"
	"slices"
	"strings"
)

// The default size of each kind of element, the room the automatic placement
// leaves for it.
func defaultSize(kind string) (w, h float64) {
	switch kind {
	case KindCharacter:
		return 640, 420
	case KindTome:
		return 260, 140
	case KindGroup:
		return 600, 400
	}
	return 320, 240 // scroll
}

// gap is the empty space left between two elements: room for the line between them and its label.
const gap = 120

func (i Item) size() (float64, float64) {
	w, h := defaultSize(i.Kind())
	if i.W > 0 {
		w = i.W
	}
	if i.H > 0 {
		h = i.H
	}
	return w, h
}

// overlaps says whether the rectangles (with the gap around them) touch.
func overlaps(x, y, w, h float64, o Item) bool {
	ow, oh := o.size()
	return x < o.X+ow+gap && o.X < x+w+gap && y < o.Y+oh+gap && o.Y < y+h+gap
}

// Free finds a position for an element of the kind that touches none of the
// others, as near as it can to the anchor (nil: the origin): the first of the
// positions around it, ring after ring, that is free. Groups do not count as
// taken: an element may sit inside one.
func Free(kind string, near *Item, others []Item) (x, y float64) {
	w, h := defaultSize(kind)
	ax, ay := 0.0, 0.0
	if near != nil {
		nw, _ := near.size()
		ax, ay = near.X+nw+gap, near.Y // to the right of the creator
	}
	stepX, stepY := w+gap, h+gap
	free := func(px, py float64) bool {
		for _, o := range others {
			if o.Kind() != KindGroup && overlaps(px, py, w, h, o) {
				return false
			}
		}
		return true
	}
	for ring := 0; ; ring++ {
		best, found := math.MaxFloat64, false
		for dx := -ring; dx <= ring; dx++ {
			for dy := -ring; dy <= ring; dy++ {
				if max(abs(dx), abs(dy)) != ring {
					continue
				}
				px, py := ax+float64(dx)*stepX, ay+float64(dy)*stepY
				if !free(px, py) {
					continue
				}
				if d := math.Hypot(float64(dx), float64(dy)); !found || d < best {
					best, found, x, y = d, true, px, py
				}
			}
		}
		if found {
			return x, y
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Wanted is an element the canvas shows that may not be placed yet.
type Wanted struct {
	Key string
	// Near is the key of the element it is placed next to (its creator); an
	// unknown or empty one places it by the others.
	Near string
}

// Ensure returns the layout of the table with a position for every wanted
// element: the ones with none are placed near their creator without overlap and
// saved. The elements that are no longer wanted are forgotten (a group stays).
func (s *Store) Ensure(ctx context.Context, workspace, table string, wanted []Wanted) ([]Item, error) {
	items, err := s.Items(ctx, workspace, table)
	if err != nil {
		return nil, err
	}
	have := make(map[string]int, len(items))
	for i, it := range items {
		have[it.Key] = i
	}
	want := make(map[string]bool, len(wanted))
	var added []Item
	for _, w := range wanted {
		want[w.Key] = true
		if _, ok := have[w.Key]; ok {
			continue
		}
		var near *Item
		if j, ok := have[w.Near]; ok {
			near = &items[j]
		}
		kind, _, _ := cut(w.Key)
		x, y := Free(kind, near, items)
		it := Item{Key: w.Key, X: x, Y: y}
		items = append(items, it)
		have[w.Key] = len(items) - 1
		added = append(added, it)
	}
	var gone []string
	kept := items[:0:0]
	for _, it := range items {
		if it.Kind() != KindGroup && !want[it.Key] {
			gone = append(gone, it.Key)
			continue
		}
		kept = append(kept, it)
	}
	if err := s.Put(ctx, workspace, table, added); err != nil {
		return nil, err
	}
	if err := s.Delete(ctx, workspace, table, gone...); err != nil {
		return nil, err
	}
	slices.SortFunc(kept, func(a, b Item) int { return strings.Compare(a.Key, b.Key) })
	return kept, nil
}

func cut(key string) (kind, id string, ok bool) {
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			return key[:i], key[i+1:], true
		}
	}
	return key, "", false
}

// Inherit gives the element `to` the place of `from`: the new character that
// replaces another keeps its position, its size and its group
// (docs/cli/coordenacao.md, CA-CLI-COORD-04). from stays as it is: it is
// forgotten when the next read finds it no longer wanted.
func (s *Store) Inherit(ctx context.Context, workspace, table, from, to string) error {
	items, err := s.Items(ctx, workspace, table)
	if err != nil {
		return err
	}
	for _, it := range items {
		if it.Key == from {
			it.Key = to
			return s.Put(ctx, workspace, table, []Item{it})
		}
	}
	return nil
}
