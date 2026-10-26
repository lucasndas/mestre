package scrolls

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/replication"
	"github.com/mestre-ia/mestre/internal/store"
)

// HistoryName is the name of the scroll that keeps the history of a table: the
// summaries of the characters that were not in a quest.
const HistoryName = "Histórico da mesa"

// QuestScrollName is the fixed name of the scroll of a quest.
func QuestScrollName(quest string) string { return "Missão: " + quest }

// Ensure returns the scroll of the table with the fixed name, making it (with
// the content) if it does not exist. The engine uses it for the scroll of a
// quest and the history of a table.
func (s *Service) Ensure(ctx context.Context, workspace, table, name, content string) (Note, error) {
	s.mu.Lock()
	n, ok, err := s.st.Find(ctx, workspace, table, name)
	s.mu.Unlock()
	if err != nil {
		return Note{}, err
	}
	if ok {
		return n, nil
	}
	n, err = s.Create(ctx, CreateRequest{Workspace: workspace, Table: table, Actor: Engine, Name: name, Content: content})
	if errors.Is(err, ErrNameTaken) { // made by someone else in the meantime
		n, _, err = s.st.Find(ctx, workspace, table, name)
	}
	return n, err
}

// AppendSection adds a section to the end of a scroll of the engine: it never
// rewrites what is there (docs/orquestracao/pergaminhos-e-tomos.md#pergaminho-da-missão).
// The scroll is made, with the fixed name, if it does not exist.
func (s *Service) AppendSection(ctx context.Context, workspace, table, name, section string) (Note, error) {
	if _, err := s.Ensure(ctx, workspace, table, name, ""); err != nil {
		return Note{}, err
	}
	return s.Write(ctx, WriteRequest{Workspace: workspace, Table: table, Actor: Engine, Name: name, Append: true, Content: joinSection(section)})
}

// joinSection starts a section on a new paragraph.
func joinSection(section string) string {
	return "\n" + strings.TrimRight(section, "\n") + "\n"
}

// Record implements characters.Scrolls: the summary of a character that ends
// goes to the scroll of its quest or, without a quest, to the history of its
// table (docs/orquestracao/personagens.md#encerramento-seguro). A character
// with no table (the Grand Master) has no scroll to write in.
func (s *Service) Record(ctx context.Context, sum characters.Summary) error {
	c := sum.Character
	if c.Table == "" {
		return nil
	}
	name := HistoryName
	if c.Quest != "" {
		qn := c.Quest
		if s.o.Quests != nil {
			if n, ok := s.o.Quests.QuestName(ctx, c.Quest); ok {
				qn = n
			}
		}
		name = QuestScrollName(qn)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Encerramento · %s — %s — %s\n", c.Name, orDash(c.Class), s.clk.Now().UTC().Format("2006-01-02 15:04"))
	if n := len(sum.Dones); n > 0 {
		b.WriteString(inbox.Say("**Resumo:**", sum.Dones[n-1].Text, ">") + "\n")
	} else {
		b.WriteString("**Resumo:** (nenhum done)\n")
	}
	if sum.Usage != "" {
		fmt.Fprintf(&b, "**Uso:** %s\n", sum.Usage)
	}
	_, err := s.AppendSection(ctx, c.Workspace, c.Table, name, b.String())
	return err
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Cites says which file a change of the index of scrolls cites, for the
// replication to send it to the Towers before the row (the file is in the
// `.mestre/` of the main clone, which root is the folder that holds the
// clones: <workspace>/principal/.mestre/mesas/<table>/pergaminhos/<id>.md).
func Cites(root string) replication.Cites {
	return func(c store.Change) []replication.FileRef {
		if c.Table != "notes" || c.Op == store.OpDelete || len(c.New) < 4 {
			return nil
		}
		ws, _ := c.New[1].(string)
		table, _ := c.New[2].(string)
		id, _ := c.New[3].(string)
		if ws == "" || table == "" || id == "" {
			return nil
		}
		return []replication.FileRef{{Root: root, Path: ws + "/principal/" + Rel(table, id)}}
	}
}
