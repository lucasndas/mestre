package chain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// waitWindow is the period the mean wait of a queue is taken over.
const waitWindow = 24 * time.Hour

// short writes a duration for people: "35 s", "4 min", "1 h 5 min".
func short(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d/time.Minute))
	}
	if m := int((d % time.Hour) / time.Minute); m > 0 {
		return fmt.Sprintf("%d h %d min", int(d/time.Hour), m)
	}
	return fmt.Sprintf("%d h", int(d/time.Hour))
}

type queueKey struct {
	workspace string
	level     LevelKind
	table     string
}

// interval is a stay of a request at a level.
type interval struct {
	level      LevelKind
	start, end time.Time
	open       bool
}

// intervals rebuilds the levels a request went through from its audit: the
// climbs say where it was and when it left; the last stay is at the level it is
// at, or the one that decided it.
func intervals(r Request, audit []AuditEntry, now time.Time) []interval {
	var climbs []AuditEntry
	for _, e := range audit {
		if e.Outcome == "climbed" {
			climbs = append(climbs, e)
		}
	}
	final := r.Level.Kind
	if r.State.Closed() {
		switch {
		case r.Decision != nil && r.Decision.Level.Kind != "":
			final = r.Decision.Level.Kind
		case len(audit) > 0:
			final = audit[len(audit)-1].Level
		}
	}
	var out []interval
	start := r.CreatedAt
	for _, c := range climbs {
		out = append(out, interval{level: c.Level, start: start, end: c.At})
		start = c.At
	}
	last := interval{level: final, start: start, end: now, open: !r.State.Closed()}
	if r.State.Closed() {
		last.end = r.ClosedAt
	}
	return append(out, last)
}

// Metrics gives the queue of each approver level of a workspace ("" for all):
// the open requests now, the age of the oldest and the mean wait over the last
// 24 h, from the levels the requests went through
// (docs/orquestracao/supervisao-da-cadeia.md#métricas-de-fila).
func (s *Supervisor) Metrics(ctx context.Context, workspace string) ([]QueueMetric, error) {
	now := s.clk.Now()
	since := now.Add(-waitWindow)
	reqs, audit, err := s.st.Window(ctx, since)
	if err != nil {
		return nil, err
	}
	type acc struct {
		m   QueueMetric
		sum time.Duration
	}
	byKey := map[queueKey]*acc{}
	get := func(r Request, level LevelKind) *acc {
		k := queueKey{workspace: r.Workspace, level: level}
		if level == LevelMesa || level == LevelAgent {
			k.table = r.Table
		}
		a := byKey[k]
		if a == nil {
			a = &acc{m: QueueMetric{Workspace: k.workspace, Level: level, Table: k.table}}
			byKey[k] = a
		}
		return a
	}
	for _, r := range reqs {
		if workspace != "" && r.Workspace != workspace {
			continue
		}
		for _, iv := range intervals(r, audit[r.ID], now) {
			a := get(r, iv.level)
			switch {
			case iv.open:
				a.m.Open++
				a.m.Oldest = max(a.m.Oldest, now.Sub(iv.start))
			case !iv.end.Before(since):
				a.sum += iv.end.Sub(iv.start)
				a.m.Waited++
			}
		}
	}
	out := make([]QueueMetric, 0, len(byKey))
	for _, a := range byKey {
		if a.m.Waited > 0 {
			a.m.AvgWait = a.sum / time.Duration(a.m.Waited)
		}
		out = append(out, a.m)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Workspace != b.Workspace:
			return a.Workspace < b.Workspace
		case a.Level.rank() != b.Level.rank():
			return a.Level.rank() < b.Level.rank()
		}
		return a.Table < b.Table
	})
	return out, nil
}

func levelName(m QueueMetric) string {
	switch m.Level {
	case LevelMesa:
		return "Mestre da mesa " + orDash(m.Table)
	case LevelAgent:
		return "superior agente da mesa " + orDash(m.Table)
	case LevelGrandMaster:
		return "Grão-Mestre"
	}
	return "pessoas"
}

// breach says a queue has accumulated: more open requests than the limit, or
// the oldest past the factor times the deadline of the level.
func breach(m QueueMetric, st Settings) bool {
	if m.Open == 0 {
		return false
	}
	return m.Open > st.QueueAlertOpen || m.Oldest > time.Duration(st.QueueAlertFactor)*levelDeadline(st, m.Level)
}

