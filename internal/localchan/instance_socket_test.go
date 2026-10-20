package localchan_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/localchan"
)

// CA-CODEX-WIN-04
//
// The instance of the engine the CLI lets identify it comes from the socket
// of its endpoint, read the same way on every system; anything of another
// shape says nothing and the CLI falls back to the principal instance.
func TestTheInstanceIsReadFromTheSocketPath(t *testing.T) {
	t.Parallel()
	for sock, want := range map[string]string{
		`C:\ProgramData\Mestre\principal\run\cli.sock`:   "principal",
		`C:/ProgramData/Mestre/x/run/cli.sock`:           "x",
		`D:\Data\mestre\ci_1\run\cli.sock`:               "ci_1",
		`C:\ProgramData\Mestre\longer-name\run\cli.sock`: "",
		`C:\ProgramData\Mestre\Principal\run\cli.sock`:   "principal",
		`C:\ProgramData\Mestre\x\pty\cli.sock`:           "",
		`C:\ProgramData\Other\x\run\cli.sock`:            "",
		`/run/mestre/x/cli.sock`:                         "",
		`cli.sock`:                                       "",
		``:                                               "",
	} {
		got, ok := localchan.InstanceOfSocket(sock)
		if got != want || ok != (want != "") {
			t.Errorf("%q = %q, %v; want %q", sock, got, ok, want)
		}
	}
}
