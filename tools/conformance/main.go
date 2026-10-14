// Command conformance runs the conformance test of a provider with its real
// CLI on this machine (docs/provedores/conformidade.md): the manual run of
// the end of M3 and of each new version of a CLI, before the heavy suite of
// M7 runs it in the lab. It plays the engine side (internal/conformance.Host)
// and is meant to run entirely as the account of the agents, with the
// binaries built into a folder that account reads:
//
//	go build -o <bin>/mestre ./cmd/mestre
//	go build -o <bin>/pty/1.0.0/mestre-pty -ldflags "-X main.version=1.0.0" ./cmd/mestre-pty
//	go build -o <bin>/conformance ./tools/conformance
//	sudo -u mestre-agente env MESTRE_REAL_CLI=1 <bin>/conformance run -provider claude-code \
//	    -profile-dir /var/lib/mestre/contas/<perfil> -cli <claude> -mestre <bin>/mestre \
//	    -pty <bin>/pty/1.0.0/mestre-pty -root <short scratch> -out <recordings>
//
// `setup-codex` is the step of the setup of a Codex profile the Owner does in
// the panel (docs/provedores/codex.md#hooks): it writes the hooks of the
// Mestre in the profile, starts Codex once and trusts them in its review,
// then checks that the trust Codex wrote is the one the fallback
// CodexHookTrust would write.
//
// It refuses to run unless MESTRE_REAL_CLI=1: it uses real logins and spends
// a few short turns of real quota. -restore puts back, at the end, the
// files of the profile the run writes (hooks.json and config.toml of Codex,
// settings.json of Antigravity), so a profile of a spike is left as found;
// the trust of the worktree and the session files are taken back by the
// run itself.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/conformance"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/pkg/clock"
)

type options struct {
	provider, profileName, profileDir, home string
	cli, version, mestre, pty, ptyVersion   string
	root, tmp, out, db, machine             string
	limit                                   time.Duration
	restore                                 bool
	fallbacks                               string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Getenv)
	stop()
	os.Exit(code)
}

const usage = "usage: MESTRE_REAL_CLI=1 conformance run|setup-codex -provider <id> -profile-dir <dir> -cli <path> -mestre <path> -pty <path> -root <dir> [flags]"

func run(ctx context.Context, args []string, out io.Writer, getenv func(string) string) int {
	if len(args) == 0 || (args[0] != "run" && args[0] != "setup-codex") {
		fmt.Fprintln(out, usage)
		return 2
	}
	if getenv("MESTRE_REAL_CLI") != "1" {
		fmt.Fprintln(out, "conformance: refusing to run: it uses the real logins of the profile and spends real quota; set MESTRE_REAL_CLI=1")
		return 2
	}
	var o options
	fl := flag.NewFlagSet("conformance "+args[0], flag.ContinueOnError)
	fl.SetOutput(out)
	fl.StringVar(&o.provider, "provider", "", "claude-code, codex or antigravity")
	fl.StringVar(&o.profileName, "profile", "", "name of the account profile (default: the folder's name)")
	fl.StringVar(&o.profileDir, "profile-dir", "", "provider folder of the profile: CLAUDE_CONFIG_DIR, CODEX_HOME or the HOME of agy")
	fl.StringVar(&o.home, "home", "", "HOME of the CLI (default: the profile folder)")
	fl.StringVar(&o.cli, "cli", "", "the CLI of the provider")
	fl.StringVar(&o.version, "version", "", "version of the CLI (default: its --version)")
	fl.StringVar(&o.mestre, "mestre", "", "the mestre binary the hooks call")
	fl.StringVar(&o.pty, "pty", "", "the mestre-pty binary")
	fl.StringVar(&o.ptyVersion, "pty-version", "1.0.0", "version of the mestre-pty")
	fl.StringVar(&o.root, "root", "", "short scratch folder of the engine side (sockets, database)")
	fl.StringVar(&o.tmp, "tmp", "", "folder of the temporary workspace (default: the system's)")
	fl.StringVar(&o.out, "out", "", "folder of the recordings (default: none)")
	fl.StringVar(&o.db, "db", "", "Guild database for the result (default: <root>/mestre.db)")
	fl.StringVar(&o.machine, "machine", "", "machine name of the result (default: the host name)")
	fl.DurationVar(&o.limit, "limit", conformance.DefaultLimit, "time limit of the provider")
	fl.BoolVar(&o.restore, "restore", false, "put back the files of the profile the run writes")
	fl.StringVar(&o.fallbacks, "fallbacks", "", "comma list of fallbacks to switch on (ClaudeMultilineViaFile, CodexMultilineViaFile, AntigravityNoPaste, CodexHookTrust, ClaudeStatusLineInLocal, AntigravityNoDeny, AntigravityNoSandbox, LayerHook, LayerFirstMessage, ReadRootAgents)")
	if err := fl.Parse(args[1:]); err != nil {
		return 2
	}
	if o.provider == "" || o.profileDir == "" || o.cli == "" || o.mestre == "" || (o.pty == "" && args[0] == "run") || o.root == "" {
		fmt.Fprintln(out, usage)
		return 2
	}
	if o.home == "" {
		o.home = o.profileDir
	}
	if o.profileName == "" {
		o.profileName = filepath.Base(o.profileDir)
	}
	logger := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelInfo}))
	var err error
	if args[0] == "setup-codex" {
		err = setupCodex(ctx, o, out, logger)
	} else {
		err = runOne(ctx, o, out, logger)
	}
	if err != nil {
		fmt.Fprintln(out, "conformance:", logging.NewMasker().Mask(err.Error()))
		return 1
	}
	return 0
}

