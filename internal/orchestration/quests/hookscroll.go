package quests

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/orchestration/scrolls"
)

// maxHookOutput is how much of the output of a hook goes to the scroll.
const maxHookOutput = 8000

// Scroll is the Scroll of a hook.HookOutputs: the output of an
// asynchronous hook of the class of a character, with `output: scroll`, is
// added to the scroll of its quest (to the history of its table when the
// character has no quest), as a section of the engine
// (docs/classes/classes.md#hooks-extras-da-classe). The log and the inbox of
// the outputs are not the quests'. What a hook printed is a
// program's text, not the Master's: it goes in a block of code.
func (s *Service) Scroll(ctx context.Context, r hook.AsyncResult) error {
	c, err := s.o.Registry.Get(ctx, r.AgentID)
	if err != nil {
		return err
	}
	if c.Table == "" {
		return nil // the Grand Master has no scroll
	}
	name := scrolls.HistoryName
	if c.Quest != "" {
		q, err := s.st.Quest(ctx, c.Quest)
		if err != nil {
			return err
		}
		name = scrolls.QuestScrollName(q.Name)
	}
	out := strings.TrimSpace(strings.ReplaceAll(r.Output, "```", "'''"))
	if utf8.RuneCountInString(out) > maxHookOutput {
		out = string([]rune(out)[:maxHookOutput]) + "\n… (cortado)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Hook %s — %s — %s\n", r.Hook, c.Name, s.stamp())
	switch {
	case r.Err != nil:
		fmt.Fprintf(&b, "**Erro:** %v\n", r.Err)
	case r.Exit != 0:
		fmt.Fprintf(&b, "**Saída do programa:** código %d\n", r.Exit)
	}
	if out != "" {
		fmt.Fprintf(&b, "```\n%s\n```\n", out)
	}
	_, err = s.o.Scrolls.AppendSection(ctx, c.Workspace, c.Table, name, b.String())
	return err
}
