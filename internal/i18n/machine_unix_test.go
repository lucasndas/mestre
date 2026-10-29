//go:build !windows

package i18n

import "testing"

func TestMachineLanguageFromTheEnvironment(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		vars map[string]string
		want string
	}{
		"en":                {map[string]string{"LANG": "en_US.UTF-8"}, En},
		"pt":                {map[string]string{"LANG": "pt_BR.UTF-8"}, PtBR},
		"C":                 {map[string]string{"LANG": "C"}, PtBR},
		"POSIX":             {map[string]string{"LANG": "POSIX"}, PtBR},
		"German":            {map[string]string{"LANG": "de_DE.UTF-8"}, PtBR},
		"unset":             {nil, PtBR},
		"LC_ALL beats LANG": {map[string]string{"LC_ALL": "en_US.UTF-8", "LANG": "pt_BR.UTF-8"}, En},
	} {
		if got := MachineLanguageFrom(env(c.vars)); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}
