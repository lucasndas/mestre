package providers

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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

// Untrust takes the worktree's entry out of the trust file: the worktree was
// removed, or the conformance test ended.
func (c *claudeCode) Untrust(req PreTrustRequest) (Trust, error) {
	return c.editTrust(req, func(projects map[string]any) (_, cleared []string) {
		wt := entryKey(req.OS, projects, req.Worktree)
		if _, ok := projects[wt]; ok {
			delete(projects, wt)
			cleared = append(cleared, wt)
		}
		return nil, cleared
	}, func(projects map[string]any) bool {
		_, ok := projects[entryKey(req.OS, projects, req.Worktree)]
		return !ok
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
type fileState struct {
	exists bool
	size   int64
	mod    time.Time
}

var errRewritten = errors.New("file rewritten meanwhile")

func readFileStat(path string) ([]byte, fileState, error) {
	st, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fileState{}, nil
	}
	if err != nil {
		return nil, fileState{}, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fileState{}, nil
	}
	if err != nil {
		return nil, fileState{}, err
	}
	return b, fileState{exists: true, size: st.Size(), mod: st.ModTime()}, nil
}

// replaceFile writes data over path through a temporary file renamed in
// place, with the mode the file had (0600 for a new one). If the file is not
// what the caller read any more, it does not write.
func replaceFile(path string, data []byte, was fileState) error {
	return replaceFileWith(path, data, was, 0)
}

// replaceFileWith is replaceFile with the permission bits in extra added to
// the mode: Antigravity's settings.json is read by the account of the agents,
// not the engine's.
func replaceFileWith(path string, data []byte, was fileState, extra os.FileMode) error {
	mode := os.FileMode(0o600)
	if was.exists {
		if st, err := os.Stat(path); err == nil {
			mode = st.Mode().Perm()
		}
	}
	mode |= extra
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".mestre-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(mode); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	now, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if was.exists {
			_ = os.Remove(name)
			return errRewritten
		}
	case err != nil:
		_ = os.Remove(name)
		return err
	case !was.exists || now.Size() != was.size || !now.ModTime().Equal(was.mod):
		_ = os.Remove(name)
		return errRewritten
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// entryKey is the key of projects that stands for folder: the one already
// there when it names the same folder (on Windows Claude Code may have
// written it with other slashes or case), else the folder as given.
func entryKey(goos string, projects map[string]any, folder string) string {
	folder = strings.TrimRight(folder, `/\`)
	if _, ok := projects[folder]; ok {
		return folder
	}
	for k := range projects {
		if sameFolder(goos, k, folder) {
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
