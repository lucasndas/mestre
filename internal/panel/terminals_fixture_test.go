package panel_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/terminal/ptyd"
)

// replayFixture is the output of a terminal and the screen the emulator of the
// machine (the one behind `mestre check`) makes of it. The Vitest of the panel
// feeds the same bytes to xterm.js and compares the rows (CA-CANVAS-09).
type replayFixture struct {
	Cols   int      `json:"cols"`
	Rows   int      `json:"rows"`
	Data   string   `json:"data_base64"`
	Screen []string `json:"screen"`
}

func fixtureBytes() []byte {
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w("\x1b[2J\x1b[H")
	w("\x1b[1;38;5;208mMestre\x1b[0m  Guilda de teste\r\n")
	w("\x1b[2m────────────────────────────────────────\x1b[0m\r\n")
	for i := 1; i <= 12; i++ {
		w("\x1b[3%dmlinha %02d\x1b[0m: ação concluída com acentuação e símbolos ✓ → ©\r\n", i%7+1, i)
	}
	w("\x1b[20;5H\x1b[7m selecionado \x1b[0m")
	w("\x1b[22;1Hrascunho\x1b[2K\x1b[22;1Hlinha refeita")
	w("\x1b[24;10Hcoluna 10\x1b[24;1H\x1b[1Kapagou o começo")
	w("\x1b[26;1H┌──────┐\r\n│ caixa │\r\n└──────┘\r\n")
	w("\x1b[30;1H> ")
	return b.Bytes()
}

// Part of CA-CANVAS-09: the committed fixture is what the emulator of the machine
// makes of the bytes; UPDATE_FIXTURES=1 rewrites it. The criterion closes in the
// Vitest of the panel (web/src/terminal/stream.test.ts), which feeds the same
// bytes to xterm.js and compares the rows with the fixture.
func TestReplayFixtureIsWhatTheEmulatorMakes(t *testing.T) {
	t.Parallel()
	data := fixtureBytes()
	em := ptyd.NewVTEmulator(200, 50, 100, nil)
	defer em.Close()
	em.Write(data)
	fx := replayFixture{Cols: 200, Rows: 50, Data: base64.StdEncoding.EncodeToString(data), Screen: strings.Split(em.Screen(), "\n")}
	want, err := json.MarshalIndent(fx, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	path := filepath.Join("..", "..", "web", "src", "terminal", "testdata", "replay.json")
	if os.Getenv("UPDATE_FIXTURES") != "" {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the fixture is missing (UPDATE_FIXTURES=1 go test ./internal/panel -run ReplayFixture): %v", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), want) {
		t.Fatal("web/src/terminal/testdata/replay.json is stale: run with UPDATE_FIXTURES=1 and commit it")
	}
}
