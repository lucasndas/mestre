package providers_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/pkg/clock"
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

// A modal above the still-held input line, with the screen unchanged since it
// was stable, is the case only the modal list decides (the last › line is
// still the text): on the last screen and on the Reader alike.
func TestCodexModalAboveTheHeldInputLineGetsNoKey(t *testing.T) {
	t.Parallel()
	text := "Teste de conformidade do Mestre. Sem usar nenhuma ferramenta, responda só com estas duas linhas, preenchidas:"
	a := providers.NewCodex(providers.Options{OS: "windows"})
	r := a.(providers.SendRetrier)
	if !r.InputHolds(codexHeldScreen, text) {
		t.Fatal("the control: the held screen must hold the text")
	}
	for name, modal := range map[string]string{
		"update":       "Update available! 0.163.0 -> 0.164.0\nUpdate now / Skip until next version",
		"rate limit":   "Rate limit reached, try later",
		"usage":        "Usage limit",
		"switch":       "Switch to gpt-6.1-mini for lower credit usage?",
		"enter":        "Press Enter to continue",
		"patch":        "Would you like to make the following edits?",
		"bordered":     "│ › 1. Yes, proceed",
		"migration":    "Try new model",
		"full access":  "Enable full access?",
		"world-writes": "Some folders are world-writable",
	} {
		screen := modal + "\n\n" + codexHeldScreen
		if r.InputHolds(screen, text) {
			t.Errorf("%s: InputHolds accepted a modal above the input line", name)
		}
		clk := clock.NewFake(at)
		w := &world{}
		sup := providers.NewSupervisor(providers.SupervisorConfig{
			Clock: clk, Adapter: a, AgentID: "a1", TableID: "3-login",
			Keyboard: w, Reader: fixedScreen(screen), Characters: w, TableMaster: w, Checklist: w, Inbox: w, Decisions: w,
		})
		sup.Start(t.Context())
		sup.Screen("› Ask Codex to do anything\n\n  GPT-6.1-Sol default · ? for shortcuts", true)
		if err := sup.Deliver(t.Context(), "d1", text); err != nil {
			t.Fatal(err)
		}
		sup.Screen(screen, true) // the Reader agrees with the last stable screen
		clk.Advance(providers.DefaultSendRetry * providers.SendRetries)
		if typed := w.snapshot().typed; len(typed) != 1 {
			t.Errorf("%s: typed %q, want only the delivery", name, typed)
		}
		sup.Close()
	}
}

// The line of the history that starts like the delivery is not the input
// line: the box is gone (a dialog without a menu), and nothing is sent.
func TestCodexHistoryLineIsNotTheInputLine(t *testing.T) {
	t.Parallel()
	text := "Teste de conformidade do Mestre. Sem usar nenhuma ferramenta"
	r := providers.NewCodex(providers.Options{OS: "windows"}).(providers.SendRetrier)
	screen := "› " + text + "\n\n• ok\n" + "linha\nlinha\nlinha\nlinha\nlinha\nlinha\nlinha\nlinha\n\nAlgo novo, sem menu"
	if r.InputHolds(screen, text) {
		t.Error("a history line far above the footer was taken as the input line")
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
