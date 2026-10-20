package providers

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lucasndas/mestre/internal/safefile"
)

// trustAttempts is how many times PreTrust reads, merges and writes the
// trust file again when Claude Code rewrote it meanwhile
// (docs/provedores/claude-code.md#regras-e-erros).
const trustAttempts = 3

// trustMu serializes the edits of the trust files of this process: several
// tables open at once and share the account profile's .claude.json.
var trustMu sync.Mutex

// claudeTrustFile is the file of the trust of the folders of an account
// profile: .claude.json inside CLAUDE_CONFIG_DIR.
func claudeTrustFile(profile string) string { return filepath.Join(profile, ".claude.json") }

// PreTrust trusts the worktree in the .claude.json of the account profile:
// projects["<worktree>"].hasTrustDialogAccepted = true, and nothing else of
// the file changes. It never trusts the key of the mirror the worktree came
// from (answering "trust" on the screen would, and that trusts every
// worktree of the mirror): it takes hasTrustDialogAccepted off that key if
// it finds it. The write is atomic and checked by a new reading; Claude Code
// rewrites this file, so a lost race is done again, up to three times.
// Idempotent.
func (c *claudeCode) PreTrust(req PreTrustRequest) (Trust, error) {
	return c.editTrust(req, func(projects map[string]any) (trusted, cleared []string) {
		wt := entryKey(req.OS, projects, req.Worktree)
		entry, _ := projects[wt].(map[string]any)
		if entry == nil {
			entry = map[string]any{}
		}
		if entry["hasTrustDialogAccepted"] != true {
			entry["hasTrustDialogAccepted"] = true
			projects[wt] = entry
			trusted = append(trusted, wt)
		}
		if req.RepoRoot != "" && !sameFolder(req.OS, req.RepoRoot, req.Worktree) {
			mirror := entryKey(req.OS, projects, req.RepoRoot)
			if m, ok := projects[mirror].(map[string]any); ok {
				if _, has := m["hasTrustDialogAccepted"]; has {
					delete(m, "hasTrustDialogAccepted")
					cleared = append(cleared, mirror)
				}
			}
		}
		return trusted, cleared
	}, func(projects map[string]any) bool {
		wt, _ := projects[entryKey(req.OS, projects, req.Worktree)].(map[string]any)
		if wt["hasTrustDialogAccepted"] != true {
			return false
		}
		if req.RepoRoot == "" || sameFolder(req.OS, req.RepoRoot, req.Worktree) {
			return true
		}
		m, _ := projects[entryKey(req.OS, projects, req.RepoRoot)].(map[string]any)
		_, has := m["hasTrustDialogAccepted"]
		return !has
	})
}

// Untrust takes the worktree's entries out of the trust file, in any writing
// of the folder (on Windows also the one with backslashes an older Mestre
// wrote): the worktree was removed, or the conformance test ended.
func (c *claudeCode) Untrust(req PreTrustRequest) (Trust, error) {
	return c.editTrust(req, func(projects map[string]any) (_, cleared []string) {
		for k := range projects {
			if sameFolder(req.OS, k, req.Worktree) {
				delete(projects, k)
				cleared = append(cleared, k)
			}
		}
		return nil, cleared
	}, func(projects map[string]any) bool {
		for k := range projects {
			if sameFolder(req.OS, k, req.Worktree) {
				return false
			}
		}
		return true
	})
}

var _ TrustRemover = (*claudeCode)(nil)

