package fakeagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// envMap turns KEY=VALUE pairs into a map (last one wins).
func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

// accountDir is the provider configuration folder of the account profile:
// CLAUDE_CONFIG_DIR, CODEX_HOME or the HOME of the profile.
func (a *agent) accountDir() string {
	home := a.env["HOME"]
	switch a.p {
	case Claude:
		if d := a.env["CLAUDE_CONFIG_DIR"]; d != "" {
			return d
		}
		return filepath.Join(home, ".claude")
	case Codex:
		if d := a.env["CODEX_HOME"]; d != "" {
			return d
		}
		return filepath.Join(home, ".codex")
	default:
		return filepath.Join(home, ".gemini", "antigravity-cli")
	}
}

func (a *agent) loggedIn() bool {
	f := profiles[a.p].credFile
	if f == "" {
		return true
	}
	_, err := os.Stat(filepath.Join(a.accountDir(), f))
	return err == nil
}

// gitRoot returns the nearest ancestor of dir with a .git entry, or "".
func gitRoot(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// trustCandidates are the paths whose trust entry counts for dir.
func trustCandidates(dir string) []string {
	c := []string{dir}
	if r := gitRoot(dir); r != "" && r != dir {
		c = append(c, r)
	}
	return c
}

// claudeTrustFile is .claude.json: inside CLAUDE_CONFIG_DIR when set,
// otherwise in HOME.
func (a *agent) claudeTrustFile() string {
	if d := a.env["CLAUDE_CONFIG_DIR"]; d != "" {
		return filepath.Join(d, ".claude.json")
	}
	return filepath.Join(a.env["HOME"], ".claude.json")
}

func (a *agent) agySettingsFile() string {
	return filepath.Join(a.accountDir(), "settings.json")
}

func readJSONMap(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}

func writeJSONMap(path string, m map[string]any) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create folder of %s: %w", path, err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// folderTrusted reads the same trust file the real CLI reads.
func (a *agent) folderTrusted() (bool, error) {
	if a.dir == a.env["HOME"] {
		return false, nil // the home folder is never trusted
	}
	switch a.p {
	case Claude:
		m, err := readJSONMap(a.claudeTrustFile())
		if err != nil {
			return false, err
		}
		projects, _ := m["projects"].(map[string]any)
		for _, c := range trustCandidates(a.dir) {
			if p, ok := projects[c].(map[string]any); ok && p["hasTrustDialogAccepted"] == true {
				return true, nil
			}
		}
		return false, nil
	case Codex:
		cfg, err := readCodexConfig(filepath.Join(a.accountDir(), "config.toml"))
		if err != nil {
			return false, err
		}
		for _, c := range trustCandidates(a.dir) {
			if cfg.projectTrust[c] == "trusted" {
				return true, nil
			}
		}
		return false, nil
	default:
		m, err := readJSONMap(a.agySettingsFile())
		if err != nil {
			return false, err
		}
		list, _ := m["trustedWorkspaces"].([]any)
		return slices.Contains(list, any(a.dir)), nil
	}
}

// trustFolder records the answer "trust" the way the real CLI does.
func (a *agent) trustFolder() error {
	switch a.p {
	case Claude:
		path := a.claudeTrustFile()
		m, err := readJSONMap(path)
		if err != nil {
			return err
		}
		projects, _ := m["projects"].(map[string]any)
		if projects == nil {
			projects = map[string]any{}
		}
		key := a.dir
		if r := gitRoot(a.dir); r != "" {
			key = r
		}
		entry, _ := projects[key].(map[string]any)
		if entry == nil {
			entry = map[string]any{}
		}
		entry["hasTrustDialogAccepted"] = true
		projects[key] = entry
		m["projects"] = projects
		return writeJSONMap(path, m)
	case Codex:
		return appendCodexConfig(filepath.Join(a.accountDir(), "config.toml"),
			fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", a.dir))
	default:
		path := a.agySettingsFile()
		m, err := readJSONMap(path)
		if err != nil {
			return err
		}
		list, _ := m["trustedWorkspaces"].([]any)
		m["trustedWorkspaces"] = append(list, a.dir)
		return writeJSONMap(path, m)
	}
}
