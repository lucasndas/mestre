package providers

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf16"
)

// codexConfig is the configuration file of a Codex profile.
func codexConfig(profile string) string { return filepath.Join(profile, "config.toml") }

// PreTrust trusts the worktree in the config.toml of the profile:
// [projects."<worktree>"] trust_level = "trusted"
// (docs/provedores/codex.md#confiança-do-projeto). Only the worktree, which
// is the folder the character runs in and comes first in the lookup of
// Codex; never the resolved git root (RepoRoot), which for a worktree of the
// mirror is the mirror and would trust every worktree of it. The rest of
// the file stays as it was, and a second run changes nothing. Codex
// rewrites this file, so the write is atomic, done only over the content
// read, and done again when Codex rewrote it meanwhile; the edits of this
// process go one at a time, so two characters of a profile never write it
// together.
func (c *codex) PreTrust(req PreTrustRequest) (Trust, error) {
	if !absolute(req.Worktree) {
		return Trust{}, fmt.Errorf("codex trust: the worktree %q is not absolute", req.Worktree)
	}
	if req.Profile == "" {
		return Trust{}, errors.New("codex trust: no account profile folder (CODEX_HOME)")
	}
	if st, err := os.Stat(req.Profile); err != nil || !st.IsDir() {
		return Trust{}, fmt.Errorf("codex trust: the account profile folder %q does not exist", req.Profile)
	}
	worktree := filepath.Clean(req.Worktree)
	path := codexConfig(req.Profile)
	trustMu.Lock()
	defer trustMu.Unlock()
	for range trustAttempts {
		cur, before, err := readFileStat(path)
		if err != nil {
			return Trust{}, fmt.Errorf("codex trust: %w", err)
		}
		next, err := setTOMLKey(cur, []string{"projects", worktree}, "trust_level", `"trusted"`)
		if err != nil {
			return Trust{}, fmt.Errorf("%w: %s: %v", ErrNativeFile, path, err)
		}
		if before.Exists && bytes.Equal(cur, next) {
			return Trust{Path: path}, nil
		}
		if err := replaceFile(path, next, before, c.noFlush); err != nil {
			if errors.Is(err, errRewritten) {
				continue
			}
			return Trust{}, fmt.Errorf("codex trust: %w", err)
		}
		return Trust{Path: path, Trusted: []string{worktree}, Changed: true}, nil
	}
	return Trust{}, fmt.Errorf("codex trust: %s was rewritten while the trust was being written, %d times", path, trustAttempts)
}

// codexMaxLayerArg bounds, on Windows, the -c argument of the Mestre layer:
// the whole command line has at most 32,767 characters and the rest of the
// flags needs room. The size of MESTRE.md is controlled well below it
// (docs/sessao-zero/personagem.md, 8,000 characters at most).
const codexMaxLayerArg = 28_000

// DeliverMestreLayer reads MESTRE.md and delivers it as
// -c developer_instructions="<content>", a TOML basic string that Codex
// reads back as the same text (docs/provedores/codex.md#camada-do-mestre).
// It becomes a developer message that adds to the built-in instructions.
// The skills of the character are a section of MESTRE.md itself: Codex has
// no folder of skills per character.
func (c *codex) DeliverMestreLayer(req LayerRequest) (Layer, error) {
	if !absolute(req.Path) {
		return Layer{}, fmt.Errorf("codex mestre layer: %q is not an absolute path", req.Path)
	}
	b, err := readShared(req.Path)
	if err != nil {
		return Layer{}, fmt.Errorf("codex mestre layer: %w", err)
	}
	value, err := tomlString(string(b))
	if err != nil {
		return Layer{}, fmt.Errorf("codex mestre layer %s: %w", req.Path, err)
	}
	arg := "developer_instructions=" + value
	if machineOS(req.OS) == "windows" {
		if n := windowsArgLen(arg); n > codexMaxLayerArg {
			return Layer{}, fmt.Errorf("codex mestre layer %s: %d characters on the Windows command line, at most %d", req.Path, n, codexMaxLayerArg)
		}
	}
	return Layer{Args: []string{"-c", arg}}, nil
}

// windowsArgLen is how many UTF-16 units arg takes on a Windows command
// line, quoted the way Go quotes it (syscall.EscapeArg): backslashes before
// a quote doubled, the quote escaped, and quotes around an argument with a
// blank or a quote.
func windowsArgLen(arg string) int {
	n, slashes, quote := 0, 0, false
	for _, r := range arg {
		switch r {
		case '\\':
			slashes++
			n++
			continue
		case '"':
			n += slashes + 1
			quote = true
		case ' ', '\t':
			quote = true
		}
		slashes = 0
		n += len(utf16.Encode([]rune{r}))
	}
	if quote || arg == "" {
		n += 2 + slashes
	}
	return n
}

// Untrust takes the worktree out of the config.toml of the profile (when the
// worktree is removed, and at the end of the conformance test); the rest of
// the file stays as it was.
func (c *codex) Untrust(req PreTrustRequest) (Trust, error) {
	if !absolute(req.Worktree) || req.Profile == "" {
		return Trust{}, fmt.Errorf("codex untrust: the worktree %q or the profile %q is missing", req.Worktree, req.Profile)
	}
	worktree := filepath.Clean(req.Worktree)
	path := codexConfig(req.Profile)
	trustMu.Lock()
	defer trustMu.Unlock()
	for range trustAttempts {
		cur, before, err := readFileStat(path)
		if err != nil {
			return Trust{}, fmt.Errorf("codex untrust: %w", err)
		}
		if !before.Exists {
			return Trust{Path: path}, nil
		}
		next, err := removeTOMLTable(cur, []string{"projects", worktree})
		if err != nil {
			return Trust{}, fmt.Errorf("%w: %s: %v", ErrNativeFile, path, err)
		}
		if bytes.Equal(cur, next) {
			return Trust{Path: path}, nil
		}
		if err := replaceFile(path, next, before, c.noFlush); err != nil {
			if errors.Is(err, errRewritten) {
				continue
			}
			return Trust{}, fmt.Errorf("codex untrust: %w", err)
		}
		return Trust{Path: path, Cleared: []string{worktree}, Changed: true}, nil
	}
	return Trust{}, fmt.Errorf("codex untrust: %s was rewritten while the trust was being removed, %d times", path, trustAttempts)
}

var _ TrustRemover = (*codex)(nil)
