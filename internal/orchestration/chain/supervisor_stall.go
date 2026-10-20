package chain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// DefaultStallAfter is how long a table at work may deliver nothing useful
// before the warning (docs/orquestracao/supervisao-da-cadeia.md#entrega-útil-e-mesa-que-gira).
const DefaultStallAfter = 3 * time.Hour

// StalledTable is a table at work that delivered nothing useful since Since.
type StalledTable struct {
	Workspace, Table string
	Since            time.Time
}

// TableStalls is what the supervision asks of the tables for the warning of a
// table that spins (the Tables of the options may also be one): the tables
// that delivered nothing for longer than a time, the mark that one was warned
// about, and the pause of the table itself (the pause of mesas.md, not the pause
// of the autonomy).
type TableStalls interface {
	Stalled(ctx context.Context, after time.Duration) ([]StalledTable, error)
	StallWarned(ctx context.Context, workspace, table string, since time.Time) error
	// PauseTable says whether it paused (false: it already was).
	PauseTable(ctx context.Context, workspace, table, reason, by string) (bool, error)
}

// StallNotice is the warning of a stalled table to the people who may pause it
// (an Approver or above): the text, and the table the button **Pausar mesa**
// acts on.
type StallNotice struct {
	Workspace, Table string
	Since            time.Time
	Text             string
}

// StallPeople delivers the warning of a stalled table to the people, with the
// button **Pausar mesa** (the Telegram, milestone 5; the panel, 6).
type StallPeople interface {
	StalledTable(ctx context.Context, n StallNotice) error
}

// stalls warns, once for each quiet, about the tables that spin: an item
// system to the Grão-Mestre and the message with the button to the people.
func (s *Supervisor) stalls(ctx context.Context) error {
	ts, ok := s.o.Tables.(TableStalls)
	if !ok {
		return nil
	}
	after := s.o.StallAfter
	if after <= 0 {
		after = DefaultStallAfter
	}
	list, err := ts.Stalled(ctx, after) // what was found is warned about even if part of the search failed
	now := s.clk.Now()
	var errs []error
	for _, st := range list {
		quiet := now.Sub(st.Since).Truncate(time.Minute)
		text := fmt.Sprintf("A mesa %s está em trabalho há %s sem entregar nada: nenhum commit novo na branch, nenhum PR aberto ou atualizado e nenhuma etapa de missão concluída.", st.Table, short(quiet))
		// The people first: they hold the button. If they could not be told, nothing
		// is marked and the next round tries again, so the Grão-Mestre is not told twice.
		if s.o.StallPeople != nil {
			if err := s.o.StallPeople.StalledTable(ctx, StallNotice{Workspace: st.Workspace, Table: st.Table, Since: st.Since, Text: text}); err != nil {
				s.log.Warn("supervision: the people were not told of a stalled table", "table", st.Table, "err", err)
				continue
			}
		}
		told := s.o.StallPeople != nil
		if gm, ok, err := s.o.Characters.GrandMaster(ctx, st.Workspace); err == nil && ok {
			if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: st.Workspace, Target: gm.ID, Kind: inbox.KindSystem, Sender: "Motor",
				Text: text + "\nVeja o que ela faz (mestre table status) e, se estiver girando em vão, pause a mesa.", Refs: inbox.Refs{Table: st.Table}}); err != nil {
				s.log.Warn("supervision: the Grand Master was not told of a stalled table", "table", st.Table, "err", err)
			} else {
				told = true
			}
		}
		if !told {
			continue // nobody to tell yet: it is tried again at the next round
		}
		if err := ts.StallWarned(ctx, st.Workspace, st.Table, st.Since); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(append(errs, err)...)
}

// PauseStalled is the button **Pausar mesa** of the warning: it pauses the table
// itself (nothing is interrupted, the inbox stops delivering and no step
// begins), without PIN because it only restricts, and the audit says who and
// why. Only an Approver or above; a table that ended refuses; pausing a paused
// table succeeds without effect (false).
func (s *Supervisor) PauseStalled(ctx context.Context, r PauseRequest) (bool, error) {
	ts, ok := s.o.Tables.(TableStalls)
	if !ok {
		return false, errors.New("chain: the tables can not be paused from here")
	}
	if !r.By.approverOrAbove() {
		return false, fmt.Errorf("%w: an Approver or above", ErrLevelDenied)
	}
	if strings.TrimSpace(r.Reason) == "" {
		return false, ErrReasonRequired
	}
	over, err := s.o.Tables.Over(ctx, r.Workspace, r.Table)
	switch {
	case err != nil:
		return false, err
	case over:
		return false, fmt.Errorf("%w: %s", ErrTableOver, r.Table)
	}
	changed, err := ts.PauseTable(ctx, r.Workspace, r.Table, r.Reason, r.By.name())
	if err != nil || !changed {
		return false, err
	}
	now := s.clk.Now()
	// The table is paused and keeps who and why (paused_by, paused_reason); a failure
	// of the audit is logged, not turned into a refusal of a pause that happened.
	if err := s.st.DB.WriteDurable(ctx, func(tx *sql.Tx) error { return s.auditTx(ctx, tx, r, "pause_table", now) }); err != nil {
		s.log.Warn("supervision: the pause of a table was not audited", "table", r.Table, "by", r.By.name(), "err", err)
	}
	s.tellPause(ctx, r, fmt.Sprintf("A mesa %s foi pausada por %s: %s. Nada foi interrompido; a caixa de entrada para de entregar e nenhuma etapa nova começa até a retomada.", r.Table, s.who(ctx, r.By), r.Reason))
	return true, nil
}
