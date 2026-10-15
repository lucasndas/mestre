package sessionzero

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/safefile"
)

// remember keeps what a living character needs to be updated.
func (c *Character) remember(req CharacterRequest, p providers.ID, in LayerInput, l Layer, plan CharacterPlan, hookText string) {
	// hookText is the layer for the hook of the start; "" when the layer travels another way.
	c.mu.Lock()
	defer c.mu.Unlock()
	req.ProviderKey = nil // a secret is not kept
	c.state[req.ID] = &charState{req: req, input: in, layer: l, agentDir: plan.AgentDir, gitconfig: plan.GitConfig,
		provider: p, hookText: hookText}
}

// RefreshReport is what Refresh did.
type RefreshReport struct {
	// Changed are the sections of the layer that changed, by key.
	Changed []string
	// Written and Removed are the files of the character that changed,
	// relative to the worktree.
	Written, Removed []string
}

// Refresh rewrites the MESTRE.md of a living character when what goes into
// it changed: a decree that reaches it, the instructions of the users, the
// class or the quest. The file on disk has the new text at once; the
// character receives the sections that changed at the start of its next turn
// (Context, TakeUpdate), never in the middle of one
// (docs/sessao-zero/personagem.md#atualização-a-cada-turno).
func (c *Character) Refresh(_ context.Context, agentID string, d Dynamic) (RefreshReport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.state[agentID]
	if st == nil {
		return RefreshReport{}, fmt.Errorf("character %s: not prepared on this machine", agentID)
	}
	in := st.input
	in.Decrees, in.Instructions, in.Quest = d.Decrees, d.Instructions, d.Quest
	if d.ClassBody != "" {
		in.ClassBody = d.ClassBody
	}
	l, err := RenderLayer(in)
	if err != nil {
		return RefreshReport{}, err
	}
	var rep RefreshReport
	for _, s := range l.Sections {
		if s.Text != st.layer.Section(s.Key) {
			rep.Changed = append(rep.Changed, s.Key)
		}
	}
	if len(rep.Changed) == 0 && sameFiles(l.Files, st.layer.Files) {
		return rep, nil
	}
	rel := path.Join(MestreDir, "agents", agentID)
	if rep.Written, rep.Removed, err = writeLayer(st.req.Dir, rel, l); err != nil {
		return RefreshReport{}, fmt.Errorf("character %s: %w", agentID, err)
	}
	if st.pending == nil {
		st.pending = map[string]string{}
	}
	for _, k := range rep.Changed {
		st.pending[k] = l.Section(k)
	}
	st.input, st.layer = in, l
	return rep, nil
}

func sameFiles(a, b []File) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Path != b[i].Path || string(a[i].Data) != string(b[i].Data) {
			return false
		}
	}
	return true
}

// TakeUpdate gives the text of the sections that changed since the character
// last heard of them, once. It is what goes at the start of the next message
// the engine delivers to a provider without a hook of the start of the turn
// (reduced monitoring); "" when nothing changed.
func (c *Character) TakeUpdate(agentID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.take(c.state[agentID])
}

func (c *Character) take(st *charState) string {
	if st == nil || len(st.pending) == 0 {
		return ""
	}
	var parts []string
	for _, s := range st.layer.Sections {
		if t, ok := st.pending[s.Key]; ok {
			parts = append(parts, t)
		}
	}
	st.pending = nil
	return "Update of the Mestre layer. These sections changed and replace the ones you had:\n\n" + strings.Join(parts, "\n\n") + "\n"
}

// Contexts is the hook.Contexts of the characters of this machine: at the
// start of a session it gives the layer when it travels by the hook (the
// reserve of Codex and Antigravity), and at the start of each turn the
// sections that changed.
func (c *Character) Contexts() hook.Contexts { return characterContexts{c} }

type characterContexts struct{ c *Character }

func (x characterContexts) Context(_ context.Context, ev providers.Event) (string, error) {
	if ev.Event != providers.EventSessionStart && ev.Event != providers.EventTurnStart {
		return "", nil
	}
	x.c.mu.Lock()
	defer x.c.mu.Unlock()
	st := x.c.state[ev.AgentID]
	if st == nil {
		return "", nil
	}
	if st.hookText != "" {
		// The whole layer, as it is now: nothing is left to update.
		text := st.hookText
		st.hookText, st.pending = "", nil
		return text, nil
	}
	if ev.Event != providers.EventTurnStart {
		return "", nil
	}
	return x.c.take(st), nil
}

