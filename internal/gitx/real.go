package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/pkg/clock"
)

// waitDelay bounds how long Go waits for the output pipes after git was
// killed: on Windows a killed git leaves its helpers (git-remote-https)
// holding them.
const waitDelay = 3 * time.Second

// maxStderr is how much of git's output an Error keeps.
const maxStderr = 4096

// Options configures Real.
type Options struct {
	// Binary is the git executable; default "git" from PATH.
	Binary string
	// Suite is the value of MESTRE_TEST_SUITE ("" in production). In the
	// fast suite no process may start (docs/qualidade/testes.md).
	Suite string
	// Clock supplies the date of commits whose Signature has no When;
	// default clock.Real.
	Clock clock.Clock
	// Masker hides secrets in errors. Required: the engine's shared masker,
	// so the values the vault registered are hidden here too.
	Masker *logging.Masker
	// Env is KEY=VALUE pairs added last to the controlled environment of
	// every git process (for example a different GIT_CONFIG_GLOBAL). Of the
	// GIT_* variables only GIT_CONFIG_GLOBAL and GIT_CONFIG_NOSYSTEM are
	// accepted (New): the others could move the repository (GIT_DIR,
	// GIT_COMMON_DIR), add configuration above the command line of every
	// call or turn GIT_NO_LAZY_FETCH off.
	Env []string
	// MinVersion overrides gitx.MinVersion (tests).
	MinVersion Version
}

// Real implements Git over the system git binary. Every process runs with a
// controlled environment: no GIT_* variable of the caller, no user or system
// configuration, no hooks, no credential helpers, no prompts, English
// messages, and the repository's own configuration cannot make git run a
// command (globalArgs, repo.guard, GIT_NO_LAZY_FETCH). A table worktree is
// opened only by its record (OpenWorktree), never by Open. Operations that
// change refs, config or info/exclude run one at a time per repository (all
// worktrees of a repository count as one); reads run concurrently. That
// holds inside one Real, so a process builds a single Real and shares it.
type Real struct {
	opts   Options
	clk    clock.Clock
	masker *logging.Masker

	verMu sync.Mutex
	ver   *Version

	lockMu sync.Mutex
	locks  map[string]chan struct{}
}

var _ Git = (*Real)(nil)

// New returns a Real git. It starts no process until the first call.
func New(opts Options) (*Real, error) {
	if opts.Masker == nil {
		return nil, errors.New("gitx: Options.Masker is required (the engine's shared masker)")
	}
	for _, kv := range opts.Env {
		k, _, _ := strings.Cut(kv, "=")
		switch k = strings.ToUpper(k); {
		case k == "GIT_CONFIG_GLOBAL", k == "GIT_CONFIG_NOSYSTEM":
		case strings.HasPrefix(k, "GIT_"):
			return nil, fmt.Errorf("%w: Options.Env may not set %s", ErrInvalidArgument, k)
		}
	}
	if opts.Binary == "" {
		opts.Binary = "git"
	}
	if opts.MinVersion == (Version{}) {
		opts.MinVersion = MinVersion
	}
	g := &Real{opts: opts, clk: opts.Clock, masker: opts.Masker, locks: map[string]chan struct{}{}}
	if g.clk == nil {
		g.clk = clock.Real{}
	}
	return g, nil
}