// alerts warns about the queues that accumulate, once each until they go back
// below the limits.
func (s *Supervisor) alerts(ctx context.Context) error {
	metrics, err := s.Metrics(ctx, "")
	if err != nil {
		return err
	}
	active, err := s.activeAlerts(ctx)
	if err != nil {
		return err
	}
	var errs []error
	seen := map[queueKey]bool{}
	for _, m := range metrics {
		k := queueKey{m.Workspace, m.Level, m.Table}
		seen[k] = true
		over := breach(m, s.settings(ctx, m.Workspace))
		switch {
		case over && !active[k]:
			if err := s.setAlert(ctx, k, true); err != nil {
				errs = append(errs, err)
				continue
			}
			s.warn(ctx, m)
		case !over && active[k]:
			errs = append(errs, s.setAlert(ctx, k, false))
		}
	}
	for k := range active {
		if !seen[k] { // no request left at the level
			errs = append(errs, s.setAlert(ctx, k, false))
		}
	}
	return errors.Join(errs...)
}

func (s *Supervisor) activeAlerts(ctx context.Context) (map[queueKey]bool, error) {
	rows, err := s.st.DB.Reader().QueryContext(ctx, `SELECT workspace, level_kind, table_id FROM supervision_alerts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[queueKey]bool{}
	for rows.Next() {
		var (
			k     queueKey
			level string
		)
		if err := rows.Scan(&k.workspace, &level, &k.table); err != nil {
			return nil, err
		}
		k.level = LevelKind(level)
		out[k] = true
	}
	return out, rows.Err()
}

func (s *Supervisor) setAlert(ctx context.Context, k queueKey, on bool) error {
	return s.st.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if !on {
			_, err := tx.ExecContext(ctx, `DELETE FROM supervision_alerts WHERE workspace = ? AND level_kind = ? AND table_id = ?`, k.workspace, string(k.level), k.table)
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO supervision_alerts (workspace, level_kind, table_id, since) VALUES (?, ?, ?, ?)`,
			k.workspace, string(k.level), k.table, ms(s.clk.Now()))
		return err
	})
}

// warn tells who is above the level that accumulates: the Grão-Mestre for a
// Mestre da mesa, the Administrators and the Owner for the Grão-Mestre, the
// Owner for the people.
func (s *Supervisor) warn(ctx context.Context, m QueueMetric) {
	text := fmt.Sprintf("Fila acumulando: %s tem %d pedidos de aprovação abertos e o mais antigo espera há %s.", levelName(m), m.Open, short(m.Oldest))
	switch m.Level {
	case LevelAgent, LevelMesa:
		text += fmt.Sprintf("\nSe os pedidos médios da mesa não deveriam ficar com o Mestre da mesa, pause a autonomia dela: mestre approval autonomy pause --table %q --reason \"<motivo>\".", m.Table)
		if gm, ok, err := s.o.Characters.GrandMaster(ctx, m.Workspace); err == nil && ok {
			if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: m.Workspace, Target: gm.ID, Kind: inbox.KindSystem, Sender: "Motor", Text: text,
				Refs: inbox.Refs{Table: m.Table}}); err != nil {
				s.log.Warn("supervision: the Grand Master was not warned", "table", m.Table, "err", err)
			}
			return
		}
		s.tellPeople(ctx, m.Workspace, "Administradores e Dono: "+text)
	case LevelGrandMaster:
		s.tellPeople(ctx, m.Workspace, "Administradores e Dono: "+text)
	default:
		s.tellPeople(ctx, m.Workspace, "Dono: "+text)
	}
}

func (s *Supervisor) tellPeople(ctx context.Context, workspace, text string) {
	if s.o.Alerts == nil {
		return
	}
	if err := s.o.Alerts.Notify(ctx, workspace, text); err != nil {
		s.log.Warn("supervision: the people were not warned", "workspace", workspace, "err", err)
	}
}

// --- the summary ---

