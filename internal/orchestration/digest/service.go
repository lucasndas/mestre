package digest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Window is how far back the repeated approvals count for a suggestion, and how
// long a suggestion without an answer stays in the summaries.
const Window = 7 * 24 * time.Hour

// DeclinedFor is how long a declined pattern is not suggested again.
const DeclinedFor = 30 * 24 * time.Hour

// Source is the chain store: the requests of the window with their audit, the
// "always" suggestions of the agents, one request, and the audit.
type Source interface {
	Window(ctx context.Context, since time.Time) ([]chain.Request, map[int64][]chain.AuditEntry, error)
	Suggestions(ctx context.Context) ([]chain.Suggestion, error)
	Get(ctx context.Context, id int64) (chain.Request, error)
	Audit(ctx context.Context, e chain.AuditEntry) error
}

// Inbox puts the list in the inbox of the Grão-Mestre of a workspace.
type Inbox interface {
	ToGrandMaster(ctx context.Context, workspace, text string) error
}

// Person is somebody the summary goes to.
type Person struct{ ID string }

// Directory finds the people: `para` of the policy names `dono`,
// `administradores` or a user by name.
type Directory interface {
	Recipients(ctx context.Context, workspace string, names []string) ([]Person, error)
	Owners(ctx context.Context) ([]Person, error)
	// MayMark says the user may mark a decision as a mistake (Administrator or
	// above, in the workspace).
	MayMark(ctx context.Context, userID, workspace string) bool
}

// Button is a button of a message.
type Button struct{ Text, Action, Arg string }

// Message is what goes to a person.
type Message struct {
	UserID    string
	Workspace string
	Text      string
	Buttons   [][]Button
}

