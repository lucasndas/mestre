package ptyd_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/terminal/ptyd"
	"github.com/mestre-ia/mestre/pkg/clock"
)

func newVT(cols, rows, history int) (ptyd.Emulator, *replies) {
	r := &replies{}
	return ptyd.NewVTEmulator(cols, rows, history, r.add), r
}

type replies struct {
	mu      sync.Mutex
	got     []byte
	changed chan struct{} // closed and replaced on every reply
}

func (r *replies) add(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, p...)
	if r.changed != nil {
		close(r.changed)
		r.changed = nil
	}
}

// waitFor blocks until the replies so far equal want, with a safety limit
// so a missing answer fails the test instead of hanging it.
func (r *replies) waitFor(t *testing.T, want string) {
	t.Helper()
	limit := clock.Real{}.NewTimer(10 * time.Second)
	defer limit.Stop()
	for {
		r.mu.Lock()
		got := string(r.got)
		if r.changed == nil {
			r.changed = make(chan struct{})
		}
		ch := r.changed
		r.mu.Unlock()
		if got == want {
			return
		}
		select {
		case <-ch:
		case <-limit.C():
			t.Fatalf("replies = %q, want %q", got, want)
		}
	}
}

func TestVTScreenIsPlainText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want string
		cols           int
	}{
		{"colors and bold", "\x1b[1;31mred\x1b[0m plain\r\n", "red plain", 10},
		{"cursor movement overwrites", "hello\r\x1b[3Cp!", "help!", 10},
		{"clear screen then redraw", "old\r\nold2\x1b[2J\x1b[Hnew", "new", 10},
		{"erase to end of line", "abcdef\x1b[3D\x1b[K", "abc", 10},
		{"title and hyperlink sequences vanish", "\x1b]0;title\x07\x1b]8;;http://x\x07link\x1b]8;;\x07", "link", 10},
		{"wide characters take two columns", "日本語 ok", "日本語 ok", 20},
		{"line wrap at the right edge", strings.Repeat("x", 12), strings.Repeat("x", 10) + "\nxx", 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			em, _ := newVT(tt.cols, 5, 100)
			defer em.Close()
			em.Write([]byte(tt.in))
			if got := em.Screen(); got != tt.want {
				t.Fatalf("Screen() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVTAlternateScreenAndScrollRegion(t *testing.T) {
	t.Parallel()
	em, _ := newVT(20, 6, 100)
	defer em.Close()

	em.Write([]byte("shell prompt$ "))
	em.Write([]byte("\x1b[?1049h\x1b[2J\x1b[Htui header\r\n\x1b[2;5r\x1b[5;1Hbottom\r\nscrolled"))
	if got, want := em.Screen(), "tui header\n\n\nbottom\nscrolled"; got != want {
		t.Fatalf("alternate screen = %q, want %q", got, want)
	}
	em.Write([]byte("\x1b[r\x1b[?1049l"))
	if got, want := em.Screen(), "shell prompt$"; got != want {
		t.Fatalf("after leaving the alternate screen = %q, want %q", got, want)
	}
}

func TestVTHistoryKeepsTheLastLines(t *testing.T) {
	t.Parallel()
	em, _ := newVT(20, 3, 4)
	defer em.Close()
	for i := 1; i <= 10; i++ {
		em.Write([]byte(fmt.Sprintf("line %d\r\n", i)))
	}

	if got, want := em.History(-1), "line 5\nline 6\nline 7\nline 8\nline 9\nline 10"; got != want {
		t.Fatalf("History(-1) = %q, want %q", got, want)
	}
	if got, want := em.History(2), "line 9\nline 10"; got != want {
		t.Fatalf("History(2) = %q, want %q", got, want)
	}
}

func TestVTCursorAndSize(t *testing.T) {
	t.Parallel()
	em, _ := newVT(30, 8, 10)
	defer em.Close()
	em.Write([]byte("ab\r\ncde"))

	if col, row := em.Cursor(); col != 3 || row != 1 {
		t.Fatalf("Cursor() = %d,%d, want 3,1", col, row)
	}
	if cols, rows := em.Size(); cols != 30 || rows != 8 {
		t.Fatalf("Size() = %dx%d, want 30x8", cols, rows)
	}
}

func TestVTResetForgetsScreenAndHistory(t *testing.T) {
	t.Parallel()
	em, _ := newVT(20, 3, 10)
	defer em.Close()
	em.Write([]byte("a\r\nb\r\nc\r\nd\r\ne"))

	em.Reset()
	em.Write([]byte("fresh"))

	if got := em.History(-1); got != "fresh" {
		t.Fatalf("History after Reset = %q, want %q", got, "fresh")
	}
}

// The process asks where the cursor is; the emulator answers through the
// reply function. The library hands answers over a synchronous pipe, so an
// emulator that does not drain it would block Write forever.
func TestVTAnswersQueriesWithoutBlocking(t *testing.T) {
	t.Parallel()
	em, r := newVT(20, 5, 10)
	defer em.Close()

	em.Write([]byte("abc\x1b[6n"))

	r.waitFor(t, "\x1b[1;4R")
	if got := em.Screen(); got != "abc" {
		t.Fatalf("Screen() = %q, want %q", got, "abc")
	}
}
