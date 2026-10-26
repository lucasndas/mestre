package fakepty_test

import (
	"io"
	"testing"

	"github.com/mestre-ia/mestre/internal/testkit/fakepty"
	"github.com/mestre-ia/mestre/pkg/clock"
)

func TestBothSidesTranscriptResizeAndHangup(t *testing.T) {
	t.Parallel()
	p := fakepty.New(clock.Real{}, 80, 24)
	term, proc := p.Terminal(), p.Process()
	_, _ = term.Write([]byte("ls\r"))
	buf := make([]byte, 3)
	if _, err := io.ReadFull(proc, buf); err != nil || string(buf) != "ls\r" {
		t.Fatalf("process read %q, %v", buf, err)
	}
	_, _ = proc.Write([]byte("file\r\n"))
	if p.Output() != "file\r\n" || p.Input() != "ls\r" {
		t.Fatalf("transcripts %q %q", p.Output(), p.Input())
	}
	_ = term.Resize(120, 40)
	if c, r := proc.Size(); c != 120 || r != 40 {
		t.Fatalf("size %dx%d", c, r)
	}
	_ = term.Close()
	if _, err := proc.Read(buf); err != io.EOF {
		t.Fatalf("read after hangup: %v", err)
	}
}

func TestProcessExitGivesEOFToTheTerminal(t *testing.T) {
	t.Parallel()
	p := fakepty.New(clock.Real{}, 80, 24)
	_, _ = p.Process().Write([]byte("bye"))
	_ = p.Process().Close()
	out, err := io.ReadAll(p.Terminal())
	if err != nil || string(out) != "bye" {
		t.Fatalf("terminal read %q, %v", out, err)
	}
}
