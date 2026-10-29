//go:build !windows

package i18n

func machineTag(lookupEnv func(string) (string, bool), _ func() string) string {
	return envTag(lookupEnv)
}
