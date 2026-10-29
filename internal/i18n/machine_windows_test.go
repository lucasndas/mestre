//go:build windows

package i18n

import (
	"testing"

	"golang.org/x/sys/windows"
)

// On Windows the language is the one of the interface of the user, and the
// variables of the environment do not count.
func TestMachineLanguageIsTheInterfaceLanguageOnWindows(t *testing.T) {
	t.Parallel()
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil || len(langs) == 0 {
		t.Skipf("no preferred UI language: %v", err)
	}
	want := Match(langs[0])
	lookup := env(map[string]string{"LC_ALL": "xx", "LANG": "xx"})
	if got := MachineLanguageFrom(lookup, nil); got != want {
		t.Fatalf("MachineLanguageFrom = %q, want %q (UI language %q)", got, want, langs[0])
	}
}

// The tag of the interface is injectable, so that both languages are proved on any runner; the
// variables of the environment do not count, and an unknown or empty tag is pt-BR.
func TestMachineLanguageOnWindowsFollowsTheInjectedInterface(t *testing.T) {
	t.Parallel()
	lookup := env(map[string]string{"LC_ALL": "en_US.UTF-8", "LANG": "en_US.UTF-8"})
	for tag, want := range map[string]string{"en-US": En, "en-GB": En, "pt-BR": PtBR, "de-DE": PtBR, "": PtBR} {
		if got := MachineLanguageFrom(lookup, func() string { return tag }); got != want {
			t.Errorf("interface %q: %q, want %q", tag, got, want)
		}
	}
}
