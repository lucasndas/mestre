package i18n

import "testing"

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestEnvTagTakesTheFirstVariableNotEmpty(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		vars map[string]string
		want string
	}{
		"LC_ALL wins":             {map[string]string{"LC_ALL": "en_US.UTF-8", "LC_MESSAGES": "pt_BR", "LANG": "pt_BR.UTF-8"}, "en_US.UTF-8"},
		"empty LC_ALL is skipped": {map[string]string{"LC_ALL": "", "LC_MESSAGES": "en_GB", "LANG": "pt_BR"}, "en_GB"},
		"blank ones are skipped":  {map[string]string{"LC_ALL": " ", "LANG": "en_US"}, "en_US"},
		"LANG is the last resort": {map[string]string{"LANG": "en_US.UTF-8"}, "en_US.UTF-8"},
		"nothing":                 {nil, ""},
	} {
		if got := envTag(env(c.vars)); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	if envTag(nil) != "" {
		t.Error("a nil lookup must give no tag")
	}
}
