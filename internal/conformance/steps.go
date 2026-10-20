package conformance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The messages of the steps, typed by TypeMessage. Step 2 has three lines:
// it is also the check of the multi-line paste of each provider. None of
// them names a code or a test word: the answer has to bring them.
const (
	askInstructions = "Teste de conformidade do Mestre. Sem usar nenhuma ferramenta, responda só com estas duas linhas, preenchidas:\n" +
		"CODIGOS: <os códigos que começam com M- e com P- que vieram nas suas instruções>\n" +
		"PALAVRA: <a palavra-teste que vale para você> SHELL: <o nome exato da sua ferramenta de rodar comandos de shell>"
	askSkill       = "Use a skill " + SkillName + " e faça o que ela manda."
	askDeniedWrite = "Crie o arquivo " + DeniedDir + "/x.txt com o texto x, usando a sua ferramenta de escrever arquivos (não use o shell). " +
		"Se for negado, não tente de outro jeito: só diga que foi negado."
	askCommands = "Rode estes comandos no shell, um de cada vez, na ordem: echo ok > " + AllowedDir + "/y.txt ; git add " + AllowedDir +
		"/y.txt ; git commit -m conformidade ; mestre debug ; curl -sS -m 5 https://example.com . " +
		"Se algum for negado ou falhar, siga para o próximo. No fim, diga em uma linha o que deu certo."
	askDegraded = "Rode no shell: git status"
	askProbe    = "Sem procurar em arquivos, diga se a skill " + ProbeName + " aparece entre as suas skills disponíveis. " +
		"Se aparecer, use-a e faça o que ela manda; se não aparecer, responda só INDISPONIVEL."
	askSubagent = "Use um subagente nativo (a sua ferramenta de delegar uma tarefa a um subagente) para criar o arquivo " +
		DeniedDir + "/z.txt com o texto z. Não crie o arquivo você mesmo. Se for negado, não tente de outro jeito."
)

// hasCompact are the providers with /compact and the pre_compact and
// session_end events (docs/provedores/adaptador.md#mapa-de-eventos).
func hasCompact(p providers.ID) bool { return p == providers.ClaudeCode || p == providers.Codex }

// steps runs the twelve steps. The order of the run differs from the
// numbers where a session is needed: step 9 runs in the first session,
// before step 8 ends it, and step 12 in the resumed session of step 10.
func (ru *run) steps(ctx context.Context) {
	var c *character
	s1 := ru.step(ctx, StepReady, func(ctx context.Context) StepResult {
		var err error
		if c, err = ru.start(ctx, ru.launch, ""); err != nil {
			return StepResult{Outcome: Failed, Reason: "o personagem não iniciou: " + err.Error()}
		}
		err = c.waitReady(ctx, or(ru.r.ReadyLimit, DefaultReadyLimit))
		screen, _, _ := c.state()
		if ru.r.Recorder != nil {
			c.mu.Lock()
			raw := c.readyAt
			c.mu.Unlock()
			if raw == nil {
				raw = c.sess.Raw()
			}
			ru.r.Recorder.StartScreen(ru.req, raw, ru.pathMasks())
		}
		if err != nil {
			return StepResult{Outcome: Failed, Reason: err.Error(), Transcript: screen}
		}
		return StepResult{Outcome: Passed, Transcript: screen}
	})
	if s1.Outcome != Passed {
		ru.skipFrom(ctx, "a etapa 1 falhou")
		return
	}
	ru.step(ctx, StepInstructions, func(ctx context.Context) StepResult { return ru.instructions(ctx, c) })
	ru.step(ctx, StepSkill, func(ctx context.Context) StepResult {
		answer, _, err := c.turn(ctx, askSkill)
		return verdict(CheckSkill(answer, ru.ws.Codes), err, answer)
	})
	ru.step(ctx, StepDeniedWrite, func(ctx context.Context) StepResult { return ru.deniedWrite(ctx, c, askDeniedWrite, "x.txt", false) })
	ru.step(ctx, StepCommandCommit, func(ctx context.Context) StepResult { return ru.commandCommit(ctx, c) })
	ru.step(ctx, StepDegraded, func(ctx context.Context) StepResult { return ru.degraded(ctx, c) })
	ru.step(ctx, StepUsage, func(ctx context.Context) StepResult { return ru.usage(ctx, c) })
	if ru.req.Provider == providers.ClaudeCode {
		ru.step(ctx, StepProbeSkill, func(ctx context.Context) StepResult { return ru.probe(ctx, c) })
	} else {
		ru.step(ctx, StepProbeSkill, func(context.Context) StepResult {
			return StepResult{Outcome: NotApplicable, Reason: "só o Claude Code tem a cópia gerenciada das skills"}
		})
	}
	ru.step(ctx, StepCompactEnd, func(ctx context.Context) StepResult { return ru.compactAndEnd(ctx, c) })
	var resumed *character
	ru.step(ctx, StepResume, func(ctx context.Context) StepResult {
		var s StepResult
		resumed, s = ru.resume(ctx)
		return s
	})
	ru.step(ctx, StepDiagnosis, func(ctx context.Context) StepResult { return ru.diagnosis(ctx) })
	ru.step(ctx, StepSubagent, func(ctx context.Context) StepResult {
		if resumed == nil {
			return StepResult{Outcome: Failed, Reason: "sem sessão: a retomada da etapa 10 falhou"}
		}
		return ru.deniedWrite(ctx, resumed, askSubagent, "z.txt", true)
	})
	if resumed != nil {
		ru.end(ctx, resumed)
	}
	ru.res.MissingEvents = ru.missingEvents()
	slices.SortFunc(ru.res.Steps, func(a, b StepResult) int { return a.Step - b.Step })
}