// globalArgs are the -c settings of every call. The command line wins over
// every configuration file, so nothing from the repository (whose config a
// worktree user may be able to write) or the user may run a command (hooks,
// fsmonitor, ssh, signature programs, upload-pack, credential and askpass
// helpers, the ext:: transport), recurse into submodules, turn TLS checks
// off or change the output. Filter drivers have per-driver names and are
// neutralized per call, and a promisor remote, whose upload-pack a lazy
// fetch would run, is refused or never fetched from (repo.guard,
// GIT_NO_LAZY_FETCH). No command gitx runs produces a diff, so
// diff.external and textconv are never invoked. safe.directory is reset to
// empty last, so git's ownership check (a repository, .git file or work
// tree owned by another account is refused) stays on whatever a custom
// GIT_CONFIG_GLOBAL says.
func globalArgs() []string {
	null := os.DevNull
	return []string{
		"-c", "core.hooksPath=" + null,
		"-c", "core.excludesFile=" + null,
		"-c", "core.attributesFile=" + null,
		"-c", "core.fsmonitor=false",
		"-c", "core.sshCommand=ssh",
		"-c", "core.askPass=",
		"-c", "credential.helper=",
		"-c", "log.showSignature=false",
		"-c", "merge.verifySignatures=false",
		"-c", "commit.gpgSign=false",
		"-c", "tag.gpgSign=false",
		"-c", "protocol.ext.allow=never",
		"-c", "protocol.git.allow=never",
		"-c", "submodule.recurse=false",
		"-c", "fetch.recurseSubmodules=false",
		"-c", "http.sslVerify=true",
		"-c", "gc.auto=0",
		"-c", "maintenance.auto=false",
		"-c", "advice.detachedHead=false",
		"-c", "safe.directory=",
	}
}

// environ returns the environment of every git process.
func (g *Real) environ() []string { return g.environFrom(os.Environ()) }

// environFrom builds the environment from the one of the caller.
func (g *Real) environFrom(base []string) []string {
	var env []string
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		switch k = strings.ToUpper(k); {
		case strings.HasPrefix(k, "GIT_"), k == "LC_ALL", k == "LANG", k == "LANGUAGE":
			continue // GIT_DIR and friends (a hook), locale
		}
		env = append(env, kv)
	}
	env = append(env,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_MERGE_AUTOEDIT=no",
		"GIT_NO_LAZY_FETCH=1",      // no fetch from a promisor remote (git 2.44+; repo.guard before)
		"GIT_NO_REPLACE_OBJECTS=1", // refs/replace (agent-writable in the mirror) never rewrites an object the engine reads
		"LC_ALL=C",
		"LANGUAGE=C",
	)
	return append(env, g.opts.Env...)
}

// Error is a failed git call. Args and Stderr are masked: credentials in
// URLs and every value the shared masker knows.
type Error struct {
	Args     []string
	ExitCode int // -1 when git did not run to the end (killed, not started)
	Stderr   string
	Err      error // the cause: *exec.ExitError, or the context error
}

