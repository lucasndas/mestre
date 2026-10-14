package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/conformance"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

var (
	hookReview = regexp.MustCompile(`(?i)hooks? (need|needs|to) review|review hooks|trust all`)
	codexReady = regexp.MustCompile(`(?m)^[│| ]*›`)
)

// setupCodex is the trust of the Mestre hooks in a Codex profile, once: what
// the Owner does in /hooks when the panel sets the profile up.
func setupCodex(ctx context.Context, o options, out io.Writer, logger *slog.Logger) error {
	if providers.ID(o.provider) != providers.Codex {
		return fmt.Errorf("setup-codex is for -provider codex")
	}
	a, err := providers.NewRegistry(providers.Options{}).For(providers.Codex)
	if err != nil {
		return err
	}
	files, err := a.HookFiles(providers.HookFilesRequest{Root: o.profileDir, Binary: o.mestre})
	if err != nil {
		return err
	}
	var w classes.OSNativeFiles
	for _, f := range files {
		if _, err := w.Update(ctx, f.Path, f.Merge); err != nil {
			return err
		}
	}
	dir, err := os.MkdirTemp(o.tmp, "mconf-setup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	trust := providers.PreTrustRequest{Profile: o.profileDir, Worktree: dir}
	if _, err := a.PreTrust(trust); err != nil {
		return err
	}
	defer func() {
		if t, ok := a.(providers.TrustRemover); ok {
			_, _ = t.Untrust(trust)
		}
	}()
	if o.pty == "" {
		return fmt.Errorf("setup-codex needs -pty")
	}
	host, err := newHost(ctx, o, logger)
	if err != nil {
		return err
	}
	defer host.Close()
	env := append(baseEnv(o), "CODEX_HOME="+o.profileDir)
	sess, err := host.Start(ctx, conformance.StartSpec{ID: "setup-" + filepath.Base(dir), AgentID: "setup", Env: classes.Env{AgentID: "setup", Worktree: dir},
		Launch: providers.Launch{Path: o.cli, Args: []string{"--no-daemon", "-s", "read-only", "-a", "on-request"}, Env: env, Dir: dir}})
	if err != nil {
		return err
	}
	defer func() { _ = sess.Stop(context.WithoutCancel(ctx)) }()
	// The review comes a moment after the prompt is drawn: without it in
	// 30 s, the hooks were already trusted.
	screen, err := waitScreen(ctx, sess, 30*time.Second, hookReview.MatchString)
	fmt.Fprintf(out, "--- start screen ---\n%s\n", mask(screen))
	if err != nil && !codexReady.MatchString(screen) {
		return err
	}
	if hookReview.MatchString(screen) {
		downs := optionIndex(screen, "Trust all")
		if downs < 0 {
			return fmt.Errorf("the review of the hooks has no option \"Trust all\"")
		}
		keys := strings.Repeat("\x1b[B", downs) + "\r"
		if err := sess.Input(ctx, []byte(keys)); err != nil {
			return err
		}
		screen, err = waitScreen(ctx, sess, 60*time.Second, func(s string) bool { return !hookReview.MatchString(s) && codexReady.MatchString(s) })
		fmt.Fprintf(out, "--- after trusting ---\n%s\n", mask(screen))
		if err != nil {
			return err
		}
	} else {
		fmt.Fprintln(out, "no review of the hooks: they were already trusted")
	}
	b, _ := a.TypeMessage("/exit")
	_ = sess.Input(ctx, b)
	t := clock.Real{}.NewTimer(20 * time.Second)
	select {
	case <-sess.Ended():
	case <-t.C():
	}
	t.Stop()
	return compareTrust(o, out)
}

// compareTrust prints the hook state Codex wrote and whether each entry is
// the one the fallback CodexHookTrust computes.
func compareTrust(o options, out io.Writer) error {
	cfg, err := os.ReadFile(filepath.Join(o.profileDir, "config.toml"))
	if err != nil {
		return err
	}
	a, _ := providers.NewRegistry(providers.Options{}).For(providers.Codex)
	files, err := a.HookFiles(providers.HookFilesRequest{Root: o.profileDir, Binary: o.mestre,
		Fallbacks: providers.NativeFallbacks{CodexHookTrust: true}})
	if err != nil {
		return err
	}
	var want []byte
	for _, f := range files {
		if strings.HasSuffix(f.Path, "config.toml") {
			if want, err = f.Merge(nil); err != nil {
				return err
			}
		}
	}
	var lines []string
	for _, l := range strings.Split(string(cfg), "\n") {
		if strings.Contains(l, "hooks.state") || strings.Contains(l, "trusted_hash") {
			lines = append(lines, l)
		}
	}
	fmt.Fprintf(out, "--- hook state Codex wrote ---\n%s\n", strings.Join(lines, "\n"))
	matched, total := 0, 0
	for _, l := range strings.Split(string(want), "\n") {
		if strings.HasPrefix(l, "trusted_hash") {
			total++
			if strings.Contains(string(cfg), l) {
				matched++
			}
		}
	}
	fmt.Fprintf(out, "CodexHookTrust computes %d entries; %d of them are byte for byte what Codex wrote\n", total, matched)
	return nil
}

func optionIndex(screen, option string) int {
	var opts []string
	for _, l := range strings.Split(screen, "\n") {
		t := strings.TrimLeft(strings.TrimSpace(l), "›❯> ")
		if regexp.MustCompile(`^\d+\.`).MatchString(t) {
			opts = append(opts, t)
		}
	}
	for i, x := range opts {
		if strings.Contains(x, option) {
			return i
		}
	}
	return -1
}

func waitScreen(ctx context.Context, s *conformance.Session, limit time.Duration, ok func(string) bool) (string, error) {
	t := clock.Real{}.NewTimer(limit)
	defer t.Stop()
	last := ""
	for {
		select {
		case <-s.Stable():
			text, err := s.Screen(ctx)
			if err == nil {
				last = text
				if ok(text) {
					return text, nil
				}
			}
		case <-s.Ended():
			return last, fmt.Errorf("codex ended")
		case <-t.C():
			return last, fmt.Errorf("the screen did not come in %s", limit)
		case <-ctx.Done():
			return last, ctx.Err()
		}
	}
}

var emailRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

func mask(s string) string { return emailRe.ReplaceAllString(s, "<email>") }
