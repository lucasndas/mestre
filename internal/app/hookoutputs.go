package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
)

// maxAgentHookOutput bounds what an `output: agent` hook puts in the inbox of
// the character.
const maxAgentHookOutput = 4000

// HookOutputs is where the asynchronous hooks of the classes write
// (hook.AsyncRunner.Out), joined to the orchestration of the Citadel: the
// scroll of the quest of the character (quests.Service.Scroll), the log of the
// engine and the inbox of the character. On a Tower the runner reaches the
// Citadel over the connection (milestone 5); on the Citadel it is this.
func (o *Orchestration) HookOutputs() hook.HookOutputs {
	return hookOutputs{orc: o}
}

type hookOutputs struct{ orc *Orchestration }

func (h hookOutputs) Log(_ context.Context, r hook.AsyncResult) {
	log := h.orc.log
	if log == nil {
		return
	}
	level := slog.LevelInfo
	if r.Err != nil || r.Exit != 0 {
		level = slog.LevelWarn
	}
	log.Log(context.Background(), level, "app: output of an asynchronous hook of a class", "agent", r.AgentID, "hook", r.Hook, "exit", r.Exit, "err", r.Err)
}

func (h hookOutputs) Scroll(ctx context.Context, r hook.AsyncResult) error {
	return h.orc.Quests.Scroll(ctx, r)
}

// Agent puts the output in the inbox of the character as an item of the
// system, in a block of code: what a program printed is not an instruction.
func (h hookOutputs) Agent(ctx context.Context, r hook.AsyncResult) error {
	c, err := h.orc.Registry.Get(ctx, r.AgentID)
	if err != nil {
		return err
	}
	if !c.State.Alive() {
		return nil
	}
	out := strings.TrimSpace(strings.ReplaceAll(r.Output, "```", "'''"))
	if utf8.RuneCountInString(out) > maxAgentHookOutput {
		out = string([]rune(out)[:maxAgentHookOutput]) + "\n… (cortado)"
	}
	text := fmt.Sprintf("O hook %s da sua classe terminou.", r.Hook)
	switch {
	case r.Err != nil:
		text += fmt.Sprintf(" Erro: %v.", r.Err)
	case r.Exit != 0:
		text += fmt.Sprintf(" Código de saída %d.", r.Exit)
	}
	if out != "" {
		text += "\n\n```\n" + out + "\n```"
	}
	_, err = h.orc.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: c.Workspace, Target: c.ID, Kind: inbox.KindSystem, Sender: inbox.SystemSender, Text: text,
		Refs: inbox.Refs{Table: c.Table}})
	return err
}