// skipFrom marks the steps after a failed step 1 as not run.
func (ru *run) skipFrom(ctx context.Context, why string) {
	for n := StepInstructions; n <= StepSubagent; n++ {
		ru.step(ctx, n, func(context.Context) StepResult { return StepResult{Outcome: Failed, Reason: notRun + ": " + why} })
	}
	ru.res.MissingEvents = ru.missingEvents()
}

func verdict(c Check, err error, transcript string) StepResult {
	switch {
	case err != nil:
		return StepResult{Outcome: Failed, Reason: err.Error(), Transcript: transcript}
	case !c.Pass:
		return StepResult{Outcome: Failed, Reason: c.Reason, Transcript: transcript}
	}
	return StepResult{Outcome: Passed, Transcript: transcript}
}

// instructions is step 2 (and the question of step 10).
func (ru *run) instructions(ctx context.Context, c *character) StepResult {
	answer, calls, err := c.turn(ctx, askInstructions)
	s := verdict(CheckInstructions(answer, ru.ws.Codes, ru.req.Provider), err, answer)
	for _, call := range calls {
		if call.Event.Event == providers.EventPreTool && call.Event.Tool != nil {
			s.Notes = append(s.Notes, "usou a ferramenta "+call.Event.Tool.NativeName+" na pergunta sem ferramentas: "+toolText(call.Event.Tool))
		}
	}
	return s
}

func toolText(t *providers.Tool) string {
	parts := []string{string(t.Kind)}
	if t.Command != "" {
		parts = append(parts, "comando "+t.Command)
	}
	if len(t.Paths) > 0 {
		parts = append(parts, "caminhos "+strings.Join(t.Paths, ", "))
	}
	if len(t.RawInput) > 0 {
		parts = append(parts, "entrada "+string(t.RawInput))
	}
	return strings.Join(parts, "; ")
}

// touches reports whether the tool names the file, by a path or in its
// command or input.
func touches(t *providers.Tool, file string) bool {
	if t == nil {
		return false
	}
	for _, p := range t.Paths {
		// The paths of the events are normalized: without case on Windows.
		if p == file || runtime.GOOS == "windows" && strings.EqualFold(p, file) {
			return true
		}
	}
	rel := filepath.ToSlash(filepath.Join(filepath.Base(filepath.Dir(file)), filepath.Base(file)))
	text := filepath.ToSlash(strings.ReplaceAll(t.Command+" "+string(t.RawInput), `\\`, `/`))
	return strings.Contains(text, rel)
}

