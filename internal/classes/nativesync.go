package classes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"slices"
	"sync"

	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/safefile"
)

// NativeFiles writes the native control files. Update reads the file (nil
// when it does not exist), merges and writes it whole when the content
// changes, and says whether it did.
type NativeFiles interface {
	Update(ctx context.Context, path string, merge func(cur []byte) ([]byte, error)) (changed bool, err error)
}

// OSNativeFiles writes the native files on disk, through internal/safefile:
// they are in folders the account of the agents writes (the worktree, the
// account profile), so a link or a junction anywhere on the path, or a file
// that is not a regular one, is refused instead of followed
// (docs/arquitetura/seguranca.md: the engine validates what it reads from
// the shared folders), and the new file is a temporary one renamed over the
// old. Its mode is always safefile.Mode with the group of the folder,
// readable by the agents' account and never a copy of the mode of what was
// there: an old file with another mode is written again even when its
// content is the same (no chmod by path). One update at a time, as the
// settings.json of an Antigravity profile is shared by its characters.
type OSNativeFiles struct{ mu sync.Mutex }

// nativeAttempts is how many times Update reads, merges and writes again a
// file the CLI rewrote between the reading and the rename.
const nativeAttempts = 3

var errNativeRewritten = errors.New("rewritten while it was being written")

// Update implements NativeFiles.
func (o *OSNativeFiles) Update(_ context.Context, path string, merge func([]byte) ([]byte, error)) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for range nativeAttempts {
		cur, info, err := safefile.Read(path, safefile.DefaultLimit)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			cur = nil
		case err != nil:
			return false, err
		}
		next, err := merge(cur)
		if err != nil {
			return false, err
		}
		same := cur != nil && bytes.Equal(cur, next)
		if same && (runtime.GOOS == "windows" || info.Perm == safefile.Mode) {
			return false, nil
		}
		err = safefile.Write(path, next, safefile.Options{Guard: func(now safefile.Info) error {
			if !now.Same(info) {
				return errNativeRewritten
			}
			return nil
		}})
		switch {
		case errors.Is(err, errNativeRewritten):
			continue
		case err != nil:
			return false, err
		}
		return !same, nil
	}
	return false, fmt.Errorf("%s: %w, %d times", path, errNativeRewritten, nativeAttempts)
}

// Restarter restarts a character with the resume of its session
// (docs/provedores/adaptador.md#retomada-de-sessão), building its launch
// again (so through NativeSync.Prepare). The characters implement it.
type Restarter interface {
	RestartResume(ctx context.Context, agentID string) error
}

// Activity says whether a character is in the middle of a turn.
type Activity interface {
	Busy(agentID string) bool
}

// RestartQueue holds the restarts with resume until each character is free:
// a turn is never cut for them.
type RestartQueue struct {
	Restarter Restarter
	Activity  Activity

	mu      sync.Mutex
	pending map[string]bool
}

// Schedule asks for a restart: at once when the character is free, or at
// the end of its turn (Free). It says whether it restarted now.
func (q *RestartQueue) Schedule(ctx context.Context, agentID string) (bool, error) {
	q.mu.Lock()
	if q.pending == nil {
		q.pending = map[string]bool{}
	}
	q.pending[agentID] = true
	q.mu.Unlock()
	if q.Activity.Busy(agentID) {
		return false, nil
	}
	return q.Free(ctx, agentID)
}

// Free is the end of a turn of the character: a pending restart runs, once.
func (q *RestartQueue) Free(ctx context.Context, agentID string) (bool, error) {
	q.mu.Lock()
	if !q.pending[agentID] {
		q.mu.Unlock()
		return false, nil
	}
	delete(q.pending, agentID)
	q.mu.Unlock()
	if err := q.Restarter.RestartResume(ctx, agentID); err != nil {
		return false, fmt.Errorf("restart of %s: %w", agentID, err)
	}
	return true, nil
}

// Pending reports whether a restart waits for the character.
func (q *RestartQueue) Pending(agentID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pending[agentID]
}

// NativeSync keeps the native controls of the characters in step with their
// effective rules (docs/classes/reforco-nativo.md#quando-as-regras-mudam).
// The settings of Claude Code, the flags of Codex and the agent file of
// Antigravity are read only at the start, so a change restarts the
// character with resume once it is free; until then, the dispatcher already
// applies the new rules.
type NativeSync struct {
	Adapters interface {
		For(providers.ID) (providers.Adapter, error)
	}
	Files     NativeFiles
	Restarts  *RestartQueue
	Fallbacks providers.NativeFallbacks

	mu      sync.Mutex
	started map[string][]string // the native arguments each running character started with
}

// Prepare writes the native files of a character about to start (or
// restart) and returns the arguments that go in LaunchRequest.ProviderArgs.
// A file that cannot be written stops the start, with the reason.
func (s *NativeSync) Prepare(ctx context.Context, ch NativeCharacter, r *Rules) ([]string, error) {
	args, _, _, err := s.apply(ctx, ch, r)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.started == nil {
		s.started = map[string][]string{}
	}
	s.started[ch.AgentID] = slices.Clone(args)
	s.mu.Unlock()
	return args, nil
}

// Update takes new effective rules of a running character: the native files
// are written again and, when anything native changed (a file or the
// arguments it started with, unknown counting as changed), the restart with
// resume is scheduled for when it is free. It says whether something
// changed.
func (s *NativeSync) Update(ctx context.Context, ch NativeCharacter, r *Rules) (bool, error) {
	args, filesChanged, native, err := s.apply(ctx, ch, r)
	if err != nil || !native {
		return false, err
	}
	s.mu.Lock()
	was, known := s.started[ch.AgentID]
	s.mu.Unlock()
	if !filesChanged && known && slices.Equal(was, args) {
		return false, nil
	}
	if _, err := s.Restarts.Schedule(ctx, ch.AgentID); err != nil {
		return true, err
	}
	return true, nil
}

// Stopped forgets a character that ended.
func (s *NativeSync) Stopped(agentID string) {
	s.mu.Lock()
	delete(s.started, agentID)
	s.mu.Unlock()
}

// apply writes the native files; native is false for a terminal without an
// agent, which has no native controls.
func (s *NativeSync) apply(ctx context.Context, ch NativeCharacter, r *Rules) (args []string, changed, native bool, err error) {
	if r == nil || r.AgentID != ch.AgentID {
		return nil, false, false, fmt.Errorf("native controls of %s: rules of another character", ch.AgentID)
	}
	a, err := s.Adapters.For(ch.Provider)
	if err != nil {
		return nil, false, false, err
	}
	t, ok := providers.NativeOf(a)
	if !ok {
		return nil, false, false, nil
	}
	nc, err := t.Native(NativePolicy(r, ch, s.Fallbacks))
	if err != nil {
		return nil, false, true, err
	}
	for _, f := range nc.Files {
		c, err := s.Files.Update(ctx, f.Path, f.Merge)
		if err != nil {
			return nil, false, true, fmt.Errorf("native controls of %s: %s: %w", ch.AgentID, f.Path, err)
		}
		changed = changed || c
	}
	return nc.Args, changed, true, nil
}
