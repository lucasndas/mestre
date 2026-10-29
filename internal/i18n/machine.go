package i18n

import (
	"os"
	"strings"
)

// MachineLanguage is the language of the machine for the CLI of a person
// (docs/plataforma/idioma-por-canal.md#cli): on Linux the first variable not
// empty of LC_ALL, LC_MESSAGES and LANG; on Windows the language of the
// interface of the user. Always one of the languages of the catalogs.
func MachineLanguage() string { return MachineLanguageFrom(os.LookupEnv, nil) }

// MachineLanguageFrom is MachineLanguage with the sources given, so that a test never
// changes the ones of the process: lookupEnv (os.LookupEnv in the binary) is what Linux
// reads, and uiTag gives the tag of the language of the interface ("en-US") that Windows
// reads; nil is the interface of the user, as the binary asks it. Each system ignores the
// source of the other.
func MachineLanguageFrom(lookupEnv func(string) (string, bool), uiTag func() string) string {
	return Match(machineTag(lookupEnv, uiTag))
}

// envTag is the first variable not empty of LC_ALL, LC_MESSAGES and LANG.
func envTag(lookupEnv func(string) (string, bool)) string {
	if lookupEnv == nil {
		return ""
	}
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v, ok := lookupEnv(k); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