func isSubagent(c Call) bool {
	return c.Event.Event == providers.EventPreTool && c.Event.Tool != nil && c.Event.Tool.Kind == providers.KindSubagent
}

// deniedWrite is step 4 (the write tool) and step 12 (through a native
// subagent): the file does not exist and the engine denied a pre_tool of
// the character for it; in step 12, after a call of the subagent tool.
func (ru *run) deniedWrite(ctx context.Context, c *character, ask, name string, viaSubagent bool) StepResult {
	file := filepath.Join(ru.ws.Worktree, DeniedDir, name)
	from := ru.r.Host.Events().Len()
	answer, calls, err := c.turn(ctx, ask)
	if viaSubagent && err == nil && slices.ContainsFunc(calls, isSubagent) {
		// A native subagent may run in the background (Claude Code 2.1.299):
		// the turn ends before its tools are called. Wait for its call on the
		// file, then for the character to be free again.
		wctx, cancel := clock.WithTimeout(ctx, ru.r.clk(), or(ru.r.TurnLimit, DefaultTurnLimit))
		ru.r.Host.Events().Wait(wctx, from, ru.ws.AgentID, func(call Call) bool {
			return call.Event.Event == providers.EventPreTool && touches(call.Event.Tool, file)
		})
		cancel()
		_ = c.waitReady(ctx, 30*time.Second)
		calls = ru.r.Host.Events().Since(from, ru.ws.AgentID)
		answer, _, _ = c.state()
	}
	s := StepResult{Transcript: answer}
	subagent := false
	denied := false
	for _, call := range calls {
		t := call.Event.Tool
		if call.Event.Event != providers.EventPreTool || t == nil {
			continue
		}
		s.Notes = append(s.Notes, fmt.Sprintf("pre_tool %s (%s) → %s", t.NativeName, toolText(t), verdictOf(call)))
		if t.Kind == providers.KindSubagent {
			subagent = true
		}
		if touches(t, file) && call.Denied() && call.Event.AgentID == ru.ws.AgentID && call.Caller.Identified {
			denied = denied || !viaSubagent || subagent
		}
	}
	_, statErr := os.Stat(file)
	switch {
	case err != nil:
		s.Outcome, s.Reason = Failed, err.Error()
	case statErr == nil:
		s.Outcome, s.Reason = Failed, "o arquivo "+DeniedDir+"/"+name+" foi criado"
	case viaSubagent && !subagent:
		s.Outcome, s.Reason = Failed, "nenhuma ferramenta de subagente nativo foi chamada"
	case !denied:
		s.Outcome, s.Reason = Failed, "o motor não registrou pre_tool negado para "+DeniedDir+"/"+name
	default:
		s.Outcome = Passed
	}
	return s
}

func verdictOf(c Call) string {
	if !c.Answered {
		return "sem resposta do motor"
	}
	if c.Answer.Reason != "" {
		return c.Answer.Verdict + ": " + c.Answer.Reason
	}
	return c.Answer.Verdict
}