// Sender delivers a message to a person (the Telegram channel).
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Rand is the random source of the sample (the engine injects it, so a test
// fixes the seed).
type Rand interface{ IntN(n int) int }

// Rules revokes the temporary table rules a request made.
type Rules interface {
	RevokeOfRequest(ctx context.Context, workspace, table string, request int64, by string) (int, error)
}

// Proposer makes the proposal of a decree (decrees.Service).
type Proposer interface {
	Propose(ctx context.Context, p decrees.Proposal) (decrees.Pending, error)
}

// Names gives the name of a character.
type Names func(ctx context.Context, id string) string

// Options are the pieces of the Service; all but the Names, Rules, Mask, Zone and
// Logger are required.
type Options struct {
	DB        *store.DB
	Clock     clock.Clock
	Source    Source
	Policy    chain.Policy
	Inbox     Inbox
	Directory Directory
	Sender    Sender
	Rand      Rand
	Decrees   Proposer
	Rules     Rules
	Names     Names
	// Mask hides secrets in the text (default: the masker of the logs).
	Mask func(string) string
	// Zone is the time zone of the workspace (default UTC).
	Zone   func(ctx context.Context, workspace string) *time.Location
	Logger *slog.Logger
}

// Service makes the summaries.
type Service struct {
	o   Options
	clk clock.Clock
	log *slog.Logger
}

// New returns the Service.
func New(o Options) (*Service, error) {
	if o.DB == nil || o.Clock == nil || o.Source == nil || o.Policy == nil || o.Inbox == nil || o.Directory == nil || o.Sender == nil || o.Rand == nil || o.Decrees == nil {
		return nil, errors.New("digest: the database, the clock, the chain, the policy, the inbox, the directory, the sender, the random source and the decrees are required")
	}
	if o.Mask == nil {
		o.Mask = logging.NewMasker().Mask
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{o: o, clk: o.Clock, log: log}, nil
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMs(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

// Decision is one decision of an agent of the chain.
type Decision struct {
	Request   int64
	Workspace string
	Table     string
	Level     chain.LevelKind
	Approver  string
	Approved  bool
	Answer    chain.Answer
	Risk      string
	About     string
	Reason    string
	Class     string
	Action    *classes.Action
	Type      classes.RequestType
	At        time.Time
}

// Loop runs Tick every so often until the context ends.
func (s *Service) Loop(ctx context.Context, every time.Duration) error {
	t := s.clk.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C():
			if err := s.Tick(ctx); err != nil {
				s.log.Warn("digest: tick", "err", err)
			}
		}
	}
}

// Tick sends the summary of each workspace whose time has come. The first time
// the engine sees a workspace it only notes the slot: nothing from before it is
// sent.
func (s *Service) Tick(ctx context.Context) error {
	rows, err := s.o.DB.Reader().QueryContext(ctx, `SELECT slug FROM workspaces WHERE state = 'active' ORDER BY slug`)
	if err != nil {
		return err
	}
	var ws []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			_ = rows.Close()
			return err
		}
		ws = append(ws, w)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	var firstErr error
	for _, w := range ws {
		if err := s.tickOf(ctx, w); err != nil {
			s.log.Warn("digest: the summary of a workspace failed", "workspace", w, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// slotOf is the latest moment at the time of day that is not after now.
func slotOf(now time.Time, at string, loc *time.Location) (time.Time, error) {
	t, err := time.Parse("15:04", at)
	if err != nil {
		return time.Time{}, err
	}
	n := now.In(loc)
	slot := time.Date(n.Year(), n.Month(), n.Day(), t.Hour(), t.Minute(), 0, 0, loc)
	if slot.After(n) {
		slot = slot.AddDate(0, 0, -1)
	}
	return slot.UTC(), nil
}

func (s *Service) tickOf(ctx context.Context, workspace string) error {
	st, _, err := s.o.Policy.Chain(ctx, workspace)
	if err != nil {
		return err
	}
	st = st.WithDefaults()
	sum := st.Summary
	if sum.Frequency == chain.SummaryOff {
		return nil
	}
	loc := time.UTC
	if s.o.Zone != nil {
		if l := s.o.Zone(ctx, workspace); l != nil {
			loc = l
		}
	}
	now := s.clk.Now()
	slot, err := slotOf(now, sum.At, loc)
	if err != nil {
		return err
	}
	var last int64
	err = s.o.DB.Reader().QueryRowContext(ctx, `SELECT last_slot FROM digest_state WHERE workspace = ?`, workspace).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return s.saveSlot(ctx, workspace, slot)
	} else if err != nil {
		return err
	}
	lastSlot := fromMs(last)
	period := 24 * time.Hour
	if sum.Frequency == chain.SummaryWeekly {
		period = 7 * 24 * time.Hour
	}
	if slot.Sub(lastSlot) < period-time.Hour { // a day with 23 or 25 hours is still a day
		return nil
	}
	reqs, audit, err := s.send(ctx, workspace, sum, lastSlot, slot)
	if err != nil {
		return err
	}
	// The list and the sample are out: the slot is done even if the suggestions
	// fail, or the whole summary would go out again at every tick.
	if err := s.saveSlot(ctx, workspace, slot); err != nil {
		return err
	}
	if err := s.suggestions(ctx, workspace, sum, reqs, audit, slot); err != nil {
		s.log.Warn("digest: the suggestions of a workspace failed", "workspace", workspace, "err", err)
	}
	return nil
}

func (s *Service) saveSlot(ctx context.Context, workspace string, slot time.Time) error {
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO digest_state (workspace, last_slot) VALUES (?, ?) ON CONFLICT (workspace) DO UPDATE SET last_slot = excluded.last_slot`, workspace, ms(slot))
		return err
	})
}

func (s *Service) name(ctx context.Context, id string) string {
	if s.o.Names != nil {
		if n := s.o.Names(ctx, id); n != "" {
			return n
		}
	}
	return id
}

// decisions are the decisions of the agents of the chain of a workspace that
// closed in (from, to]: the last accepted answer of the Grão-Mestre or of a
// Mestre da mesa on each request.
func (s *Service) decisions(ctx context.Context, workspace string, reqs []chain.Request, audit map[int64][]chain.AuditEntry, from, to time.Time) []Decision {
	var out []Decision
	for _, r := range reqs {
		if r.Workspace != workspace || (r.State != chain.Approved && r.State != chain.Denied) || r.ClosedAt.IsZero() {
			continue
		}
		if !r.ClosedAt.After(from) || r.ClosedAt.After(to) {
			continue
		}
		var by *chain.AuditEntry
		for i := range audit[r.ID] {
			e := &audit[r.ID][i]
			if e.Outcome == "accepted" && (e.Level == chain.LevelMesa || e.Level == chain.LevelGrandMaster) && e.Approver != "" && (e.Answer.Approves() || e.Answer.Denies()) {
				by = e
			}
		}
		if by == nil {
			continue
		}
		out = append(out, Decision{Request: r.ID, Workspace: workspace, Table: r.Table, Level: by.Level, Approver: by.Approver, Approved: r.State == chain.Approved,
			Answer: by.Answer, Risk: string(r.Risk.Risk), About: r.About, Reason: by.Reason, Class: r.Class, Action: r.Action, Type: r.Type, At: r.ClosedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Request < out[j].Request })
	return out
}

func levelWord(l chain.LevelKind) string {
	if l == chain.LevelGrandMaster {
		return "Grão-Mestre"
	}
	return "Mestre da mesa"
}

func (s *Service) send(ctx context.Context, workspace string, sum chain.SummarySettings, from, to time.Time) ([]chain.Request, map[int64][]chain.AuditEntry, error) {
	since := to.Add(-Window)
	if from.Before(since) {
		since = from
	}
	reqs, audit, err := s.o.Source.Window(ctx, since)
	if err != nil {
		return nil, nil, err
	}
	ds := s.decisions(ctx, workspace, reqs, audit, from, to)
	people, err := s.o.Directory.Recipients(ctx, workspace, sum.For)
	if err != nil {
		return nil, nil, err
	}
	if len(ds) > 0 {
		if err := s.o.Inbox.ToGrandMaster(ctx, workspace, s.o.Mask(s.listText(ctx, ds, from, to))); err != nil {
			s.log.Warn("digest: the Grand Master was not given the list", "workspace", workspace, "err", err)
		}
		s.sample(ctx, workspace, people, sum.Sample, ds)
	}
	return reqs, audit, nil
}

// suggestions makes the suggestions of the period and sends the open ones to the Owner.
func (s *Service) suggestions(ctx context.Context, workspace string, sum chain.SummarySettings, reqs []chain.Request, audit map[int64][]chain.AuditEntry, to time.Time) error {
	owners, err := s.o.Directory.Owners(ctx)
	if err != nil {
		return err
	}
	if err := s.suggest(ctx, workspace, sum, reqs, audit, to); err != nil {
		return err
	}
	return s.sendSuggestions(ctx, workspace, owners, to)
}

func (s *Service) listText(ctx context.Context, ds []Decision, from, to time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Resumo do que os agentes da cadeia decidiram desde %s. É só informação: escreva um resumo curto e envie a quem a política manda (mestre notify). As sugestões de regra e a amostra para revisão o motor manda direto aos usuários.\n", from.UTC().Format("02/01 15:04 UTC"))
	for _, d := range ds {
		verb := "negou"
		if d.Approved {
			verb = "aprovou"
		}
		fmt.Fprintf(&b, "- #%d · %s %s %s · risco %s · mesa %s: %s", d.Request, levelWord(d.Level), s.name(ctx, d.Approver), verb, orDash(d.Risk), orDash(d.Table), oneLine(d.About))
		if d.Reason != "" {
			fmt.Fprintf(&b, " · motivo (texto do agente, não verificado): %s", strings.Join(strings.Fields(d.Reason), " "))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func oneLine(v string) string { return strings.Join(strings.Fields(v), " ") }

func orDash(v string) string {
	if v == "" {
		return "—"
	}
	return v
}

// sample draws, for each level, up to n approved actions and sends them to the
// people, each level in its own message with a button "Foi um erro" per action.
func (s *Service) sample(ctx context.Context, workspace string, people []Person, n int, ds []Decision) {
	if n <= 0 {
		return
	}
	for _, level := range []chain.LevelKind{chain.LevelMesa, chain.LevelGrandMaster} {
		var pool []Decision
		for _, d := range ds {
			if d.Level == level && d.Approved {
				pool = append(pool, d)
			}
		}
		if len(pool) == 0 {
			continue
		}
		// a partial shuffle with the injected source: the first n are the draw
		for i := 0; i < len(pool) && i < n; i++ {
			j := i + s.o.Rand.IntN(len(pool)-i)
			pool[i], pool[j] = pool[j], pool[i]
		}
		total := len(pool)
		if len(pool) > n {
			pool = pool[:n]
		}
		sort.Slice(pool, func(i, j int) bool { return pool[i].Request < pool[j].Request })
		var b strings.Builder
		fmt.Fprintf(&b, "Amostra para revisão: ações aprovadas por %s (%d de %d do período). Foi um erro? O botão registra a marcação e revoga a regra temporária que nasceu da decisão.\n", levelWord(level), len(pool), total)
		var rows [][]Button
		for _, d := range pool {
			fmt.Fprintf(&b, "\n#%d · %s · nível %s · risco %s · mesa %s\n  pedido: %s\n", d.Request, s.name(ctx, d.Approver), levelWord(d.Level), orDash(d.Risk), orDash(d.Table), oneLine(d.About))
			if d.Reason != "" {
				fmt.Fprintf(&b, "  motivo (texto do agente, não verificado): %s\n", strings.Join(strings.Fields(d.Reason), " "))
			}
			rows = append(rows, []Button{{Text: fmt.Sprintf("Foi um erro #%d", d.Request), Action: MistakeAction, Arg: fmt.Sprint(d.Request)}})
		}
		// Pausar autonomia, one per table of the sample: whoever reads what the agents
		// decided can stop the decisions of that table there (docs/orquestracao/supervisao-da-cadeia.md)
		var pauses [][]Button
		seen := map[string]bool{}
		for _, d := range pool {
			if d.Table != "" && !seen[d.Table] {
				seen[d.Table] = true
				pauses = append(pauses, []Button{{Text: "Pausar autonomia " + d.Table, Action: PauseAutonomyAction, Arg: workspace + ":" + d.Table}})
			}
		}
		for _, p := range people {
			m := Message{UserID: p.ID, Workspace: workspace, Text: s.o.Mask(b.String()), Buttons: pauses}
			if s.o.Directory.MayMark(ctx, p.ID, workspace) {
				m.Buttons = append(append([][]Button(nil), rows...), pauses...)
			}
			if err := s.o.Sender.Send(ctx, m); err != nil {
				s.log.Warn("digest: the sample was not sent", "user", p.ID, "err", err)
			}
		}
	}
}

// The buttons of the summary.
const (
	MistakeAction  = "digest_mistake"
	AcceptAction   = "digest_accept"
	DeclineAction  = "digest_decline"
	ExamplesAction = "digest_examples"
	// PauseAutonomyAction is the button **Pausar autonomia** of a table of the sample
	// (the same of the warning of a queue, telegram.PauseAutonomyAction); its argument is
	// "<workspace>:<table>".
	PauseAutonomyAction = "pause_autonomy"
)

// Mark is the click on "Foi um erro": the mark goes to the audit of the decision
// and the temporary rule that came of it is revoked.
func (s *Service) Mark(ctx context.Context, request int64, userID string) (int, error) {
	r, err := s.o.Source.Get(ctx, request)
	if err != nil {
		return 0, err
	}
	if !s.o.Directory.MayMark(ctx, userID, r.Workspace) {
		return 0, errors.New("digest: only an Administrator or above marks a decision as a mistake")
	}
	fresh := false
	err = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO digest_marks (request, by_user, at) VALUES (?, ?, ?) ON CONFLICT (request) DO NOTHING`, request, userID, ms(s.clk.Now()))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		fresh = n > 0
		return nil
	})
	if err != nil {
		return 0, err
	}
	if !fresh {
		return 0, nil // already marked: neither the audit nor the rules again
	}
	if err := s.o.Source.Audit(ctx, chain.AuditEntry{Request: request, At: s.clk.Now(), Approver: userID, Answer: "marked_error", Outcome: "accepted", Reason: "marcada como erro na amostra do resumo"}); err != nil {
		return 0, err
	}
	if s.o.Rules == nil {
		return 0, nil
	}
	return s.o.Rules.RevokeOfRequest(ctx, r.Workspace, r.Table, request, userID)
}

// candidate is the pattern of a suggestion for a command: the program and the
// subcommand, literal, then " *" (`npm install lodash` is `npm install *`). A
// chained command, a flag in place of the subcommand or a command with fewer
// than three words is no candidate.
func candidate(a classes.Action) string {
	if a.Kind != providers.KindShell || strings.ContainsAny(a.Command, ";&|<>$`()\n\r\\\"'*?") {
		return ""
	}
	f := strings.Fields(a.Command)
	if len(f) < 3 || strings.HasPrefix(f[1], "-") || strings.HasPrefix(f[0], "-") {
		return ""
	}
	return f[0] + " " + f[1] + " *"
}

type examples struct {
	Actions []string       `json:"actions"`
	Example classes.Action `json:"example"`
	Risk    string         `json:"risk"`
	Rule    string         `json:"rule"`
}

type found struct {
	workspace, class, pattern, effect, source, reason string
	tables                                            map[string]bool
	actions                                           []string
	example                                           classes.Action
	request                                           int64
}

func (s *Service) suggest(ctx context.Context, workspace string, sum chain.SummarySettings, reqs []chain.Request, audit map[int64][]chain.AuditEntry, to time.Time) error {
	byKey := map[string]*found{}
	var order []string
	since := to.Add(-Window)
	for _, r := range reqs {
		if r.Workspace != workspace || r.State != chain.Approved || r.Type != classes.RequestClassPermission || r.Action == nil || r.Class == "" || !r.ClosedAt.After(since) {
			continue
		}
		p := candidate(*r.Action)
		if p == "" {
			continue
		}
		if _, err := classes.CheckApprovablePattern(p, *r.Action, r.Class, r.Table); err != nil {
			continue
		}
		k := r.Class + "\x00" + p
		f := byKey[k]
		if f == nil {
			f = &found{workspace: workspace, class: r.Class, pattern: p, effect: "allow", source: "repeat", tables: map[string]bool{}, example: *r.Action, request: r.ID}
			byKey[k] = f
			order = append(order, k)
		}
		f.tables[r.Table] = true
		f.actions = append(f.actions, r.Action.String())
	}
	for _, k := range order {
		f := byKey[k]
		if len(f.actions) >= sum.SuggestAfter {
			if err := s.keep(ctx, *f, to); err != nil {
				return err
			}
		}
	}
	always, err := s.o.Source.Suggestions(ctx)
	if err != nil {
		return err
	}
	for _, a := range always {
		if a.Kind != chain.AnswerApproveAlways && a.Kind != chain.AnswerDenyAlways {
			continue
		}
		if !a.At.After(since) {
			continue
		}
		r, err := s.o.Source.Get(ctx, a.Request)
		if err != nil || r.Workspace != workspace || r.Action == nil || r.Class == "" {
			continue
		}
		p := candidate(*r.Action)
		if p == "" {
			continue
		}
		if _, err := classes.CheckApprovablePattern(p, *r.Action, r.Class, r.Table); err != nil {
			continue
		}
		effect := "allow"
		if a.Kind == chain.AnswerDenyAlways {
			effect = "deny"
		}
		f := found{workspace: workspace, class: r.Class, pattern: p, effect: effect, source: "always", reason: a.Reason, tables: map[string]bool{r.Table: true},
			actions: []string{r.Action.String()}, example: *r.Action, request: r.ID}
		if err := s.keep(ctx, f, to); err != nil {
			return err
		}
	}
	return nil
}

// keep records a suggestion unless the same one is open or was declined lately.
func (s *Service) keep(ctx context.Context, f found, now time.Time) error {
	mesa := ""
	if len(f.tables) == 1 {
		for t := range f.tables {
			mesa = t
		}
	}
	check, err := classes.CheckApprovablePattern(f.pattern, f.example, f.class, mesa)
	if err != nil {
		return nil
	}
	body, err := json.Marshal(examples{Actions: dedupe(f.actions), Example: f.example, Risk: string(check.Risk.Risk), Rule: check.Risk.Rule})
	if err != nil {
		return err
	}
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM digest_suggestions WHERE workspace = ? AND class = ? AND mesa = ? AND pattern = ? AND effect = ? AND (state = 'open' OR (state = 'declined' AND declined_until > ?) OR (state = 'accepted' AND created_at > ?))`,
			f.workspace, f.class, mesa, f.pattern, f.effect, ms(now), ms(now.Add(-Window))).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO digest_suggestions (workspace, class, mesa, pattern, effect, source, reason, state, body, request, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'open', ?, ?, ?)`,
			f.workspace, f.class, mesa, f.pattern, f.effect, f.source, f.reason, string(body), f.request, ms(now))
		return err
	})
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// Suggestion is a suggestion of rule as it is kept.
type Suggestion struct {
	ID        int64
	Workspace string
	Class     string
	Mesa      string
	Pattern   string
	Effect    string
	Source    string
	Reason    string
	State     string
	Examples  examples
	CreatedAt time.Time
}

