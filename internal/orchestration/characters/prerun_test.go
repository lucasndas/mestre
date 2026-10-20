package characters

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf16"
)

// A command of a pre-run runs as it was decided on: as it is under sh, and as the
// text PowerShell is given on Windows, whatever it holds of quotes and operators.
func TestACommandOfAPreRunRunsInTheShellItWasDecidedFor(t *testing.T) {
	t.Parallel()
	cmd := `echo "a; b" | Out-String; $x = 'é'`
	if got, err := shellLine("linux", cmd); err != nil || got != cmd {
		t.Errorf("on Linux: %q, %v", got, err)
	}
	got, err := shellLine("windows", cmd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := shellLine("windows", strings.Repeat("echo x; ", 500)); err == nil {
		t.Error("a command of 4000 characters was given to cmd, whose line is at most 8191")
	}
	// the program is the PowerShell of the system by its full path, quoted: one
	// planted in the worktree is not the one that runs
	enc, ok := strings.CutPrefix(got, `"`+systemPowerShell()+`" -NoProfile -NonInteractive -EncodedCommand `)
	if !ok || !strings.HasSuffix(systemPowerShell(), `\WindowsPowerShell\v1.0\powershell.exe`) || strings.ContainsAny(enc, ` "&|<>^%`) {
		t.Fatalf("on Windows: %q", got)
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(raw)%2 != 0 {
		t.Fatal(err)
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	if string(utf16.Decode(u)) != cmd {
		t.Errorf("the command PowerShell is given is %q", string(utf16.Decode(u)))
	}
}