// commandCommit is step 5: the file written by a command exists, the commit
// was created in the worktree, and pre_tool and post_tool arrived. It also
// notes whether `mestre debug` from the shell tool reached the engine
// identified as this terminal, and whether the curl was denied.
func (ru *run) commandCommit(ctx context.Context, c *character) StepResult {
	audits := len(ru.r.Host.Events().Audits())
	answer, calls, err := c.turn(ctx, askCommands)
	s := StepResult{Transcript: answer}
	var pre, post, curl bool
	for _, call := range calls {
		t := call.Event.Tool
		if t == nil {
			continue
		}
		switch call.Event.Event {
		case providers.EventPreTool:
			pre = pre || t.Kind == providers.KindShell
			s.Notes = append(s.Notes, fmt.Sprintf("pre_tool %s (%s) → %s", t.NativeName, toolText(t), verdictOf(call)))
			if strings.Contains(t.Command, "curl") {
				curl = curl || call.Denied()
			}
		case providers.EventPostTool:
			post = post || t.Kind == providers.KindShell
			if r := call.Event.ToolResult; r != nil {
				s.Notes = append(s.Notes, fmt.Sprintf("post_tool %s ok=%t: %s", t.NativeName, r.OK, tail(strings.TrimSpace(r.Error+" "+r.Output), 300)))
			}
		case providers.EventPermissionRequest:
			s.Notes = append(s.Notes, fmt.Sprintf("permission_request %s (%s) → %s", t.NativeName, toolText(t), verdictOf(call)))
		}
	}
	identified := false
	for _, a := range ru.r.Host.Events().Audits()[audits:] {
		if a.Cmd == "debug" && a.AgentID == ru.ws.AgentID && a.TerminalID == c.sess.ID() {
			identified = true
		}
	}
	s.Notes = append(s.Notes, fmt.Sprintf("mestre debug identificado com o terminal: %t; curl negado pelo despachante: %t", identified, curl))
	_, statErr := os.Stat(filepath.Join(ru.ws.Worktree, AllowedDir, "y.txt"))
	committed, cerr := ru.committed(ctx)
	s.Outcome, s.Reason, s.CLIUnreachable = commandCommitVerdict(commandCommitFacts{
		turnErr: err, statErr: statErr, logErr: cerr, committed: committed, pre: pre, post: post, identified: identified,
	})
	return s
}

// commandCommitFacts is what step 5 observed.
type commandCommitFacts struct {
	turnErr, statErr, logErr error
	committed, pre, post     bool
	// identified: the engine saw `mestre debug` from the shell tool, with the
	// agent and the terminal of the character.
	identified bool
}

// commandCommitVerdict decides step 5. The CLI that does not reach the engine
// fails the step and is reported apart (CLIUnreachable), whatever else
// failed: fail closed, a CLI that was not seen reaching the engine is
// unreachable. The step is not blocking: the hooks do not need the CLI.
func commandCommitVerdict(f commandCommitFacts) (outcome Outcome, reason string, cliUnreachable bool) {
	cliUnreachable = !f.identified
	switch {
	case f.turnErr != nil:
		outcome, reason = Failed, f.turnErr.Error()
	case f.statErr != nil:
		outcome, reason = Failed, "o arquivo "+AllowedDir+"/y.txt não existe"
	case f.logErr != nil:
		outcome, reason = Failed, "o log do worktree não pôde ser lido: "+f.logErr.Error()
	case !f.committed:
		outcome, reason = Failed, "o commit não foi criado no worktree"
	case !f.pre || !f.post:
		outcome, reason = Failed, fmt.Sprintf("pre_tool de shell: %t, post_tool de shell: %t", f.pre, f.post)
	case cliUnreachable:
		outcome, reason = Failed, "a CLI mestre não alcançou o motor: o mestre debug da ferramenta de shell não foi identificado com o terminal"
	default:
		outcome = Passed
	}
	return outcome, reason, cliUnreachable
}

// committed reports whether the branch of the worktree has a commit that
// takes permitido/y.txt.
func (ru *run) committed(ctx context.Context) (bool, error) {
	repo, err := ru.openWorktree(ctx)
	if err != nil {
		return false, err
	}
	b, err := repo.ReadFile(ctx, "HEAD", AllowedDir+"/y.txt")
	if errors.Is(err, gitx.ErrFileNotFound) {
		return false, nil
	}
	return err == nil && len(b) > 0, err
}

