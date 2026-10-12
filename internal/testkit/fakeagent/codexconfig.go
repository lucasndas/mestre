package fakeagent

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// codexConfig is the part of $CODEX_HOME/config.toml the fake reads:
// [projects."<path>"] trust_level and [hooks.state."<key>"] trusted_hash.
// It is a line reader, not a TOML parser: enough for the tables Codex
// writes, and later tables win.
type codexConfig struct {
	projectTrust map[string]string
	hookTrust    map[string]string
}

func readCodexConfig(path string) (codexConfig, error) {
	cfg := codexConfig{projectTrust: map[string]string{}, hookTrust: map[string]string{}}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()
	var table, name string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			table, name = splitTable(line[1 : len(line)-1])
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val, err := strconv.Unquote(strings.TrimSpace(v))
		if err != nil {
			continue
		}
		switch k = strings.TrimSpace(k); {
		case table == "projects" && k == "trust_level":
			cfg.projectTrust[name] = val
		case table == "hooks.state" && k == "trusted_hash":
			cfg.hookTrust[name] = val
		}
	}
	if err := sc.Err(); err != nil {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	return cfg, nil
}

// splitTable splits `projects."/a/b"` into ("projects", "/a/b").
func splitTable(h string) (string, string) {
	i := strings.Index(h, `."`)
	if i < 0 {
		return h, ""
	}
	name, err := strconv.Unquote(h[i+1:])
	if err != nil {
		return h, ""
	}
	return h[:i], name
}

func appendCodexConfig(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create folder of %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := f.WriteString("\n" + text); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}