// writeLayer writes the MESTRE.md and the files that left it in
// <dir>/<rel>, and takes away the ones that came back into the text.
func writeLayer(dir, rel string, l Layer) (written, removed []string, err error) {
	files := append([]File{{Path: "MESTRE.md", Data: []byte(l.Text)}}, l.Files...)
	want := map[string]bool{}
	for _, f := range files {
		want[f.Path] = true
		changed, err := writeIfChanged(filepath.Join(dir, filepath.FromSlash(rel), f.Path), f.Data)
		if err != nil {
			return written, removed, fmt.Errorf("%s: %w", path.Join(rel, f.Path), err)
		}
		if changed {
			written = append(written, path.Join(rel, f.Path))
		}
	}
	for _, name := range []string{classFile, skillsFile, decreesFile} {
		if want[name] {
			continue
		}
		file := filepath.Join(dir, filepath.FromSlash(rel), name)
		if _, _, rerr := safefile.Read(file, 1<<20); rerr != nil {
			continue // not there
		}
		if err := safefile.Remove(file, nil); err != nil {
			return written, removed, fmt.Errorf("%s: %w", path.Join(rel, name), err)
		}
		removed = append(removed, path.Join(rel, name))
	}
	return written, removed, nil
}

// EndRequest says which character ended.
type EndRequest struct {
	ID string
	// Dir is the worktree of the character; Provider its provider.
	Dir      string
	Provider providers.ID
	// Final is the end for good (a final state of the character). A restart
	// with resume keeps everything: it is not an end.
	Final bool
}

// End is what the engine of the machine does when a character stops. For good, it
// deletes .mestre/agents/<id>/, the MESTRE_TMP of the character and, in
// Antigravity, .agents/agents/mestre-<id>.md, and forgets its birth; these
// files are in the exclusions of the worktree since the table opened, so git
// never listed them (docs/sessao-zero/personagem.md#ao-encerrar). A restart
// with resume keeps the files and MESTRE_TMP, and Prepare makes the files
// again if the class or the decrees changed.
func (c *Character) End(ctx context.Context, req EndRequest) error {
	if !validEndID(req.ID) || req.Dir == "" || !filepath.IsAbs(req.Dir) {
		return fmt.Errorf("end of %q: no valid id or worktree", req.ID)
	}
	c.mu.Lock()
	delete(c.state, req.ID)
	c.mu.Unlock()
	if !req.Final {
		return nil
	}
	errs := []error{
		removeTree(filepath.Join(req.Dir, MestreDir, "agents", req.ID)),
		removeTree(filepath.Join(c.o.Agents, req.ID, "tmp")),
	}
	if req.Provider == providers.Antigravity {
		errs = append(errs, safefile.Remove(providers.AntigravityAgentPath(req.Dir, req.ID), nil))
	}
	errs = append(errs, c.o.Births.Delete(ctx, req.ID))
	return errors.Join(errs...)
}

// pruneEmptyDirs removes the folders under root that have nothing in them
// (the folder of a skill whose files were taken away), through safefile; root
// itself stays. A link is left alone.
func pruneEmptyDirs(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Type()&fs.ModeSymlink != 0 || !e.IsDir() {
			continue
		}
		sub := filepath.Join(root, e.Name())
		pruneEmptyDirs(sub)
		if left, err := os.ReadDir(sub); err == nil && len(left) == 0 {
			_ = safefile.RemoveDir(sub)
		}
	}
}

func validEndID(id string) bool {
	return id != "" && id == filepath.Base(id) && id != "." && id != ".."
}

// removeTree deletes a folder and all that is in it, through safefile: the
// folder is one the agents write, so a link on the way is refused, and a link
// inside is taken away itself, never followed.
func removeTree(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		switch {
		case e.Type()&fs.ModeSymlink == 0 && e.IsDir():
			errs = append(errs, removeTree(p))
		case e.Type().IsRegular():
			errs = append(errs, safefile.Remove(p, nil))
		default: // a link, a junction or something that is not a file
			errs = append(errs, safefile.RemoveDir(p))
		}
	}
	errs = append(errs, safefile.RemoveDir(dir))
	return errors.Join(errs...)
}