func scanSuggestion(sc interface{ Scan(...any) error }) (Suggestion, error) {
	var x Suggestion
	var body string
	var at int64
	if err := sc.Scan(&x.ID, &x.Workspace, &x.Class, &x.Mesa, &x.Pattern, &x.Effect, &x.Source, &x.Reason, &x.State, &body, &at); err != nil {
		return Suggestion{}, err
	}
	x.CreatedAt = fromMs(at)
	_ = json.Unmarshal([]byte(body), &x.Examples)
	return x, nil
}

const suggestionColumns = `id, workspace, class, mesa, pattern, effect, source, reason, state, body, created_at`

// Suggestions lists the suggestions of a workspace, in a state if given.
func (s *Service) Suggestions(ctx context.Context, workspace, state string) ([]Suggestion, error) {
	q := `SELECT ` + suggestionColumns + ` FROM digest_suggestions WHERE workspace = ?`
	args := []any{workspace}
	if state != "" {
		q += ` AND state = ?`
		args = append(args, state)
	}
	rows, err := s.o.DB.Reader().QueryContext(ctx, q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Suggestion
	for rows.Next() {
		x, err := scanSuggestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Suggestion is one suggestion.
func (s *Service) Suggestion(ctx context.Context, id int64) (Suggestion, error) {
	return scanSuggestion(s.o.DB.Reader().QueryRowContext(ctx, `SELECT `+suggestionColumns+` FROM digest_suggestions WHERE id = ?`, id))
}

// sendSuggestions sends the open ones to the Owner, and lets the ones older
// than a week go.
func (s *Service) sendSuggestions(ctx context.Context, workspace string, owners []Person, now time.Time) error {
	if err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE digest_suggestions SET state = 'expired' WHERE workspace = ? AND state = 'open' AND created_at <= ?`, workspace, ms(now.Add(-Window)))
		return err
	}); err != nil {
		return err
	}
	open, err := s.Suggestions(ctx, workspace, "open")
	if err != nil {
		return err
	}
	for _, x := range open {
		text := s.o.Mask(s.suggestionText(x, false))
		for _, o := range owners {
			err := s.o.Sender.Send(ctx, Message{UserID: o.ID, Workspace: workspace, Text: text, Buttons: [][]Button{{
				{Text: "Aceitar", Action: AcceptAction, Arg: fmt.Sprint(x.ID)}, {Text: "Recusar", Action: DeclineAction, Arg: fmt.Sprint(x.ID)}, {Text: "Ver exemplos", Action: ExamplesAction, Arg: fmt.Sprint(x.ID)}}}})
			if err != nil {
				s.log.Warn("digest: a suggestion was not sent", "user", o.ID, "err", err)
			}
		}
	}
	return nil
}

func (s *Service) suggestionText(x Suggestion, more bool) string {
	verb := "permitir"
	if x.Effect == "deny" {
		verb = "negar"
	}
	scope := "no workspace " + x.Workspace
	if x.Mesa != "" {
		scope = "na mesa " + x.Mesa
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Sugestão de regra: %s `%s` para a classe %s, %s. Risco do padrão: %s.\n", verb, x.Pattern, x.Class, scope, orDash(x.Examples.Risk))
	if x.Source == "always" {
		fmt.Fprintf(&b, "Veio de um \"sempre\" de um agente. Motivo dele (texto do agente, não verificado): %s\n", orDash(strings.Join(strings.Fields(x.Reason), " ")))
	} else {
		b.WriteString("Ações já aprovadas que ele casa:\n")
		for i, a := range x.Examples.Actions {
			if i == 5 {
				break
			}
			fmt.Fprintf(&b, "- %s\n", a)
		}
	}
	if more {
		fmt.Fprintf(&b, "Libera a mais: a forma geral `%s`, com qualquer argumento no lugar do *. O que a tabela de risco diz do padrão: %s.\n", x.Pattern, orDash(x.Examples.Rule))
	} else {
		b.WriteString("Aceitar cria um decreto seu, que pede o PIN.\n")
	}
	return b.String()
}

// ExamplesText is the click on "Ver exemplos": what the pattern would free
// beyond the actions already approved.
func (s *Service) ExamplesText(ctx context.Context, id int64) (string, error) {
	x, err := s.Suggestion(ctx, id)
	if err != nil {
		return "", err
	}
	return s.o.Mask(s.suggestionText(x, true)), nil
}

// Errors of the answers to a suggestion.
var (
	ErrNotOpen  = errors.New("digest: the suggestion is not open")
	ErrNotOwner = errors.New("digest: only the Owner answers a suggestion")
)

// Decline is the click on "Recusar": the same pattern is not suggested again
// for 30 days.
func (s *Service) Decline(ctx context.Context, id int64, userID string) error {
	if err := s.owner(ctx, userID); err != nil {
		return err
	}
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE digest_suggestions SET state = 'declined', declined_until = ?, decided_by = ? WHERE id = ? AND state = 'open'`, ms(s.clk.Now().Add(DeclinedFor)), userID, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotOpen
		}
		return nil
	})
}