func (e *Error) Error() string {
	msg := "git " + strings.Join(e.Args, " ") + ": " + e.Err.Error()
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// has reports whether the message of git contains any of subs (ignoring case).
func has(err error, subs ...string) bool {
	var ge *Error
	if !errors.As(err, &ge) {
		return false
	}
	text := strings.ToLower(ge.Stderr)
	for _, s := range subs {
		if strings.Contains(text, strings.ToLower(s)) {
			return true
		}
	}
	return false
}

// exitedWithoutMessage reports whether git ended with code and printed
// nothing (the --quiet answers of rev-parse and merge-base).
func exitedWithoutMessage(err error, code int) bool {
	var ge *Error
	return errors.As(err, &ge) && ge.ExitCode == code && ge.Stderr == ""
}

// as wraps err with sentinel, keeping both for errors.Is and errors.As.
func as(sentinel, err error) error { return fmt.Errorf("%w: %w", sentinel, err) }

var credsRe = regexp.MustCompile(`://[^/\s@]+@`)

// RedactURL hides the credentials of URLs ("https://user:token@host/...")
// in s.
func RedactURL(s string) string {
	return credsRe.ReplaceAllString(s, "://"+logging.Redacted+"@")
}

// mask hides credentials embedded in URLs and every secret the masker knows.
func (g *Real) mask(s string) string {
	s = RedactURL(s)
	return g.masker.Mask(s)
}

// maskArg masks one argument piece by piece between path separators: a
// known value or format inside a piece is hidden, while a long path is not
// blanked as one high-entropy word.
func (g *Real) maskArg(a string) string {
	a = RedactURL(a)
	var b strings.Builder
	for {
		i := strings.IndexAny(a, `/\`)
		if i < 0 {
			b.WriteString(g.masker.Mask(a))
			return b.String()
		}
		b.WriteString(g.masker.Mask(a[:i]))
		b.WriteByte(a[i])
		a = a[i+1:]
	}
}

type runOpts struct {
	dir    string
	stdin  []byte
	env    []string
	config []string // extra -c settings, after globalArgs
	tree   bool     // the call touches the working tree (repo.guard)
}

// exec starts git once. It does not check the version.
func (g *Real) exec(ctx context.Context, ro runOpts, args ...string) ([]byte, error) {
	if g.opts.Suite == "fast" {
		return nil, g.newError(args, -1, ErrExternalProcessInFastSuite, "")
	}
	if err := context.Cause(ctx); err != nil {
		return nil, g.newError(args, -1, err, "")
	}
	full := globalArgs()
	for _, c := range ro.config {
		full = append(full, "-c", c)
	}
	cmd := exec.CommandContext(ctx, g.opts.Binary, append(full, args...)...)
	cmd.Dir = ro.dir
	cmd.Env = append(g.environ(), ro.env...)
	cmd.WaitDelay = waitDelay
	if ro.stdin != nil {
		cmd.Stdin = bytes.NewReader(ro.stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return nil, fmt.Errorf("%w: %q", ErrGitNotFound, g.opts.Binary)
	}
	code := -1
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	}
	if cause := context.Cause(ctx); cause != nil {
		err = cause
	}
	text := strings.TrimSpace(stderr.String())
	if text == "" {
		text = strings.TrimSpace(stdout.String()) // "nothing to commit" goes to stdout
	}
	return nil, g.newError(args, code, err, text)
}

// newError builds an Error with the arguments and the output masked.
func (g *Real) newError(args []string, code int, cause error, output string) *Error {
	e := &Error{Args: make([]string, len(args)), ExitCode: code, Err: cause}
	for i, a := range args {
		e.Args[i] = g.maskArg(a)
	}
	if len(output) > maxStderr {
		output = "..." + output[len(output)-maxStderr:]
	}
	e.Stderr = g.mask(output)
	return e
}

// run starts git after checking, once, that the binary is new enough.
func (g *Real) run(ctx context.Context, ro runOpts, args ...string) ([]byte, error) {
	if _, err := g.Version(ctx); err != nil {
		return nil, err
	}
	return g.exec(ctx, ro, args...)
}

// Version returns the version of the git binary, or ErrGitNotFound /
// ErrGitTooOld. A good answer is cached.
func (g *Real) Version(ctx context.Context) (Version, error) {
	g.verMu.Lock()
	defer g.verMu.Unlock()
	if g.ver != nil {
		return *g.ver, nil
	}
	out, err := g.exec(ctx, runOpts{}, "version")
	if err != nil {
		return Version{}, err
	}
	v, err := ParseVersion(string(out))
	if err != nil {
		return Version{}, err
	}
	if err := CheckVersion(v, g.opts.MinVersion); err != nil {
		return v, err
	}
	g.ver = &v
	return v, nil
}

// lock serializes the operations that change refs of one repository, shared
// by all its worktrees (ADR 0004). It gives up when ctx is done. The key is
// the common dir in a canonical form (lockKey), so two spellings of the
// same folder share one lock.
func (g *Real) lock(ctx context.Context, common string) (unlock func(), err error) {
	key := lockKey(common)
	g.lockMu.Lock()
	ch, ok := g.locks[key]
	if !ok {
		ch = make(chan struct{}, 1)
		g.locks[key] = ch
	}
	g.lockMu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// Init implements Git.
func (g *Real) Init(ctx context.Context, dir string, opts InitOptions) (Repo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("init %s: %w", dir, err)
	}
	branch := opts.InitialBranch
	if branch == "" {
		branch = "main"
	}
	if err := ValidateRef(branch); err != nil {
		return nil, err
	}
	args := []string{"init", "-q", "--initial-branch=" + branch}
	if opts.Bare {
		args = append(args, "--bare")
	}
	if _, err := g.run(ctx, runOpts{}, append(args, "--", abs)...); err != nil {
		return nil, fmt.Errorf("init %s: %w", dir, err)
	}
	return g.Open(ctx, abs)
}

// Clone implements Git. A relative local url is resolved from the working
// directory of the process.
func (g *Real) Clone(ctx context.Context, url, dir string, opts CloneOptions) (Repo, error) {
	if err := CheckURL(url); err != nil {
		return nil, fmt.Errorf("clone: %w", err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("clone into %s: %w", dir, err)
	}
	ro, err := g.authOpts(opts.Auth)
	if err != nil {
		return nil, fmt.Errorf("clone %s: %w", url, err)
	}
	args := []string{"clone", "-q", "--no-recurse-submodules", "--upload-pack=git-upload-pack"}
	if opts.Bare {
		args = append(args, "--bare")
	}
	if _, err := g.run(ctx, ro, append(args, "--", url, abs)...); err != nil {
		if has(err, "already exists and is not an empty directory") {
			err = as(ErrPathExists, err)
		}
		return nil, fmt.Errorf("clone %s: %w", RedactURL(url), err)
	}
	return g.Open(ctx, abs)
}

// authOpts puts auth in the environment of the call and registers its
// secrets in the masker.
func (g *Real) authOpts(auth *Auth) (runOpts, error) {
	if auth == nil {
		return runOpts{}, nil
	}
	if err := auth.Validate(); err != nil {
		return runOpts{}, err
	}
	for _, s := range auth.secrets() {
		g.masker.Register(s)
	}
	return runOpts{env: auth.env()}, nil
}

// Open implements Git.
func (g *Real) Open(ctx context.Context, dir string) (Repo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dir, err)
	}
	st, err := os.Stat(abs)
	if err == nil && !st.IsDir() {
		err = fs.ErrInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dir, as(ErrNotARepo, err))
	}
	// A bare repository (a mirror) is opened as one, by GIT_DIR: git looks
	// for a .git entry inside a folder before taking it as a bare
	// repository, and the folder of a mirror is writable by mestre-agente.
	pin := []string{"GIT_DIR=" + abs}
	if out, err := g.run(ctx, runOpts{dir: abs, env: pin}, "rev-parse", "--path-format=absolute", "--git-common-dir", "--is-bare-repository"); err == nil {
		if lines := strings.Split(strings.TrimSpace(string(out)), "\n"); len(lines) == 2 && strings.TrimSpace(lines[1]) == "true" {
			return &repo{g: g, dir: abs, common: filepath.Clean(filepath.FromSlash(strings.TrimSpace(lines[0]))), bare: true, gitDir: abs}, nil
		}
	} else if errors.Is(err, ErrGitNotFound) || errors.Is(err, ErrGitTooOld) || errors.Is(err, ErrExternalProcessInFastSuite) {
		return nil, fmt.Errorf("open %s: %w", dir, err)
	}
	out, err := g.run(ctx, runOpts{dir: abs}, "rev-parse", "--path-format=absolute", "--git-common-dir", "--is-bare-repository")
	if err != nil {
		if has(err, "not a git repository") {
			err = as(ErrNotARepo, err)
		}
		return nil, fmt.Errorf("open %s: %w", dir, err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return nil, fmt.Errorf("open %s: unexpected rev-parse output %q", dir, out)
	}
	return &repo{
		g:      g,
		dir:    abs,
		common: filepath.Clean(filepath.FromSlash(strings.TrimSpace(lines[0]))),
		bare:   strings.TrimSpace(lines[1]) == "true",
	}, nil
}
