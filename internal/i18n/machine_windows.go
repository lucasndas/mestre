//go:build windows

package i18n

import "golang.org/x/sys/windows"

// machineTag is the language of the interface of the user ("en-US"); the
// variables of the environment are not the way a Windows user chooses it.
func machineTag(_ func(string) (string, bool), uiTag func() string) string {
	if uiTag != nil {
		return uiTag()
	}
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil || len(langs) == 0 {
		return ""
	}
	return langs[0]
}