// baseEnv is the environment of the CLI: nothing of this process is
// inherited but what a terminal needs.
func baseEnv(o options) []string {
	env := []string{"PATH=" + filepath.Dir(o.cli) + ":/usr/local/bin:/usr/bin:/bin", "TERM=xterm-256color", "LANG=C.UTF-8"}
	// The HOME of Antigravity is its account profile, which the run gives
	// as the engine does; a base may not set it
	// (docs/orquestracao/bases-e-fichas.md#regras-e-erros).
	if providers.ID(o.provider) != providers.Antigravity {
		env = append(env, "HOME="+o.home)
	}
	return env
}

func cliVersion(ctx context.Context, o options) string {
	if o.version != "" {
		return o.version
	}
	c := exec.CommandContext(ctx, o.cli, "--version")
	c.Env = append(baseEnv(o), "HOME="+o.home, "CLAUDE_CONFIG_DIR="+o.profileDir, "CODEX_HOME="+o.profileDir)
	b, err := c.Output()
	if err != nil {
		return "unknown"
	}
	if m := regexp.MustCompile(`\d+\.\d+\.\d+`).Find(b); m != nil {
		return string(m)
	}
	return strings.TrimSpace(string(b))
}

// profileFiles are the files of the profile a run writes besides the trust
// and the sessions, which the run takes back itself.
func profileFiles(o options) []string {
	switch providers.ID(o.provider) {
	case providers.Codex:
		return []string{filepath.Join(o.profileDir, "hooks.json"), filepath.Join(o.profileDir, "config.toml")}
	case providers.Antigravity:
		return []string{providers.AntigravitySettingsPath(o.profileDir)}
	}
	return nil
}

// snapshot keeps the files (nil for one that does not exist) and returns
// the function that puts them back.
func snapshot(paths []string) func() error {
	saved := map[string][]byte{}
	modes := map[string]os.FileMode{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err == nil {
			saved[p] = b
			if st, err := os.Stat(p); err == nil {
				modes[p] = st.Mode().Perm()
			}
		} else {
			saved[p] = nil
		}
	}
	return func() error {
		var errs []error
		for p, b := range saved {
			if b == nil {
				if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
					errs = append(errs, err)
				}
				continue
			}
			if err := os.WriteFile(p, b, modes[p]); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}
}

