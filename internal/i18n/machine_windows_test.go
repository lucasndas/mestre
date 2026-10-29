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
	if got := MachineLanguageFrom(lookup); got != want {
		t.Fatalf("MachineLanguageFrom = %q, want %q (UI language %q)", got, want, langs[0])
	}
}
