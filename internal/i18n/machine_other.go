//go:build !windows

package i18n

func machineTag(lookupEnv func(string) (string, bool)) string { return envTag(lookupEnv) }
