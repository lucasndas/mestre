package providers_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

// A modal of Codex over the input line takes the send key as the answer of
// its default option: none of them gets the key again, whether it came in
// the last screen or between that screen and the look (the Reader).
var codexModals = map[string]string{
	"update":         "Update available! 0.163.0 -> 0.164.0\n› 1. Update now\n  2. Skip\n  3. Skip until next version",
	"model switch":   "Approaching rate limits\nSwitch to gpt-6.1-mini for lower credit usage?\n› 1. Switch to gpt-6.1-mini\n  2. Keep current model\n  3. Keep current model (never show again)",
	"rate limit":     "You've hit your usage limit. Try again later.\n› 1. Stop and wait for limit to reset",
	"approval":       "Would you like to run the following command?\n› 1. Yes, proceed (y)",
	"network":        "Do you want to approve network access to \"example.com\"?\n› 1. Yes, just this once (y)",
	"trust":          "Trust this folder? Codex can read, edit, and run files here.\n› 1. Trust and continue",
	"hook review":    "Hooks need review\n› 1. Review hooks\n  2. Trust all and continue",
	"windows setup":  "Set up the Codex agent sandbox to protect your files",
	"login":          "Sign in with ChatGPT",
	"unknown option": "Something new\n› 1. Do the default\n  2. Other",
}

type fixedScreen string

func (f fixedScreen) ReadScreen(context.Context) (string, error) { return string(f), nil }

func TestCodexDoesNotSendAgainOverAModal(t *testing.T) {
	t.Parallel()
	ready := "› Ask Codex to do anything\n\n  GPT-6.1-Sol default · ? for shortcuts"
	text := "Teste de conformidade do Mestre. Sem usar nenhuma ferramenta, responda só com estas duas linhas, preenchidas:"
	for name, modal := range codexModals {
		for _, viaReader := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s reader=%v", name, viaReader), func(t *testing.T) {
				t.Parallel()
				clk := clock.NewFake(at)
				w := &world{}
				var rd providers.ScreenReader
				if viaReader {
					rd = fixedScreen(codexHeldScreen + "\n\n" + modal)
				}
				sup := providers.NewSupervisor(providers.SupervisorConfig{
					Clock: clk, Adapter: providers.NewCodex(providers.Options{OS: "windows"}), AgentID: "a1", TableID: "3-login",
					Keyboard: w, Reader: rd, Characters: w, TableMaster: w, Checklist: w, Inbox: w, Decisions: w,
				})
				defer sup.Close()
				sup.Start(t.Context())
				sup.Screen(ready, true)
				if err := sup.Deliver(t.Context(), "d1", text); err != nil {
					t.Fatal(err)
				}
				if viaReader {
					sup.Screen(codexHeldScreen, true) // the last screen reported is still the held input
				} else {
					sup.Screen(codexHeldScreen+"\n\n"+modal, true)
				}
				clk.Advance(providers.DefaultSendRetry * providers.SendRetries)
				for _, k := range w.snapshot().typed[1:] {
					if k == "\r" {
						t.Fatalf("the send key went over the modal; typed %q", w.snapshot().typed)
					}
				}
			})
		}
	}
}

// The control: the same held input, with the Reader showing the same screen,
// gets the key.
func TestCodexSendsAgainWhenTheReaderAgrees(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(at)
	w := &world{}
	sup := providers.NewSupervisor(providers.SupervisorConfig{
		Clock: clk, Adapter: providers.NewCodex(providers.Options{OS: "windows"}), AgentID: "a1", TableID: "3-login",
		Keyboard: w, Reader: fixedScreen(codexHeldScreen), Characters: w, TableMaster: w, Checklist: w, Inbox: w, Decisions: w,
	})
	defer sup.Close()
	sup.Start(t.Context())
	sup.Screen("› Ask Codex to do anything\n\n  GPT-6.1-Sol default · ? for shortcuts", true)
	if err := sup.Deliver(t.Context(), "d1", "Teste de conformidade do Mestre. Sem usar nenhuma ferramenta, responda só com estas duas linhas, preenchidas:"); err != nil {
		t.Fatal(err)
	}
	sup.Screen(codexHeldScreen, true)
	clk.Advance(providers.DefaultSendRetry)
	if typed := w.snapshot().typed; len(typed) != 2 || typed[1] != "\r" {
		t.Errorf("typed = %q, want the delivery and the key", typed)
	}
}