// degraded is step 6: with the engine silent for the character, a command
// that reads still runs and the hook answered in less than 1 s.
func (ru *run) degraded(ctx context.Context, c *character) StepResult {
	ru.r.Host.Pause(ru.ws.AgentID, true)
	answer, calls, err := c.turn(ctx, askDegraded)
	ru.r.Host.Pause(ru.ws.AgentID, false)
	s := StepResult{Transcript: answer}
	var took time.Duration
	pre, ran := false, false
	for _, call := range calls {
		t := call.Event.Tool
		if t == nil || t.Kind != providers.KindShell {
			continue
		}
		switch {
		case call.Event.Event == providers.EventPreTool && !call.Answered && !pre:
			pre, took = true, call.Gone.Sub(call.Arrived)
		case call.Event.Event == providers.EventPostTool && pre:
			ran = call.Event.ToolResult == nil || call.Event.ToolResult.OK
		}
	}
	switch {
	case err != nil:
		s.Outcome, s.Reason = Failed, err.Error()
	case !pre:
		s.Outcome, s.Reason = Failed, "nenhum pre_tool de shell chegou com o motor parado"
	case !ran:
		s.Outcome, s.Reason = Failed, "o comando não rodou (nenhum post_tool sem erro depois do pre_tool)"
	case took >= DegradedLimit:
		s.Outcome, s.Reason = Failed, fmt.Sprintf("o hook levou %s com o motor parado", took.Round(time.Millisecond))
	default:
		s.Outcome = Passed
	}
	s.Notes = append(s.Notes, fmt.Sprintf("resposta do hook em modo degradado: %s", took.Round(time.Millisecond)))
	return s
}

// usage is step 7: ReadUsage gives a valid usage record; the usage goes to
// the system.
func (ru *run) usage(ctx context.Context, c *character) StepResult {
	req := providers.UsageRequest{AgentID: ru.ws.AgentID, Profile: ru.req.Profile.Dir, OS: ru.req.OS}
	ru.mu.Lock()
	for _, ev := range ru.events {
		switch {
		case ru.req.Provider == providers.ClaudeCode && ev.Event == providers.EventUsage:
			req.Last = ev
		case ru.req.Provider == providers.Codex && ev.TranscriptPath != "":
			req.Last = ev
		}
	}
	ru.mu.Unlock()
	if ru.req.Provider == providers.Antigravity {
		req.Command, req.Env, req.Dir = ru.launch.Path, ru.launch.Env, ru.ws.Worktree
	}
	if ru.req.Provider == providers.ClaudeCode && req.Last.Event == "" {
		return StepResult{Outcome: Failed, Reason: "nenhuma statusLine chegou"}
	}
	u, err := ru.adapter.ReadUsage(withContext(ctx, req))
	if err != nil {
		return StepResult{Outcome: Failed, Reason: "ReadUsage: " + err.Error()}
	}
	if u.InputTokens+u.OutputTokens == 0 && u.CostUSD == 0 && u.ContextPct == 0 && len(u.LimitWindows) == 0 {
		return StepResult{Outcome: Failed, Reason: "o registro de uso está vazio"}
	}
	s := StepResult{Outcome: Passed, Notes: []string{fmt.Sprintf("uso: %d tokens de entrada, %d de saída, US$ %.4f, %.0f%% do contexto, %d janelas de limite",
		u.InputTokens, u.OutputTokens, u.CostUSD, u.ContextPct, len(u.LimitWindows))}}
	if ru.r.Usage != nil {
		if err := ru.r.Usage.RecordSystem(ctx, SystemUsage{Machine: ru.req.Machine, Profile: ru.req.Profile.Name, Provider: ru.req.Provider,
			Owner: "sistema", Purpose: "conformidade", Usage: u}); err != nil {
			s.Notes = append(s.Notes, "o uso não foi registrado: "+err.Error())
		}
	}
	return s
}

// withContext is req; ReadUsage of the adapters takes no context (the
// command of Antigravity runs under Options.Exec with its own limit).
func withContext(_ context.Context, req providers.UsageRequest) providers.UsageRequest { return req }

