package conformance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

// DefaultLimit is the time of one provider (docs/provedores/conformidade.md#regras-e-erros).
const DefaultLimit = 10 * time.Minute

// Defaults of the waits of the steps.
const (
	// DefaultReadyLimit is how long step 1 waits for the character.
	DefaultReadyLimit = 60 * time.Second
	// DefaultTurnLimit bounds one turn of a step.
	DefaultTurnLimit = 3 * time.Minute
	// DegradedLimit is the answer time of the hook with the engine silent
	// (step 6).
	DegradedLimit = time.Second
)

// Profile is the account profile under test.
type Profile struct {
	// Name is the profile's name on the machine sheet.
	Name string
	// Dir is the provider folder of the profile: CLAUDE_CONFIG_DIR,
	// CODEX_HOME, or the HOME of Antigravity.
	Dir string
	// Home is the HOME the CLI runs with ("" leaves the base's).
	Home string
}

// Fallbacks are the "Se falhar" paths already switched on for the
// provider on this machine: the run tests them as a character would get
// them.
type Fallbacks struct {
	Native providers.NativeFallbacks
	Layer  providers.LayerFallbacks
}

// Request is one run: one provider, one account profile, one machine.
type Request struct {
	Machine  string
	Provider providers.ID
	Profile  Profile
	// Version is the version of the CLI, as the machine sheet has it.
	Version string
	Origin  Origin
	// OS is the system of the machine ("" is this one).
	OS string
	// Base is the base of the provider on the machine: the CLI executable,
	// its arguments and its environment (PATH, TERM, LANG).
	Base providers.Base
	// Mestre is the stable path of the `mestre` binary the hooks call and
	// the character runs (MESTRE_CLI, and its folder first in PATH).
	Mestre    string
	Model     string
	Fallbacks Fallbacks
}

// Checklist shows the result to the users (docs/plataforma/checklist.md,
// M7): the alert of a provider not conformant, the warnings of the steps
// that only warn and the information of step 9.
type Checklist interface {
	Conformance(ctx context.Context, n Notice) error
}

// NoticeKind is the weight of a notice.
type NoticeKind string

// Notices.
const (
	NoticeNotConformant NoticeKind = "not_conformant"
	NoticeWarning       NoticeKind = "warning"
	NoticeInfo          NoticeKind = "info"
)

// Notice is one item for the checklist.
type Notice struct {
	Kind     NoticeKind
	Machine  string
	Profile  string
	Provider providers.ID
	Version  string
	// Step is the step it is about (0: the whole run).
	Step int
	Text string
}

// UsageLog records the usage of the run, which belongs to the system and
// to no table (docs/plataforma/limites-de-uso.md, M5).
type UsageLog interface {
	RecordSystem(ctx context.Context, u SystemUsage) error
}

// SystemUsage is the usage of one run.
type SystemUsage struct {
	Machine, Profile string
	Provider         providers.ID
	// Owner is always "sistema"; Purpose "conformidade".
	Owner, Purpose string
	Usage          providers.Usage
}

// Diagnosis is the mode of the AI diagnosis (docs/autocura/diagnostico-com-ia.md,
// M7): it runs the provider in that mode in dir, asking it to create a file
// and to run a command, and reports what happened. Nil makes step 11 not
// applicable.
type Diagnosis interface {
	Probe(ctx context.Context, p providers.ID, profile Profile, dir string) (DiagnosisReport, error)
}

// DiagnosisReport is what the diagnosis mode left.
type DiagnosisReport struct {
	Created, Executed bool
	Detail            string
}

// Runner runs the conformance test.
type Runner struct {
	Clock clock.Clock
	Git   gitx.Git
	Host  *Host
	// Options configure the adapters: the fallbacks of the typing and the
	// command runner of ReadUsage (Antigravity's /usage).
	Options   providers.Options
	Results   Results
	Checklist Checklist
	Usage     UsageLog
	Diagnosis Diagnosis
	// Recorder keeps the hook payloads and the screens for review; nil
	// keeps none.
	Recorder *Recorder
	// TempDir is where the temporary folder of a run goes ("" is the
	// system's).
	TempDir string
	// Limit is the time of one provider; ReadyLimit and TurnLimit bound
	// step 1 and each turn. Zero means the defaults.
	Limit, ReadyLimit, TurnLimit time.Duration
	Log                          *slog.Logger
}

func (r *Runner) clk() clock.Clock {
	if r.Clock == nil {
		return clock.Real{}
	}
	return r.Clock
}

