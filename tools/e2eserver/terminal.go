package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/privdir"
	"github.com/mestre-ia/mestre/internal/terminal"
	"github.com/mestre-ia/mestre/internal/terminal/ptyd"
	"github.com/mestre-ia/mestre/internal/termstream"
	"github.com/mestre-ia/mestre/internal/testkit/fakeagent"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The character and the terminal the smoke test of the browser opens: a fake
// Claude Code that really runs in the process of the server, its output kept in
// a buffer by offset like the mestre-pty of a machine does, so the panel, the
// socket, the replay and xterm.js are the real ones.
const (
	fakeCharacter = "e2e-dev"
	fakeTerminal  = "e2e-terminal-1"
)

// fakeTerminals is the panel.TerminalSource of the smoke test: one terminal, of
// the fake agent.
type fakeTerminals struct {
	mu   sync.Mutex
	data []byte
	wait chan struct{}
	em   ptyd.Emulator
	in   *io.PipeWriter
}

var _ panel.TerminalSource = (*fakeTerminals)(nil)

// startFakeTerminal runs the fake agent in dir until ctx ends.
func startFakeTerminal(ctx context.Context, dir string) (*fakeTerminals, error) {
	work, home, cfg := filepath.Join(dir, "work"), filepath.Join(dir, "home"), filepath.Join(dir, "cfg")
	files := map[string]string{
		"work/.git/HEAD":        "ref: refs/heads/main\n",
		"home/.keep":            "",
		"cfg/.credentials.json": "{}",
		"cfg/.claude.json":      fmt.Sprintf(`{"numStartups": 3, "projects": {%q: {"hasTrustDialogAccepted": true}}}`, filepath.ToSlash(work)),
	}
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := privdir.Ensure(filepath.Dir(path)); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			return nil, err
		}
	}
	pr, pw := io.Pipe()
	f := &fakeTerminals{wait: make(chan struct{}), in: pw, em: ptyd.NewVTEmulator(200, 50, 100, nil)}
	go func() {
		_ = fakeagent.Run(ctx, fakeagent.Config{
			Provider: fakeagent.Claude, Dir: work, In: pr, Out: f, Clock: clock.Real{},
			Env: []string{"HOME=" + home, "MESTRE_AGENT_ID=" + fakeCharacter, "CLAUDE_CONFIG_DIR=" + cfg},
		})
		_ = pw.Close()
	}()
	go func() {
		<-ctx.Done()
		_ = pr.Close()
	}()
	return f, nil
}

// Write is the output of the agent: the buffer, the emulator, and whoever waits.
func (f *fakeTerminals) Write(p []byte) (int, error) {
	f.mu.Lock()
	f.data = append(f.data, p...)
	f.em.Write(p)
	close(f.wait)
	f.wait = make(chan struct{})
	f.mu.Unlock()
	return len(p), nil
}

func (f *fakeTerminals) info() panel.TerminalInfo {
	return panel.TerminalInfo{TerminalID: fakeTerminal, Character: fakeCharacter, Name: "Dev-1", Machine: "e2e", State: "idle", Signal: true, Workspace: fakeWorkspace, Table: fakeTable}
}

// ByCharacter implements panel.TerminalSource.
func (f *fakeTerminals) ByCharacter(_ context.Context, id string) (panel.TerminalInfo, error) {
	if id != fakeCharacter {
		return panel.TerminalInfo{}, panel.ErrNoTerminal
	}
	return f.info(), nil
}

// Info implements panel.TerminalSource.
func (f *fakeTerminals) Info(_ context.Context, id string) (panel.TerminalInfo, error) {
	if id != fakeTerminal {
		return panel.TerminalInfo{}, panel.ErrNoTerminal
	}
	return f.info(), nil
}

// Replay and WaitOutput make it the buffer of a machine (termstream.Source).
func (f *fakeTerminals) Replay(_ context.Context, _ string, from uint64) (terminal.Replayed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := terminal.Replayed{Offset: from, End: uint64(len(f.data))}
	if from < uint64(len(f.data)) {
		r.Data = append([]byte(nil), f.data[from:]...)
	}
	return r, nil
}

func (f *fakeTerminals) WaitOutput(ctx context.Context, _ string, after uint64) error {
	for {
		f.mu.Lock()
		more, ch := uint64(len(f.data)) > after, f.wait
		f.mu.Unlock()
		if more {
			return nil
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type sinkOf struct{ s panel.TerminalSink }

func (k sinkOf) Chunk(_ context.Context, off uint64, d []byte) error { k.s.Output(off, d); return nil }
func (k sinkOf) Replayed(_ context.Context, from, to uint64, lost bool) error {
	k.s.Replayed(from, to, lost)
	return nil
}

// Follow implements panel.TerminalSource.
func (f *fakeTerminals) Follow(_ context.Context, id string, from uint64, sink panel.TerminalSink) (func(), error) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = termstream.Run(ctx, f, id, from, sinkOf{sink}) }()
	return cancel, nil
}

// Screen implements panel.TerminalSource.
func (f *fakeTerminals) Screen(context.Context, string) (string, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.em.Screen(), uint64(len(f.data)), nil
}

// Type implements panel.TerminalSource: the keys go to the input of the agent.
func (f *fakeTerminals) Type(ctx context.Context, _ string, data []byte) error {
	done := make(chan error, 1)
	go func() { _, err := f.in.Write(data); done <- err }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-clock.Real{}.NewTimer(5 * time.Second).C():
		return panel.ErrNoSignal
	}
}
