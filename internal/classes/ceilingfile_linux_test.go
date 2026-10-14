package classes_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

// C6: teto.yaml is in the main clone, which the agents write. A link to an
// engine file, a FIFO (which would block the read) or a huge file is not
// read: the alert says what it is, without a byte of it, and the approved
// version holds.
func TestCeilingFileThatIsNotARegularFileIsAlertedWithoutItsContent(t *testing.T) {
	t.Parallel()
	secret := filepath.Join(t.TempDir(), "shadow")
	if err := os.WriteFile(secret, []byte("version: 1\nroot: $6$secret-hash\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, plant := range map[string]func(path string) error{
		"link": func(p string) error { return os.Symlink(secret, p) },
		"fifo": func(p string) error { return syscall.Mkfifo(p, 0o644) },
		"huge": func(p string) error { return os.WriteFile(p, make([]byte, classes.MaxCeilingBytes+1), 0o644) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), filepath.FromSlash(classes.CeilingFile))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := plant(path); err != nil {
				t.Fatal(err)
			}
			k := newKeeper(t)
			alerts := &alertLog{}
			k.File, k.Alerts = classes.CeilingPath(path), alerts
			st, err := k.Check(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if st.Alert == nil || st.Alert.Kind != classes.CeilingInvalid || st.Pending != nil {
				t.Fatalf("state = %+v", st)
			}
			if a := alerts.all(); len(a) != 1 || strings.Contains(a[0].Detail, "secret") || strings.Contains(a[0].Detail, "root") {
				t.Fatalf("alerts = %+v", a)
			}
			if err := k.ApprovePending(t.Context(), "x", classes.Authority{}); err == nil {
				t.Fatal("a version that cannot be read was approved")
			}
		})
	}
}