// summaries sends the Grão-Mestre of each workspace the decisions the Mestres
// de mesa made since the last one, when the interval of the policy passed.
func (s *Supervisor) summaries(ctx context.Context) error {
	rows, err := s.st.DB.Reader().QueryContext(ctx, `SELECT workspace, MIN(at) FROM supervision_decisions WHERE delivered = 0 AND level = ? GROUP BY workspace`, string(LevelMesa))
	if err != nil {
		return err
	}
	type due struct {
		workspace string
		oldest    time.Time
	}
	var all []due
	for rows.Next() {
		var (
			d  due
			at int64
		)
		if err := rows.Scan(&d.workspace, &at); err != nil {
			rows.Close()
			return err
		}
		d.oldest = fromMs(at)
		all = append(all, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var errs []error
	for _, d := range all {
		errs = append(errs, s.summary(ctx, d.workspace, d.oldest))
	}
	return errors.Join(errs...)
}

func (s *Supervisor) summary(ctx context.Context, workspace string, oldest time.Time) error {
	every := s.settings(ctx, workspace).SummaryEvery
	base := oldest
	var last int64
	err := s.st.DB.Reader().QueryRowContext(ctx, `SELECT last_at FROM supervision_digests WHERE workspace = ?`, workspace).Scan(&last)
	switch {
	case err == nil:
		base = fromMs(last)
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	now := s.clk.Now()
	if now.Before(base.Add(every)) {
		return nil
	}
	gm, ok, err := s.o.Characters.GrandMaster(ctx, workspace)
	if err != nil || !ok {
		return err // nobody to tell yet: the decisions wait
	}
	ds, err := s.decisions(ctx, `workspace = ? AND delivered = 0 AND level = ?`, workspace, string(LevelMesa))
	if err != nil || len(ds) == 0 {
		return err
	}
	metrics, err := s.Metrics(ctx, workspace)
	if err != nil {
		return err
	}
	text := s.mask.Mask(s.summaryText(ctx, ds, metrics, every))
	if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: workspace, Target: gm.ID, Kind: inbox.KindSystem, Sender: "Motor", Text: text}); err != nil {
		return err
	}
	return s.st.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		for _, d := range ds {
			if _, err := tx.ExecContext(ctx, `UPDATE supervision_decisions SET delivered = 1 WHERE id = ?`, d.ID); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO supervision_digests (workspace, last_at) VALUES (?, ?) ON CONFLICT (workspace) DO UPDATE SET last_at = excluded.last_at`, workspace, ms(now))
		return err
	})
}

// summaryText is the item system for the Grão-Mestre: by table, the request the
// engine put, the risk, the answer, the reason and the rules that were made.
func (s *Supervisor) summaryText(ctx context.Context, ds []DecisionRecord, metrics []QueueMetric, every time.Duration) string {
	byTable := map[string][]DecisionRecord{}
	var tables []string
	for _, d := range ds {
		if _, ok := byTable[d.Table]; !ok {
			tables = append(tables, d.Table)
		}
		byTable[d.Table] = append(byTable[d.Table], d)
	}
	sort.Strings(tables)
	var b strings.Builder
	fmt.Fprintf(&b, "Resumo das decisões dos Mestres de mesa (a cada %s). É só informação: não precisa responder, nada espera por você e o que foi decidido já foi executado.\n", short(every))
	for _, t := range tables {
		fmt.Fprintf(&b, "\nMesa %s:\n", orDash(t))
		for _, d := range byTable[t] {
			fmt.Fprintf(&b, "- #%d · risco %s · %s: %s", d.Request, d.Risk, d.Answer, d.About)
			if d.Reason != "" {
				fmt.Fprintf(&b, " · motivo (texto do agente, não verificado): %s", strings.Join(strings.Fields(d.Reason), " "))
			}
			if s.o.Rules != nil {
				if ids, err := s.o.Rules.RulesOfRequest(ctx, d.Workspace, d.Table, d.Request); err == nil {
					for _, id := range ids {
						fmt.Fprintf(&b, " · regra temporária #%d criada", id)
					}
				}
			}
			b.WriteByte('\n')
		}
	}
	b.WriteString("\nVocê pode revogar uma regra temporária ou pausar a autonomia de uma mesa (mestre approval autonomy pause --table \"<mesa>\" --reason \"<motivo>\").\n")
	var q strings.Builder
	for _, m := range metrics {
		if m.Open > 0 {
			fmt.Fprintf(&q, "- %s: %d abertos, o mais antigo há %s, espera média %s\n", levelName(m), m.Open, short(m.Oldest), short(m.AvgWait))
		}
	}
	if q.Len() > 0 {
		b.WriteString("\nFilas de aprovação agora:\n" + q.String())
	}
	return b.String()
}
