package machinesheet_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/testkit/sheetfake"
)

// online builds the sheet of a fake machine, as the Citadel hands it out.
func online(t *testing.T, m *sheetfake.Machine, id, name string, up bool) machinesheet.Sheet {
	t.Helper()
	b, err := machinesheet.NewBuilder(m.Sources(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	s, err := b.Build(t.Context(), machinesheet.Sheet{})
	if err != nil {
		t.Fatal(err)
	}
	s.Name, s.Online = name, up
	return s
}

func parse(t *testing.T, raw map[string]any, scope machinesheet.Scope) machinesheet.Requirements {
	t.Helper()
	r, err := machinesheet.ParseRequires(raw, scope)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// CA-FMAQ-02
func TestNoMachineMeetsWindowsWithAGPUAndEachSaysWhatIsMissing(t *testing.T) {
	t.Parallel()
	linux := online(t, sheetfake.Linux("linux-pc"), "m1", "linux-pc", true)
	win := online(t, sheetfake.Windows("windows-pc"), "m2", "windows-pc", true)
	req := parse(t, map[string]any{"os": "windows", "gpu": true}, machinesheet.Scope{})

	c := machinesheet.Choose([]machinesheet.Sheet{linux, win}, req)
	if len(c.Fit()) != 0 {
		t.Fatalf("fit = %v", c.Fit())
	}
	if got := c.Verdicts[0]; got.Unmet != machinesheet.ReqOS || got.Reason != "o sistema é linux e o pedido é windows" {
		t.Errorf("linux-pc: %+v", got)
	}
	if got := c.Verdicts[1]; got.Unmet != machinesheet.ReqGPU || got.Reason != "sem GPU" {
		t.Errorf("windows-pc: %+v", got)
	}
	var none *machinesheet.NoMachineError
	if err := c.Err(); !errors.Is(err, machinesheet.ErrNoMachine) || !errors.As(err, &none) {
		t.Fatalf("Err = %v", err)
	}
	for _, want := range []string{"linux-pc: o sistema é linux", "windows-pc: sem GPU"} {
		if !strings.Contains(none.Error(), want) {
			t.Errorf("%q lacks %q", none.Error(), want)
		}
	}
}

func TestAMachineThatMeetsEverythingIsChosen(t *testing.T) {
	t.Parallel()
	linux := online(t, sheetfake.Linux("linux-pc"), "m1", "linux-pc", true)
	win := online(t, sheetfake.Windows("windows-pc"), "m2", "windows-pc", true)
	req := parse(t, map[string]any{"os": "linux", "gpu": true, "mem_min": 8 << 30, "provider": "claude-code"}, machinesheet.Scope{})
	c := machinesheet.Choose([]machinesheet.Sheet{win, linux}, req)
	fit := c.Fit()
	if len(fit) != 1 || fit[0].MachineID != "m1" || c.Err() != nil {
		t.Fatalf("fit = %v, err = %v", fit, c.Err())
	}
	if got := c.Verdicts[0].String(); got != "windows-pc: o sistema é windows e o pedido é linux" {
		t.Errorf("verdict = %q", got)
	}
}

func TestTheFirstRequirementNotMetIsTheOneReported(t *testing.T) {
	t.Parallel()
	m := sheetfake.Windows("windows-pc")
	s := online(t, m, "m2", "windows-pc", true)
	// memory, browser, transcription and the Operator all fail; the order of
	// the document decides
	req := parse(t, map[string]any{"mem_min": 64 << 30, "browser": true, "transcricao": true, "operador": true}, machinesheet.Scope{})
	steps := []struct{ unmet, reason string }{
		{machinesheet.ReqMemMin, "memória de 32.0 GiB, o mínimo é 64.0 GiB"},
		{machinesheet.ReqTranscription, "transcrição indisponível (whisper-server não verificado)"},
		{machinesheet.ReqOperator, "o Operador do PC não está habilitado"},
	}
	if v := machinesheet.Match(s, req); v.Unmet != steps[0].unmet || v.Reason != steps[0].reason {
		t.Errorf("verdict = %+v", v)
	}
	req.MemMin = 0
	if v := machinesheet.Match(s, req); v.Unmet != steps[1].unmet || v.Reason != steps[1].reason {
		t.Errorf("verdict = %+v", v)
	}
	req.Transcribes = false
	if v := machinesheet.Match(s, req); v.Unmet != steps[2].unmet || v.Reason != steps[2].reason {
		t.Errorf("verdict = %+v", v)
	}
	req.Operator = false
	if v := machinesheet.Match(s, req); !v.OK {
		t.Errorf("verdict = %+v", v)
	}
	// the Linux machine has no browser
	l := online(t, sheetfake.Linux("linux-pc"), "m1", "linux-pc", true)
	if v := machinesheet.Match(l, machinesheet.Requirements{Browser: true}); v.Unmet != machinesheet.ReqBrowser || v.Reason != "sem navegador" {
		t.Errorf("verdict = %+v", v)
	}
}

// CA-FMAQ-03: the match half. The sheets of characters, with `agent` and
// `account`, and the registration of the profiles come with milestone 4.
func TestAProviderInstalledButNotLoggedInOrNotConformantDoesNotMeet(t *testing.T) {
	t.Parallel()
	profile := func(logged, conformant bool) machinesheet.Sheet {
		m := sheetfake.Windows("windows-pc")
		m.SetProviders(machinesheet.Provider{Provider: machinesheet.ProviderCodex, Profiles: []machinesheet.Profile{
			{Name: "codex-pessoal", Version: "0.9.0", LoggedIn: logged, Conformant: conformant},
			{Name: "codex-outro", Version: "0.9.0", LoggedIn: true, Conformant: true},
		}})
		return online(t, m, "m2", "windows-pc", true)
	}
	req, err := machinesheet.Requirements{}.WithAgent("codex", "codex-pessoal")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name            string
		logged, conform bool
		ok              bool
		reason          string
	}{
		{"not logged in", false, true, false, "windows-pc: perfil de conta `codex-pessoal` não está logado"},
		{"logged in, not conformant", true, false, false, "windows-pc: perfil de conta `codex-pessoal` não é conforme na versão 0.9.0"},
		{"logged in and conformant", true, true, true, "windows-pc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := machinesheet.Match(profile(tt.logged, tt.conform), req)
			if v.OK != tt.ok || v.String() != tt.reason {
				t.Errorf("verdict = %q (ok %v), want %q", v, v.OK, tt.reason)
			}
		})
	}
	t.Run("the provider is not installed", func(t *testing.T) {
		t.Parallel()
		v := machinesheet.Match(online(t, sheetfake.Linux("linux-pc"), "m1", "linux-pc", true), req)
		if v.OK || v.String() != "linux-pc: o provedor `codex` não está instalado" {
			t.Errorf("verdict = %q", v)
		}
	})
	t.Run("the profile does not exist on the machine", func(t *testing.T) {
		t.Parallel()
		r, _ := machinesheet.Requirements{}.WithAgent("codex", "codex-trabalho")
		if v := machinesheet.Match(profile(true, true), r); v.OK || v.String() != "windows-pc: perfil de conta `codex-trabalho` não existe" {
			t.Errorf("verdict = %q", v)
		}
	})
	t.Run("without an account the default profile is used", func(t *testing.T) {
		t.Parallel()
		r, _ := machinesheet.Requirements{}.WithAgent("codex", "")
		// two profiles and none marked default: the machine has no default
		if v := machinesheet.Match(profile(true, true), r); v.OK || !strings.Contains(v.Reason, "perfil de conta padrão") {
			t.Errorf("verdict = %q", v)
		}
		// the stock machine has one profile marked default
		if v := machinesheet.Match(online(t, sheetfake.Windows("w"), "m2", "w", true), r); !v.OK {
			t.Errorf("verdict = %q", v)
		}
	})
}

// CA-FMAQ-04: the refusal when a requirement is saved; the class that calls
// it when it is saved is milestone 3.
func TestARequirementThatCannotBeMetIsRefusedWithTheReason(t *testing.T) {
	t.Parallel()
	t.Run("an interface outside the Operator base", func(t *testing.T) {
		t.Parallel()
		_, err := machinesheet.ParseRequires(map[string]any{"gui": true}, machinesheet.Scope{})
		if !errors.Is(err, machinesheet.ErrRequires) || !strings.Contains(err.Error(), "só vale na base `operador`") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("an unknown requirement lists the accepted ones", func(t *testing.T) {
		t.Parallel()
		_, err := machinesheet.ParseRequires(map[string]any{"cor": "azul"}, machinesheet.Scope{})
		if !errors.Is(err, machinesheet.ErrRequires) || !strings.Contains(err.Error(), "`cor` desconhecido") {
			t.Fatalf("err = %v", err)
		}
		for _, name := range machinesheet.AcceptedRequirements() {
			if !strings.Contains(err.Error(), "`"+name+"`") {
				t.Errorf("the message does not list %q: %v", name, err)
			}
		}
	})
	t.Run("an interface in the Operator base", func(t *testing.T) {
		t.Parallel()
		r := parse(t, map[string]any{"gui": true}, machinesheet.Scope{OperatorBase: true})
		if !r.GUI || !r.OperatorBase {
			t.Errorf("requirements = %+v", r)
		}
		win := online(t, sheetfake.Windows("windows-pc"), "m2", "windows-pc", true)
		if v := machinesheet.Match(win, r); !v.OK {
			t.Errorf("verdict = %q", v)
		}
		if v := machinesheet.Match(online(t, sheetfake.Linux("linux-pc"), "m1", "linux-pc", true), r); v.OK || v.Unmet != machinesheet.ReqGUI {
			t.Errorf("verdict = %q", v)
		}
	})
	t.Run("values of the wrong kind", func(t *testing.T) {
		t.Parallel()
		for name, raw := range map[string]map[string]any{
			"os":       {"os": "macos"},
			"gpu":      {"gpu": "sim"},
			"mem_min":  {"mem_min": "8GB"},
			"mem_min-": {"mem_min": -1},
			"provider": {"provider": "gemini"},
			"browser":  {"browser": 1},
		} {
			if _, err := machinesheet.ParseRequires(raw, machinesheet.Scope{}); !errors.Is(err, machinesheet.ErrRequires) {
				t.Errorf("%s: err = %v", name, err)
			}
		}
	})
	t.Run("the accepted ones parse", func(t *testing.T) {
		t.Parallel()
		r := parse(t, map[string]any{"os": "linux", "gpu": true, "mem_min": float64(8 << 30), "provider": "codex", "browser": true, "transcricao": true, "operador": true}, machinesheet.Scope{})
		want := machinesheet.Requirements{OS: "linux", GPU: true, MemMin: 8 << 30, Provider: "codex", Browser: true, Transcribes: true, Operator: true}
		if r != want {
			t.Errorf("requirements = %+v, want %+v", r, want)
		}
	})
}

// CA-FMAQ-05
func TestAMachineOfflineDoesNotMeetEvenIfItWould(t *testing.T) {
	t.Parallel()
	off := online(t, sheetfake.Linux("linux-pc"), "m1", "linux-pc", false)
	v := machinesheet.Match(off, machinesheet.Requirements{OS: "linux"})
	if v.OK || v.Unmet != machinesheet.UnmetOffline || v.String() != "linux-pc: offline" {
		t.Errorf("verdict = %q", v)
	}
	if v := machinesheet.Match(online(t, sheetfake.Linux("linux-pc"), "m1", "linux-pc", true), machinesheet.Requirements{OS: "linux"}); !v.OK {
		t.Errorf("the same machine online: %q", v)
	}
}

func TestTheAgentBecomesTheProviderRequirementAndTheThreeSourcesAdd(t *testing.T) {
	t.Parallel()
	base := parse(t, map[string]any{"os": "linux", "mem_min": 4 << 30}, machinesheet.Scope{})
	class := parse(t, map[string]any{"mem_min": 16 << 30, "browser": true}, machinesheet.Scope{})
	sheet, err := machinesheet.Requirements{}.WithAgent("claude-code", "claude-trabalho")
	if err != nil {
		t.Fatal(err)
	}
	all, err := machinesheet.Combine(base, sheet, class)
	if err != nil {
		t.Fatal(err)
	}
	want := machinesheet.Requirements{OS: "linux", MemMin: 16 << 30, Browser: true, Provider: "claude-code", Account: "claude-trabalho"}
	if all != want {
		t.Errorf("combined = %+v, want %+v", all, want)
	}
	// two systems cannot both be met, and an agent against another provider
	if _, err := machinesheet.Combine(base, parse(t, map[string]any{"os": "windows"}, machinesheet.Scope{})); !errors.Is(err, machinesheet.ErrRequires) {
		t.Errorf("os clash = %v", err)
	}
	withProvider := parse(t, map[string]any{"provider": "codex"}, machinesheet.Scope{})
	if _, err := withProvider.WithAgent("claude-code", ""); !errors.Is(err, machinesheet.ErrRequires) {
		t.Errorf("provider clash = %v", err)
	}
	if r, err := withProvider.WithAgent("codex", ""); err != nil || r.Provider != "codex" {
		t.Errorf("repeating the agent is not an error: %+v, %v", r, err)
	}
}

func TestNoMachinesInTheGuildIsAlsoARefusal(t *testing.T) {
	t.Parallel()
	err := machinesheet.Choose(nil, machinesheet.Requirements{}).Err()
	if !errors.Is(err, machinesheet.ErrNoMachine) || !strings.Contains(err.Error(), "não tem máquinas") {
		t.Errorf("err = %v", err)
	}
}