func (s *Service) owner(ctx context.Context, userID string) error {
	owners, err := s.o.Directory.Owners(ctx)
	if err != nil {
		return err
	}
	for _, o := range owners {
		if o.ID == userID {
			return nil
		}
	}
	return ErrNotOwner
}

// Accept is the click on "Aceitar": the pattern is checked again against the
// table of risk (a pattern that became fixo or no longer approvable is refused)
// and the proposal of the decree goes to the Owner, who confirms it with the
// PIN like any permanent decree. No decree exists before that.
func (s *Service) Accept(ctx context.Context, id int64, userID string) (decrees.Pending, error) {
	if err := s.owner(ctx, userID); err != nil {
		return decrees.Pending{}, err
	}
	x, err := s.Suggestion(ctx, id)
	if err != nil {
		return decrees.Pending{}, err
	}
	if x.State != "open" {
		return decrees.Pending{}, ErrNotOpen
	}
	check, err := classes.CheckApprovablePattern(x.Pattern, x.Examples.Example, x.Class, x.Mesa)
	if err != nil || check.Risk.Risk == classes.RiskFixed {
		return decrees.Pending{}, fmt.Errorf("digest: o padrão %q não vale mais como regra (%v)", x.Pattern, orErr(err, "a tabela de risco o dá como fixo"))
	}
	grant := &classes.Grant{Commands: []string{x.Pattern}}
	eff := decrees.Effect{Kind: decrees.EffectPermission, Allow: grant}
	if x.Effect == "deny" {
		eff = decrees.Effect{Kind: decrees.EffectPermission, Deny: grant}
	}
	claimed := false
	if err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE digest_suggestions SET state = 'accepted', decided_by = ? WHERE id = ? AND state = 'open'`, userID, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		claimed = n > 0
		return nil
	}); err != nil {
		return decrees.Pending{}, err
	}
	if !claimed {
		return decrees.Pending{}, ErrNotOpen
	}
	pend, err := s.o.Decrees.Propose(ctx, decrees.Proposal{Author: userID, Kind: decrees.KindRule, Effect: eff, Target: decrees.Target{Kind: decrees.TargetClass, Name: x.Class},
		Workspace: x.Workspace, Mesa: x.Mesa, Text: fmt.Sprintf("Sugestão do resumo %d: %s `%s` para a classe %s", x.ID, map[string]string{"allow": "permitir", "deny": "negar"}[x.Effect], x.Pattern, x.Class),
		Origin: decrees.Origin{Channel: "summary"}})
	if err != nil {
		// nothing was proposed: the suggestion is open again
		_ = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
			_, uerr := tx.ExecContext(ctx, `UPDATE digest_suggestions SET state = 'open', decided_by = '' WHERE id = ? AND state = 'accepted'`, id)
			return uerr
		})
		return decrees.Pending{}, err
	}
	return pend, nil
}

func orErr(err error, fallback string) string {
	if err != nil {
		return err.Error()
	}
	return fallback
}

// UsersDirectory is the Directory over the users of the Guild.
type UsersDirectory struct{ Users *users.Service }

// Recipients implements Directory.
func (d UsersDirectory) Recipients(ctx context.Context, workspace string, names []string) ([]Person, error) {
	all, err := d.Users.List(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Person
	add := func(u users.User) {
		if u.State == users.Active && u.Acts(workspace) && !seen[u.ID] {
			seen[u.ID] = true
			out = append(out, Person{ID: u.ID})
		}
	}
	for _, n := range names {
		for _, u := range all {
			switch strings.ToLower(strings.TrimSpace(n)) {
			case "dono":
				if u.Level == users.Owner {
					add(u)
				}
			case "administradores":
				if users.Can(u.Level, users.RevokeTemporaryRules) {
					add(u)
				}
			default:
				if strings.EqualFold(u.Name, strings.TrimSpace(n)) {
					add(u)
				}
			}
		}
	}
	return out, nil
}

// Owners implements Directory.
func (d UsersDirectory) Owners(ctx context.Context) ([]Person, error) {
	all, err := d.Users.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []Person
	for _, u := range all {
		if u.Level == users.Owner && u.State == users.Active {
			out = append(out, Person{ID: u.ID})
		}
	}
	return out, nil
}

// MayMark implements Directory.
func (d UsersDirectory) MayMark(ctx context.Context, userID, workspace string) bool {
	u, err := d.Users.Get(ctx, userID)
	return err == nil && u.State == users.Active && u.Acts(workspace) && users.Can(u.Level, users.RevokeTemporaryRules)
}