// editTrust reads the trust file, applies edit to its projects, writes it
// when edit changed something and reads it again to check with done. A file
// that is not a JSON object is an error and is not touched.
func (c *claudeCode) editTrust(req PreTrustRequest, edit func(projects map[string]any) (trusted, cleared []string), done func(projects map[string]any) bool) (Trust, error) {
	if !absolute(req.Worktree) {
		return Trust{}, fmt.Errorf("claude-code trust: the worktree %q is not absolute", req.Worktree)
	}
	if req.Profile == "" {
		return Trust{}, errors.New("claude-code trust: no account profile folder (CLAUDE_CONFIG_DIR)")
	}
	if st, err := os.Stat(req.Profile); err != nil || !st.IsDir() {
		return Trust{}, fmt.Errorf("claude-code trust: the account profile folder %q does not exist", req.Profile)
	}
	req.OS = machineOS(req.OS)
	path := claudeTrustFile(req.Profile)
	trustMu.Lock()
	defer trustMu.Unlock()

	out := Trust{Path: path}
	for range trustAttempts {
		cur, before, err := readFileStat(path)
		if err != nil {
			return Trust{}, fmt.Errorf("claude-code trust: %w", err)
		}
		obj, err := parseObject(cur, path)
		if err != nil {
			return Trust{}, err
		}
		projects, ok := obj["projects"].(map[string]any)
		if _, has := obj["projects"]; has && !ok {
			return Trust{}, fmt.Errorf("%w: %s: projects is not an object", ErrNativeFile, path)
		}
		if projects == nil {
			projects = map[string]any{}
		}
		trusted, cleared := edit(projects)
		if len(trusted) == 0 && len(cleared) == 0 {
			if done(projects) {
				return out, nil
			}
			continue
		}
		obj["projects"] = projects
		data, err := marshalObject(obj)
		if err != nil {
			return Trust{}, err
		}
		if err := replaceFile(path, data, before); err != nil {
			if errors.Is(err, errRewritten) {
				continue
			}
			return Trust{}, fmt.Errorf("claude-code trust: %w", err)
		}
		if c.afterTrustWrite != nil {
			c.afterTrustWrite(path)
		}
		out.Trusted, out.Cleared, out.Changed = append(out.Trusted, trusted...), append(out.Cleared, cleared...), true
		again, _, err := readFileStat(path)
		if err != nil {
			return Trust{}, fmt.Errorf("claude-code trust: %w", err)
		}
		check, err := parseObject(again, path)
		if err != nil {
			continue // rewritten half-way by Claude Code: read it again
		}
		p, _ := check["projects"].(map[string]any)
		if done(p) {
			return out, nil
		}
	}
	return Trust{}, fmt.Errorf("claude-code trust: %s was rewritten while the trust was being written, %d times", path, trustAttempts)
}

// fileState is what tells that a file was rewritten between a reading and a
// write.
type fileState = safefile.Info

var errRewritten = errors.New("file rewritten meanwhile")

// readFileStat reads a file of the account profile, which the account of the
// agents writes: through internal/safefile, so a link or a junction on the
// path and a file that is not regular are refused, never followed (an agent
// could otherwise point .claude.json at an engine file and have it copied
// into a file it reads). A missing file is nil and no error.
func readFileStat(path string) ([]byte, fileState, error) {
	b, st, err := safefile.Read(path, safefile.DefaultLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fileState{}, nil
	}
	if err != nil {
		return nil, fileState{}, err
	}
	return b, st, nil
}

// maxSharedFile bounds the files of the Mestre the engine reads back from a
// folder the agents write (MESTRE.md, the agent file of Antigravity).
const maxSharedFile = 1 << 20

// readShared reads such a file through safefile: an agent could put a link
// to an engine file in place of MESTRE.md, and the engine would copy that
// file into the agent file or the command line of the character.
func readShared(path string) ([]byte, error) {
	b, _, err := safefile.Read(path, maxSharedFile)
	return b, err
}

// replaceFile writes data over path through a temporary file renamed in
// place (safefile), with the mode of every native file (safefile.Mode and
// the group of the folder: the CLI, running as the account of the agents,
// reads and rewrites it; never the mode the old file had, which agy and
// Claude Code create 0600). If the file is not what the caller read any
// more, it does not write (errRewritten).
func replaceFile(path string, data []byte, was fileState) error {
	return safefile.Write(path, data, safefile.Options{Sync: true, Guard: func(now safefile.Info) error {
		if !now.Same(was) {
			return errRewritten
		}
		return nil
	}})
}

// entryKey is the key of projects that stands for folder: the one already
// there when it names the same folder (on Windows Claude Code may have
// written it with another case), else the folder as Claude Code writes it.
// On Windows that is with forward slashes: Claude Code 2.1.304 does not read
// a key with backslashes, and the trust screen shows up anyway (the Windows
// round, 2026-10-19), so such a key never stands for the folder.
func entryKey(goos string, projects map[string]any, folder string) string {
	folder = strings.TrimRight(folder, `/\`)
	if goos == "windows" {
		folder = strings.ReplaceAll(folder, `\`, "/")
	}
	if _, ok := projects[folder]; ok {
		return folder
	}
	for k := range projects {
		if (goos != "windows" || !strings.Contains(k, `\`)) && sameFolder(goos, k, folder) {
			return k
		}
	}
	return folder
}

// sameFolder compares two folders: after Clean on Linux; without difference
// of case or of slash on Windows.
func sameFolder(goos, a, b string) bool {
	a, b = strings.TrimRight(a, `/\`), strings.TrimRight(b, `/\`)
	if goos == "windows" {
		return strings.EqualFold(strings.ReplaceAll(a, `\`, "/"), strings.ReplaceAll(b, `\`, "/"))
	}
	return a == b
}
