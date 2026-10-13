//go:build full

package clock

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Remote is the clock of a process of the full suite (docs/qualidade/dubles.md):
// a Fake driven by the test through the socket of MESTRE_TEST_CLOCK. The test
// is the server; the process connects, receives `set <RFC 3339>` or
// `advance <duration>` lines, applies each to its Fake and answers
// `ok <now in RFC 3339>`. The first line must be a set: it is the time the
// Fake starts at. Binaries without the tag full do not have this code.
func Remote(path string) (*Fake, func() error, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, nil, fmt.Errorf("clock: the test clock at %s: %w", path, err)
	}
	r := bufio.NewReader(conn)
	line, err := r.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("clock: the test clock: %w", err)
	}
	t, ok := strings.CutPrefix(strings.TrimSpace(line), "set ")
	if !ok {
		conn.Close()
		return nil, nil, fmt.Errorf("clock: the test clock began with %q, not a set", line)
	}
	start, err := time.Parse(time.RFC3339Nano, t)
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("clock: the test clock: %w", err)
	}
	f := NewFake(start)
	if _, err := fmt.Fprintf(conn, "ok %s\n", f.Now().Format(time.RFC3339Nano)); err != nil {
		conn.Close()
		return nil, nil, err
	}
	go func() {
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if err := apply(f, strings.TrimSpace(line)); err != nil {
				_, _ = fmt.Fprintf(conn, "error %v\n", err)
				continue
			}
			if _, err := fmt.Fprintf(conn, "ok %s\n", f.Now().Format(time.RFC3339Nano)); err != nil {
				return
			}
		}
	}()
	return f, conn.Close, nil
}

func apply(f *Fake, line string) error {
	switch verb, arg, _ := strings.Cut(line, " "); verb {
	case "set":
		t, err := time.Parse(time.RFC3339Nano, arg)
		if err != nil {
			return err
		}
		f.Set(t)
	case "advance":
		d, err := time.ParseDuration(arg)
		if err != nil {
			return err
		}
		f.Advance(d)
	default:
		return errors.New("unknown line " + line)
	}
	return nil
}