func (r *Runner) log() *slog.Logger {
	if r.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return r.Log
}

func or(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// Run tests one provider on one account profile, keeps the result in place
// of the previous one of the combination and tells the checklist. A failure
// is recorded, never retried. The error is only for a result that could not
// be kept.
func (r *Runner) Run(ctx context.Context, req Request) (Result, error) {
	if req.OS == "" {
		req.OS = runtime.GOOS
	}
	res := Result{Machine: req.Machine, Profile: req.Profile.Name, Provider: req.Provider, Version: req.Version,
		Origin: req.Origin, StartedAt: r.clk().Now().UTC()}
	rctx, cancel := clock.WithTimeout(ctx, r.clk(), or(r.Limit, DefaultLimit))
	ru := &run{r: r, req: req, res: &res}
	ru.do(rctx)
	cancel()
	ru.cleanup(context.WithoutCancel(ctx))
	res.FinishedAt = r.clk().Now().UTC()
	res.decide()
	if r.Recorder != nil {
		r.Recorder.Result(res)
	}
	var errs []error
	if r.Results != nil {
		if err := r.Results.Save(ctx, res); err != nil {
			errs = append(errs, err)
		}
	}
	if err := r.notify(ctx, res); err != nil {
		errs = append(errs, err)
	}
	return res, errors.Join(errs...)
}

// notify tells the checklist what the result raises
// (docs/provedores/conformidade.md#resultado).
func (r *Runner) notify(ctx context.Context, res Result) error {
	if r.Checklist == nil {
		return nil
	}
	n := Notice{Machine: res.Machine, Profile: res.Profile, Provider: res.Provider, Version: res.Version}
	var notes []Notice
	add := func(k NoticeKind, step int, text string) {
		x := n
		x.Kind, x.Step, x.Text = k, step, text
		notes = append(notes, x)
	}
	if !res.Conformant {
		add(NoticeNotConformant, 0, fmt.Sprintf("%s não é conforme na versão %s do perfil %s: %s", res.Provider, res.Version, res.Profile, res.Reason))
	}
	for _, s := range res.Steps {
		if s.Outcome != Failed || slices.Contains(blocking, s.Step) {
			continue
		}
		text := fmt.Sprintf("etapa %d falhou: %s", s.Step, s.Reason)
		switch s.Step {
		case StepResume:
			text += "; a retomada deste provedor passa a renascer o personagem"
		case StepSubagent:
			text += "; nesta máquina, as classes passam a negar a ferramenta de subagente do provedor no próprio personagem"
		case StepDiagnosis:
			text += "; o diagnóstico com IA não usa este provedor nesta máquina"
		}
		add(NoticeWarning, s.Step, text)
	}
	if res.Provider == providers.ClaudeCode && res.Step(StepProbeSkill).Outcome == Passed {
		if res.Flags.AgentsSkillsRead {
			add(NoticeInfo, StepProbeSkill, "o Claude já lê .agents/skills/; a cópia gerenciada pode ser desligada")
		} else {
			add(NoticeInfo, StepProbeSkill, "o Claude não lê .agents/skills/; a cópia gerenciada em .claude/skills/ continua")
		}
	}
	var errs []error
	for _, x := range notes {
		if err := r.Checklist.Conformance(ctx, x); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// run is the state of one run.
type run struct {
	r       *Runner
	req     Request
	res     *Result
	ws      *Workspace
	adapter providers.Adapter
	lreq    providers.LaunchRequest
	launch  providers.Launch
	layer   providers.Layer
	env     classes.Env
	// sessionID is the provider session of the first terminal; turns the
	// last turn it had.
	sessionID string
	lastTurn  string
	chars     []*character
	files     classes.OSNativeFiles
	tmpBefore []string

	mu     sync.Mutex
	calls  []providers.CallDecision // the decided calls of the turn that may still ask on a native screen
	events []providers.Event        // every event of the run, completed by Turns
}

func (ru *run) now() time.Time { return ru.r.clk().Now() }

// step runs one step and records it.
func (ru *run) step(ctx context.Context, n int, f func(ctx context.Context) StepResult) StepResult {
	start := ru.now()
	var s StepResult
	if err := ctx.Err(); err != nil {
		s = StepResult{Outcome: Failed, Reason: fmt.Sprintf("tempo limite de %s do provedor", or(ru.r.Limit, DefaultLimit))}
	} else {
		s = f(ctx)
		if s.Outcome == Failed && ctx.Err() != nil && s.Reason == "" {
			s.Reason = fmt.Sprintf("tempo limite de %s do provedor", or(ru.r.Limit, DefaultLimit))
		}
	}
	s.Step, s.Duration = n, clock.Since(ru.r.clk(), start)
	if c := ru.current(); c != nil && !strings.HasPrefix(s.Reason, notRun) {
		// The transcript is the screen at the end of the step; what the
		// step returned there, when it is something else, is the answer.
		if screen, _, _ := c.state(); screen != "" && screen != s.Transcript {
			s.Answer, s.Transcript = ru.mask(tail(s.Transcript, 4<<10)), screen
		}
	}
	if s.Transcript != "" {
		s.Transcript = ru.mask(tail(s.Transcript, 8<<10))
		if ru.r.Recorder != nil {
			ru.r.Recorder.StepScreen(ru.req, n, s.Transcript)
		}
	}
	for i := range s.Notes {
		s.Notes[i] = ru.mask(s.Notes[i])
	}
	s.Reason = ru.mask(s.Reason)
	ru.res.Steps = append(ru.res.Steps, s)
	ru.r.log().Info("conformance step", "provider", ru.req.Provider, "step", n, "outcome", s.Outcome, "reason", s.Reason, "took", s.Duration)
	return s
}

// current is the terminal started last.
func (ru *run) current() *character {
	if len(ru.chars) == 0 {
		return nil
	}
	return ru.chars[len(ru.chars)-1]
}

// mask hides secrets and the account in what the result keeps.
func (ru *run) mask(s string) string {
	return maskText(s, ru.pathMasks())
}

func (ru *run) pathMasks() map[string]string {
	m := map[string]string{}
	if ru.req.Profile.Dir != "" {
		m[ru.req.Profile.Dir] = "/perfil"
	}
	if ru.ws != nil {
		m[ru.ws.Root] = "/mconf"
	}
	return m
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// do prepares, provisions and runs the steps.
func (ru *run) do(ctx context.Context) {
	fail := func(why string, err error) {
		ru.step(ctx, StepReady, func(context.Context) StepResult {
			return StepResult{Outcome: Failed, Reason: why + ": " + err.Error()}
		})
	}
	ru.tmpBefore = tmpEntries()
	ws, err := prepare(ctx, ru.r.Git, ru.r.TempDir, ru.req.Provider == providers.ClaudeCode, ru.now())
	if err != nil {
		fail("preparação", err)
		return
	}
	ru.ws = ws
	if ru.adapter, err = providers.NewRegistry(ru.r.Options).For(ru.req.Provider); err != nil {
		fail("adaptador", err)
		return
	}
	if err := ru.provision(ctx); err != nil {
		fail("provisão do personagem", err)
		return
	}
	ru.steps(ctx)
}

// provision does what a table and the start of a character do: the trust,
// the hook files, the Mestre layer, the native controls, the effective rules
// and the launch command.
func (ru *run) provision(ctx context.Context) error {
	req, ws, a := ru.req, ru.ws, ru.adapter
	if _, err := a.PreTrust(providers.PreTrustRequest{OS: req.OS, Profile: req.Profile.Dir, Worktree: ws.Worktree, RepoRoot: ws.Mirror}); err != nil {
		return err
	}
	root := ws.Worktree
	if req.Provider == providers.Codex {
		root = req.Profile.Dir // the hooks of Codex live in the profile
	}
	hooks, err := a.HookFiles(providers.HookFilesRequest{OS: req.OS, Root: root, Binary: req.Mestre, Fallbacks: req.Fallbacks.Native})
	if err != nil {
		return err
	}
	if ru.layer, err = a.DeliverMestreLayer(providers.LayerRequest{Path: ws.MestreMD, OS: req.OS, AgentID: ws.AgentID,
		Worktree: ws.Worktree, Fallbacks: req.Fallbacks.Layer}); err != nil {
		return err
	}
	files := slices.Concat(hooks, ru.layer.Files)
	hookEnv := []string{localchan.EnvEndpoint + "=" + ru.r.Host.Endpoint(), localchan.EnvCLI + "=" + req.Mestre}
	var native providers.NativeControls
	if t, ok := providers.NativeOf(a); ok {
		pol := classes.NativePolicy(ws.Rules, classes.NativeCharacter{AgentID: ws.AgentID, Provider: req.Provider, OS: req.OS,
			Worktree: ws.Worktree, ProfileHome: req.Profile.Dir, GitDir: ws.GitDir, Mirror: ws.Mirror}, req.Fallbacks.Native)
		pol.HookEnv = hookEnv
		if native, err = t.Native(pol); err != nil {
			return err
		}
		for _, f := range native.Files {
			files = append(files, providers.ManagedFile(f))
		}
	}
	for _, f := range files {
		if _, err := ru.files.Update(ctx, f.Path, f.Merge); err != nil {
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
	}
	if err := ru.r.Host.Rules.Save(ctx, ws.Rules); err != nil {
		return err
	}
	if ru.layer.HookContext != "" {
		ru.r.Host.SetContext(ws.AgentID, ru.layer.HookContext)
	}
	env := []string{
		hookEnv[0], hookEnv[1], hook.EnvRecord + "=1",
		"MESTRE_WORKSPACE=conformidade", "MESTRE_TMP=" + ws.Tmp, "TMPDIR=" + ws.Tmp, "GIT_CONFIG_GLOBAL=" + ws.GitConfig,
	}
	if p := pathWith(req.Base.Env, filepath.Dir(req.Mestre)); p != "" {
		env = append(env, p)
	}
	if req.Profile.Home != "" {
		env = append(env, "HOME="+req.Profile.Home)
	}
	switch req.Provider {
	case providers.ClaudeCode:
		env = append(env, "CLAUDE_CONFIG_DIR="+req.Profile.Dir)
	case providers.Antigravity:
		env = append(env, "HOME="+req.Profile.Dir)
	}
	ru.lreq = providers.LaunchRequest{
		Base: req.Base, OS: req.OS, AgentID: ws.AgentID, TableID: "0-conformidade", Worktree: ws.Worktree,
		ProviderArgs: slices.Concat(ru.layer.Args, native.Args), Env: env, Profile: req.Profile.Dir, Model: req.Model,
	}
	if req.Provider == providers.ClaudeCode {
		ru.lreq.NewSessionID = newSessionUUID()
	}
	if ru.lreq.Base.Dir == "" {
		ru.lreq.Base.Dir = ws.Worktree
	}
	if ru.launch, err = a.BuildLaunch(ru.lreq); err != nil {
		return err
	}
	ru.env = classes.Env{AgentID: ws.AgentID, TableID: ru.lreq.TableID, Worktree: ws.Worktree, Tmp: ws.Tmp, OS: req.OS,
		Layout: ru.r.Host.cfg.Layout, Normalize: ru.r.Options.NormalizePath, Git: classes.GitxState{Git: ru.r.Git, Main: MainBranch}}
	return nil
}

// pathWith is PATH of env with dir first.
func pathWith(env []string, dir string) string {
	cur := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			cur = v
		}
	}
	if cur == "" {
		cur = os.Getenv("PATH")
	}
	return "PATH=" + dir + string(os.PathListSeparator) + cur
}

func newSessionUUID() string {
	b := []byte(randomID() + randomID() + randomID() + randomID())
	s := string(b)
	return s[0:8] + "-" + s[8:12] + "-4" + s[13:16] + "-a" + s[17:20] + "-" + s[20:32]
}

// cleanup stops what still runs and takes back what the run left in the
// profile: the trust of the worktree, the session files, the temporary
// folder with the class (docs/provedores/conformidade.md#regras-e-erros).
func (ru *run) cleanup(ctx context.Context) {
	for _, c := range ru.chars {
		_ = c.stop(ctx)
	}
	if ru.ws == nil {
		return
	}
	if t, ok := ru.adapter.(providers.TrustRemover); ok {
		// The worktree's entry, and the mirror's: Claude Code 2.1.296 keeps
		// the statistics of a session under the git root, which for a
		// worktree of the mirror is the mirror (without trusting it).
		for _, dir := range []string{ru.ws.Worktree, ru.ws.Mirror} {
			if _, err := t.Untrust(providers.PreTrustRequest{OS: ru.req.OS, Profile: ru.req.Profile.Dir, Worktree: dir, RepoRoot: ru.ws.Mirror}); err != nil {
				ru.r.log().Warn("conformance: the entry of the folder was not taken back", "provider", ru.req.Provider, "dir", dir, "err", err)
			}
		}
	}
	for _, p := range ru.sessionFiles() {
		_ = os.RemoveAll(p)
	}
	if err := os.RemoveAll(ru.ws.Root); err != nil {
		ru.r.log().Warn("conformance: the temporary folder was not removed", "dir", ru.ws.Root, "err", err)
	}
	if left := newTmpEntries(ru.tmpBefore, tmpEntries()); len(left) > 0 {
		ru.res.Notes = append(ru.res.Notes, "temporários deixados fora do TMPDIR do personagem: "+strings.Join(left, ", "))
	}
}

// sessionFiles are the files of the test sessions inside the profile, as
// the run builds them from what it knows (the worktree, the mirror) and
// from the session ids of the hooks that have the shape of a UUID; never a
// path of a payload, which the agent account could write. Every one is
// reached through no link (noLinkPath), so a link planted in the
// profile sends no removal elsewhere.
func (ru *run) sessionFiles() []string {
	ru.mu.Lock()
	defer ru.mu.Unlock()
	profile := ru.req.Profile.Dir
	var cands [][]string // parts under the profile
	if ru.req.Provider == providers.ClaudeCode && ru.ws != nil {
		// The folder of the project of the worktree (transcripts, tool
		// results, subagents) and the mirror's: Claude Code 2.1.299 keeps
		// the memory of a project under the git root, which for a worktree
		// of the mirror is the mirror.
		cands = append(cands, []string{"projects", claudeProject(ru.ws.Worktree)}, []string{"projects", claudeProject(ru.ws.Mirror)})
	}
	seenID := map[string]bool{}
	for _, ev := range ru.events {
		id := ev.SessionID
		if !sessionUUID.MatchString(id) || seenID[id] {
			continue
		}
		seenID[id] = true
		switch ru.req.Provider {
		case providers.Antigravity:
			cands = append(cands, []string{".gemini", "antigravity-cli", "brain", id})
		case providers.Codex: // sessions/AAAA/MM/DD/rollout-<time>-<id>.jsonl
			matches, _ := filepath.Glob(filepath.Join(profile, "sessions", "*", "*", "*", "rollout-*-"+id+".jsonl"))
			for _, m := range matches {
				if rel, err := filepath.Rel(profile, m); err == nil {
					cands = append(cands, strings.Split(rel, string(filepath.Separator)))
				}
			}
		}
	}
	var out []string
	for _, parts := range cands {
		if p, err := noLinkPath(profile, parts...); err == nil && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// sessionUUID is the shape of a session id the cleanup takes.
var sessionUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// tmpPatterns are the temporary files the spike saw the CLIs leave in /tmp
// (docs/sessao-zero/personagem.md#verificar-na-implementação).
var tmpPatterns = []string{"claude-*", "cc-socks-*", "codex-*", "unleash-repo-schema-*"}

func tmpEntries() []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	var out []string
	for _, p := range tmpPatterns {
		m, _ := filepath.Glob(filepath.Join("/tmp", p))
		out = append(out, m...)
	}
	return out
}

func newTmpEntries(before, after []string) []string {
	var out []string
	for _, a := range after {
		if !slices.Contains(before, a) {
			out = append(out, a)
		}
	}
	return out
}

// maskText masks the secrets the logs mask, the e-mails, the name of the
// person a start screen greets and the given paths.
func maskText(s string, paths map[string]string) string {
	for p, to := range paths {
		if p != "" {
			s = strings.ReplaceAll(s, p, to)
		}
	}
	// The patterns of the secrets of the logs, not the entropy rule of the
	// masker: it would also take the ids and the paths the recordings need.
	for _, p := range logging.Patterns() {
		s = maskPattern(p, s)
	}
	s = emailRe.ReplaceAllString(s, "<email>")
	return greetRe.ReplaceAllString(s, "${1} <nome>")
}

func maskPattern(p logging.Pattern, s string) string {
	if p.Group == 0 {
		return p.Re.ReplaceAllString(s, logging.Redacted)
	}
	return p.Re.ReplaceAllStringFunc(s, func(match string) string {
		sub := p.Re.FindStringSubmatchIndex(match)
		if sub == nil || sub[2*p.Group] < 0 {
			return logging.Redacted
		}
		return match[:sub[2*p.Group]] + logging.Redacted + match[sub[2*p.Group+1]:]
	})
}

// claudeProject is the name Claude Code gives the folder of a project in
// projects/ of the profile: the path with every character that is not a
// letter or a digit as "-".
func claudeProject(dir string) string {
	return strings.Map(func(r rune) rune {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, dir)
}