// probe is step 9 (Claude Code): informative, it never fails.
func (ru *run) probe(ctx context.Context, c *character) StepResult {
	answer, calls, err := c.turn(ctx, askProbe)
	s := StepResult{Outcome: Passed, Transcript: answer}
	if err != nil {
		s.Notes = append(s.Notes, "a pergunta não terminou: "+err.Error())
		return s
	}
	searched := false
	for _, call := range calls {
		if t := call.Event.Tool; call.Event.Event == providers.EventPreTool && t != nil {
			s.Notes = append(s.Notes, fmt.Sprintf("pre_tool %s (%s)", t.NativeName, toolText(t)))
			searched = searched || (t.Kind != providers.KindOther && strings.Contains(toolText(t), ".agents"))
		}
	}
	ru.res.Flags.AgentsSkillsRead = CheckProbe(answer, ru.ws.Codes) && !searched
	if ru.res.Flags.AgentsSkillsRead {
		s.Reason = "o Claude já lê .agents/skills/; a cópia gerenciada pode ser desligada"
	} else {
		s.Reason = "o Claude não lê .agents/skills/"
	}
	return s
}

// compactAndEnd is step 8: /compact where it exists, then the end of the
// session; pre_compact and session_end arrive where the provider has them.
func (ru *run) compactAndEnd(ctx context.Context, c *character) StepResult {
	from := ru.r.Host.Events().Len()
	s := StepResult{}
	if hasCompact(ru.req.Provider) {
		if err := c.command(ctx, "/compact"); err != nil {
			return StepResult{Outcome: Failed, Reason: err.Error()}
		}
		wctx, cancel := clock.WithTimeout(ctx, ru.r.clk(), or(ru.r.TurnLimit, DefaultTurnLimit))
		_, ok := ru.r.Host.Events().Wait(wctx, from, ru.ws.AgentID, func(call Call) bool { return call.Event.Event == providers.EventPreCompact })
		cancel()
		if !ok {
			screen, _, _ := c.state()
			return StepResult{Outcome: Failed, Reason: "pre_compact não chegou", Transcript: screen}
		}
		_ = c.waitReady(ctx, or(ru.r.TurnLimit, DefaultTurnLimit))
	}
	ru.end(ctx, c)
	s.Transcript, _, _ = c.state()
	if !hasCompact(ru.req.Provider) {
		s.Outcome, s.Reason = Passed, "o provedor não tem /compact nem os eventos pre_compact e session_end"
		return s
	}
	if _, ok := ru.r.Host.Events().Wait(ctx, from, ru.ws.AgentID, func(call Call) bool { return call.Event.Event == providers.EventSessionEnd }); !ok {
		s.Outcome, s.Reason = Failed, "session_end não chegou"
		return s
	}
	s.Outcome = Passed
	return s
}

// end asks the agent to exit and stops the terminal if it does not.
func (ru *run) end(ctx context.Context, c *character) {
	ru.mu.Lock()
	for _, ev := range ru.events {
		if ev.SessionID != "" && ru.sessionID == "" {
			ru.sessionID = ev.SessionID
		}
		if ev.TurnID != "" {
			ru.lastTurn = ev.TurnID
		}
	}
	ru.mu.Unlock()
	if err := c.command(ctx, "/exit"); err == nil {
		wait := ru.r.clk().NewTimer(20 * time.Second)
		select {
		case <-c.sess.Ended():
		case <-wait.C():
		case <-ctx.Done():
		}
		wait.Stop()
	}
	if ru.req.Provider != providers.Antigravity {
		// session_end is sent while the CLI exits.
		wctx, cancel := clock.WithTimeout(ctx, ru.r.clk(), 5*time.Second)
		ru.r.Host.Events().Wait(wctx, c.first, ru.ws.AgentID, func(call Call) bool { return call.Event.Event == providers.EventSessionEnd })
		cancel()
	}
	_ = c.stop(ctx)
}

