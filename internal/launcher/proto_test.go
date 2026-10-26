package launcher_test

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/launcher"
)

func TestValidateVersion(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"1.0.0", "0.9.0", "12.34.567", "1.0.0-rc.1", "1.0.0-beta"} {
		if err := launcher.ValidateVersion(v); err != nil {
			t.Errorf("%q: %v", v, err)
		}
	}
	for _, v := range []string{"", "1.0", "../1.0.0", "1.0.0/../..", `1.0.0\x`, "1.0.0 ", "latest", "1.0.0-", "v1.0.0", strings.Repeat("1", 70) + ".0.0"} {
		if err := launcher.ValidateVersion(v); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
}

func TestPTYBinary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "1.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "1.0.0", ptyName())
	if err := os.WriteFile(want, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := launcher.PTYBinary(dir, "1.0.0"); err != nil || got != want {
		t.Errorf("PTYBinary = %q, %v", got, err)
	}
	if _, err := launcher.PTYBinary(dir, "2.0.0"); err == nil {
		t.Error("a version that is not installed was found")
	}
	if err := os.MkdirAll(filepath.Join(dir, "3.0.0", ptyName()), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := launcher.PTYBinary(dir, "3.0.0"); err == nil {
		t.Error("a folder was taken for the binary")
	}
}

func TestFramesRoundTrip(t *testing.T) {
	t.Parallel()
	a, b := net.Pipe()
	ca, cb := launcher.NewConn(a), launcher.NewConn(b)
	defer ca.Close()
	defer cb.Close()
	go func() { _ = ca.WriteFrame([]byte(`{"hello":"world"}`), nil) }()
	got, files, err := cb.ReadFrame()
	if err != nil || string(got) != `{"hello":"world"}` || len(files) != 0 {
		t.Fatalf("ReadFrame = %q, %v, %v", got, files, err)
	}
}

func TestFramesLimits(t *testing.T) {
	t.Parallel()
	a, b := net.Pipe()
	ca, cb := launcher.NewConn(a), launcher.NewConn(b)
	defer ca.Close()
	defer cb.Close()
	if err := ca.WriteFrame(nil, nil); err == nil {
		t.Error("an empty frame was sent")
	}
	if err := ca.WriteFrame(make([]byte, 64<<10+1), nil); err == nil {
		t.Error("a frame over the limit was sent")
	}
	// A header that announces more than the limit is refused before reading.
	go func() { _, _ = a.Write([]byte{0x7f, 0xff, 0xff, 0xff}) }()
	if _, _, err := cb.ReadFrame(); err == nil {
		t.Error("a huge frame was accepted")
	}
}

func TestFramesEndOfStream(t *testing.T) {
	t.Parallel()
	a, b := net.Pipe()
	cb := launcher.NewConn(b)
	_ = a.Close()
	if _, _, err := cb.ReadFrame(); err == nil {
		t.Error("no error at the end of the stream")
	}
}