func parseFallbacks(list string) (conformance.Fallbacks, providers.Options, error) {
	var fb conformance.Fallbacks
	var opts providers.Options
	for _, f := range strings.Split(list, ",") {
		switch strings.TrimSpace(f) {
		case "":
		case "ClaudeMultilineViaFile":
			opts.ClaudeMultilineViaFile = true
		case "CodexMultilineViaFile":
			opts.CodexMultilineViaFile = true
		case "AntigravityNoPaste":
			opts.AntigravityNoPaste = true
		case "CodexHookTrust":
			fb.Native.CodexHookTrust = true
		case "ClaudeStatusLineInLocal":
			fb.Native.ClaudeStatusLineInLocal = true
		case "AntigravityNoDeny":
			fb.Native.AntigravityNoDeny = true
		case "AntigravityNoSandbox":
			fb.Native.AntigravityNoSandbox = true
		case "LayerHook":
			fb.Layer.Channel = providers.LayerHook
		case "LayerFirstMessage":
			fb.Layer.Channel = providers.LayerFirstMessage
		case "ReadRootAgents":
			fb.Layer.ReadRootAgents = true
		default:
			return fb, opts, fmt.Errorf("unknown fallback %q", f)
		}
	}
	return fb, opts, nil
}

func newHost(ctx context.Context, o options, logger *slog.Logger) (*conformance.Host, error) {
	if err := os.MkdirAll(o.root, 0o755); err != nil {
		return nil, err
	}
	layout := classes.DefaultLayout(runtime.GOOS, os.Getenv("ProgramData"), "conformidade")
	layout.Data = append(layout.Data, o.root)
	return conformance.NewHost(ctx, conformance.HostConfig{Root: o.root, PTYBinary: o.pty, PTYVersion: o.ptyVersion,
		Layout: layout, Clock: clock.Real{}, Logger: logger})
}

func newGit() (*gitx.Real, error) {
	return gitx.New(gitx.Options{Masker: logging.NewMasker(), Env: []string{"GIT_CONFIG_NOSYSTEM=1"}})
}

// execUsage runs the usage command of Antigravity (agy -p "/usage").
func execUsage(ctx context.Context, c providers.Command) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Env, cmd.Dir = c.Env, c.Dir
	var errb bytes.Buffer
	cmd.Stderr = &errb
	b, err := cmd.Output()
	if err != nil {
		return b, fmt.Errorf("%w: %s", err, strings.TrimSpace(errb.String()))
	}
	return b, nil
}

func runOne(ctx context.Context, o options, out io.Writer, logger *slog.Logger) error {
	fb, opts, err := parseFallbacks(o.fallbacks)
	if err != nil {
		return err
	}
	if o.restore {
		put := snapshot(profileFiles(o))
		defer func() {
			if err := put(); err != nil {
				fmt.Fprintln(out, "conformance: the files of the profile were not put back:", err)
			}
		}()
	}
	host, err := newHost(ctx, o, logger)
	if err != nil {
		return err
	}
	defer host.Close()
	git, err := newGit()
	if err != nil {
		return err
	}
	if o.db == "" {
		o.db = filepath.Join(o.root, "mestre.db")
	}
	db, err := guilddb.Open(ctx, o.db, store.Options{})
	if err != nil {
		return err
	}
	defer db.Close()
	if o.machine == "" {
		o.machine, _ = os.Hostname()
	}
	opts.Exec = execUsage
	var rec *conformance.Recorder
	if o.out != "" {
		rec = &conformance.Recorder{Dir: o.out}
	}
	r := &conformance.Runner{Clock: clock.Real{}, Git: git, Host: host, Options: opts, Results: conformance.ResultsDB{DB: db},
		Recorder: rec, TempDir: o.tmp, Limit: o.limit, Log: logger}
	req := conformance.Request{
		Machine: o.machine, Provider: providers.ID(o.provider), Version: cliVersion(ctx, o), Origin: conformance.OriginManual,
		Profile: conformance.Profile{Name: o.profileName, Dir: o.profileDir, Home: o.home},
		Base:    providers.Base{Name: o.provider, Command: o.cli, Env: baseEnv(o)},
		Mestre:  o.mestre, Fallbacks: fb,
	}
	if req.Provider == providers.Codex {
		req.Base.Args = []string{"--no-daemon"}
	}
	res, err := r.Run(ctx, req)
	b, _ := json.MarshalIndent(res, "", "  ")
	fmt.Fprintln(out, string(b))
	if rec != nil {
		if rerr := rec.Err(); rerr != nil {
			fmt.Fprintln(out, "conformance:", rerr)
		}
	}
	return err
}