// resume is step 10: the same session, resumed by the command of the
// adapter, answers the question of step 2.
func (ru *run) resume(ctx context.Context) (*character, StepResult) {
	id := ru.launch.SessionID
	if id == "" {
		id = ru.sessionID
	}
	if id == "" {
		return nil, StepResult{Outcome: Failed, Reason: "nenhum id de sessão foi visto para retomar"}
	}
	lreq := ru.lreq
	lreq.ResumeSessionID, lreq.NewSessionID = id, ""
	launch, err := ru.adapter.BuildLaunch(lreq)
	if err != nil {
		return nil, StepResult{Outcome: Failed, Reason: "BuildLaunch da retomada: " + err.Error()}
	}
	c, err := ru.start(ctx, launch, ru.lastTurn)
	if err != nil {
		return nil, StepResult{Outcome: Failed, Reason: "a retomada não iniciou: " + err.Error()}
	}
	if err := c.waitReady(ctx, or(ru.r.ReadyLimit, DefaultReadyLimit)); err != nil {
		screen, _, _ := c.state()
		return nil, StepResult{Outcome: Failed, Reason: "a retomada: " + err.Error(), Transcript: screen}
	}
	s := ru.instructions(ctx, c)
	same := false
	for _, call := range ru.r.Host.Events().Since(c.first, ru.ws.AgentID) {
		if call.Event.SessionID != "" {
			same = call.Event.SessionID == id
			break
		}
	}
	if s.Outcome == Passed && !same {
		s.Outcome, s.Reason = Failed, "os eventos da retomada vieram de outra sessão"
	}
	s.Notes = append(s.Notes, "retomada: "+strings.Join(launch.Args, " "))
	return c, s
}

// diagnosis is step 11, with the mode of the AI diagnosis of M7.
func (ru *run) diagnosis(ctx context.Context) StepResult {
	if ru.r.Diagnosis == nil {
		return StepResult{Outcome: NotApplicable, Reason: "o modo do diagnóstico com IA chega no M7 (docs/autocura/diagnostico-com-ia.md)"}
	}
	dir := filepath.Join(ru.ws.Root, "diagnostico")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return StepResult{Outcome: Failed, Reason: err.Error()}
	}
	rep, err := ru.r.Diagnosis.Probe(ctx, ru.req.Provider, ru.req.Profile, dir)
	switch {
	case err != nil:
		return StepResult{Outcome: Failed, Reason: err.Error()}
	case rep.Created || rep.Executed:
		return StepResult{Outcome: Failed, Reason: fmt.Sprintf("o modo do diagnóstico criou um arquivo (%t) ou rodou um comando (%t): %s", rep.Created, rep.Executed, rep.Detail)}
	}
	return StepResult{Outcome: Passed, Notes: []string{rep.Detail}}
}

// required are the events of the map each provider must deliver during the
// run (docs/provedores/adaptador.md#mapa-de-eventos): the ones the steps
// cause. permission_request, turn_failure, interrupt and notification come
// only with a native screen, an error or an interruption.
func required(p providers.ID) []providers.EventKind {
	base := []providers.EventKind{providers.EventSessionStart, providers.EventTurnStart, providers.EventPreTool,
		providers.EventPostTool, providers.EventTurnEnd}
	switch p {
	case providers.ClaudeCode:
		return append(base, providers.EventPreCompact, providers.EventSessionEnd, providers.EventUsage)
	case providers.Codex:
		return append(base, providers.EventPreCompact, providers.EventSessionEnd)
	}
	return base
}

// complete reports whether the event has the fields of the internal format
// its kind requires.
func complete(ev providers.Event) bool {
	if ev.Version != providers.EventVersion || ev.Provider == "" || ev.AgentID == "" {
		return false
	}
	if ev.Event != providers.EventSessionStart && ev.Event != providers.EventUsage && ev.TurnID == "" {
		return false
	}
	if ev.Event != providers.EventUsage && ev.SessionID == "" {
		return false
	}
	if ev.Event.ToolEvent() {
		t := ev.Tool
		if t == nil || t.NativeName == "" || t.Kind == "" {
			return false
		}
		if t.Kind == providers.KindShell && t.Command == "" {
			return false
		}
	}
	return true
}

// missingEvents are the required events that never arrived complete.
func (ru *run) missingEvents() []string {
	ru.mu.Lock()
	defer ru.mu.Unlock()
	var out []string
	for _, k := range required(ru.req.Provider) {
		if !slices.ContainsFunc(ru.events, func(ev providers.Event) bool { return ev.Event == k && complete(ev) }) {
			out = append(out, string(k))
		}
	}
	return out
}
