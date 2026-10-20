package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Service is the use and the limits on it
// (docs/plataforma/limites-de-uso.md).
type Service struct {
	o   Options
	st  *Store
	clk clock.Clock
	log *slog.Logger

	mu   sync.Mutex
	busy map[string]time.Time // when the turn of a character began
}

// NewService returns the Service.
func NewService(o Options) (*Service, error) {
	if o.Store == nil || o.Store.DB == nil || o.Directory == nil || o.Pauser == nil {
		return nil, errors.New("usage: the service needs the store, the directory and the pauser")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.ExtraShare <= 0 {
		o.ExtraShare = 0.5
	}
	return &Service{o: o, st: o.Store, clk: o.Clock, log: o.Logger, busy: map[string]time.Time{}}, nil
}

// day is the date of a moment in the time zone of the Guild.
func (s *Service) day(ctx context.Context, t time.Time) string {
	loc := time.UTC
	if s.o.Zone != nil {
		if z := s.o.Zone(ctx); z != nil {
			loc = z
		}
	}
	return t.In(loc).Format("2006-01-02")
}

func (s *Service) location(ctx context.Context) *time.Location {
	if s.o.Zone != nil {
		if z := s.o.Zone(ctx); z != nil {
			return z
		}
	}
	return time.UTC
}

// Today is the date the limits per day are counting.
func (s *Service) Today(ctx context.Context) string { return s.day(ctx, s.clk.Now()) }

// errInsert marks the error of a row that was not kept.
var errInsert = errors.New("the use was not kept")

// Record adds a piece of use and checks the limits it counts in. A limit
// that cannot be checked does not undo the record.
func (s *Service) Record(ctx context.Context, r Record) error {
	if err := r.validate(); err != nil {
		return err
	}
	if r.At.IsZero() {
		r.At = s.clk.Now()
	}
	day := s.day(ctx, r.At)
	if err := s.st.Insert(ctx, r, day); err != nil {
		return fmt.Errorf("usage: %w: %w", errInsert, err)
	}
	return s.check(ctx, r, day)
}

type target struct {
	scope       Scope
	key, period string
}

// targets are the limits a record counts in: the ones of the work of the
// steps, and the total of the day for everything.
func targets(r Record, day string) []target {
	var out []target
	if r.Origin == OriginStep {
		if r.Quest != "" {
			out = append(out, target{ScopeQuest, r.Quest, ""})
		}
		if r.Workspace != "" && r.Table != "" {
			out = append(out, target{ScopeTable, TableKey(r.Workspace, r.Table), ""})
		}
		if r.Workspace != "" {
			out = append(out, target{ScopeWorkspace, r.Workspace, ""}, target{ScopeDay, r.Workspace, day})
		}
	}
	return append(out, target{ScopeDayTotal, "", day})
}

func (s *Service) check(ctx context.Context, r Record, day string) error {
	var errs []error
	for _, t := range targets(r, day) {
		lim, ok, err := s.st.Limit(ctx, t.scope, t.key)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok {
			continue
		}
		if err := s.checkOne(ctx, r, lim, t.period); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// effective is the limit that holds in a period: the configured one, never
// above the cost ceiling, plus what was approved above it.
func (s *Service) effective(ctx context.Context, lim Limit, workspace, period string) (float64, error) {
	base := lim.Value
	if s.o.Ceilings != nil && workspace != "" && lim.Scope != ScopeDayTotal {
		c, err := s.o.Ceilings.Ceiling(ctx, workspace)
		if err != nil {
			return 0, fmt.Errorf("the ceiling of %s: %w", workspace, err)
		}
		if cp := c.Cap(lim.Unit, lim.Scope.ceilingScope()); cp != nil && *cp < base {
			base = *cp
		}
	}
	extra, err := s.st.Extra(ctx, lim.Scope, lim.Key, period)
	return base + extra, err
}

func (s *Service) checkOne(ctx context.Context, r Record, lim Limit, period string) error {
	eff, err := s.effective(ctx, lim, r.Workspace, period)
	if err != nil {
		return err
	}
	if eff <= 0 {
		return nil
	}
	tot, err := s.st.Sum(ctx, lim.Scope, lim.Key, period)
	if err != nil {
		return err
	}
	used := tot.Used(lim.Unit)
	now := s.clk.Now()
	switch ratio := used / eff; {
	case ratio >= 1:
		fresh, err := s.st.MarkAlert(ctx, lim.Scope, lim.Key, period, 100, now)
		if err != nil || !fresh {
			return err
		}
		// jumping over 80% says it once: the 100% covers it
		if _, err := s.st.MarkAlert(ctx, lim.Scope, lim.Key, period, 80, now); err != nil {
			return err
		}
		if err := s.reached(ctx, r, lim, period, used, eff); err != nil {
			// not enforced: the next record tries again
			_ = s.st.ClearAlert(ctx, lim.Scope, lim.Key, period, 100)
			return err
		}
		return nil
	case ratio >= 0.8:
		fresh, err := s.st.MarkAlert(ctx, lim.Scope, lim.Key, period, 80, now)
		if err != nil || !fresh {
			return err
		}
		s.tell(ctx, r.Workspace, r.Table, fmt.Sprintf("Uso em %.0f%%: %s chegou a %s de %s.", ratio*100, s.what(lim, r), amount(lim.Unit, used), amount(lim.Unit, eff)))
	}
	return nil
}

// what is the words for what the limit covers.
func (s *Service) what(lim Limit, r Record) string {
	switch lim.Scope {
	case ScopeQuest:
		return "a missão " + orElse(r.QuestName, lim.Key)
	case ScopeTable:
		return "a mesa " + orElse(r.TableName, r.Table)
	case ScopeWorkspace:
		return "o workspace " + lim.Key
	case ScopeDay:
		return "o dia do workspace " + lim.Key
	}
	return "o dia da instalação"
}

func orElse(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// amount writes a number in the unit of a limit.
func amount(u classes.Unit, v float64) string {
	switch u {
	case classes.UnitUSD:
		return fmt.Sprintf("US$ %.2f", v)
	case classes.UnitTokens:
		return fmt.Sprintf("%.0f tokens", v)
	}
	return fmt.Sprintf("%.1f h", v)
}

// reached is a limit at 100%: it pauses what the limit covers and asks for
// the extra. Nothing interrupts a turn.
func (s *Service) reached(ctx context.Context, r Record, lim Limit, period string, used, eff float64) error {
	extra := eff * s.o.ExtraShare
	if lim.Unit == classes.UnitTokens {
		extra = math.Ceil(extra)
	}
	paused, perr := s.pause(ctx, lim, r)
	what := s.what(lim, r)
	msg := fmt.Sprintf("%s chegou ao limite: %s de %s. ", capitalize(what), amount(lim.Unit, used), amount(lim.Unit, eff))
	if perr != nil {
		s.log.Warn("usage: what the limit covers was not paused", "scope", lim.Scope, "key", lim.Key, "err", perr)
		msg += "Não consegui pausar; veja o log. "
	} else {
		msg += "Pausei; quem está no turno termina. "
	}
	req := request{Scope: lim.Scope, Key: lim.Key, Period: period, Unit: lim.Unit, Workspace: r.Workspace, Table: r.Table, Used: used, Cap: eff, Extra: extra, Paused: paused, State: reqOpen, At: s.clk.Now()}
	if s.o.Extras != nil {
		id, err := s.askExtra(ctx, r, lim, req)
		switch {
		case err != nil:
			s.log.Warn("usage: the custo_extra request was not opened", "scope", lim.Scope, "key", lim.Key, "err", err)
			msg += "O pedido de custo extra não abriu."
		default:
			req.ID = id
			if err := s.st.putRequest(ctx, req); err != nil {
				return fmt.Errorf("usage: %w", err)
			}
			msg += fmt.Sprintf("Pedi %s a mais (pedido #%d).", amount(lim.Unit, extra), id)
		}
	}
	s.tell(ctx, r.Workspace, r.Table, strings.TrimSpace(msg))
	return perr
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// askExtra opens the custo_extra request. An extra that takes the limit above
// the cost ceiling is of risk alto without delegation: the Grand Master does
// not decide it, the Administrators and the Owner do.
func (s *Service) askExtra(ctx context.Context, r Record, lim Limit, req request) (int64, error) {
	above := false
	if s.o.Ceilings != nil && r.Workspace != "" && lim.Scope != ScopeDayTotal {
		c, err := s.o.Ceilings.Ceiling(ctx, r.Workspace)
		if err != nil {
			return 0, err
		}
		above = c.PassesCeiling(classes.ExtraSpend{Workspace: r.Workspace, Table: r.Table, Unit: lim.Unit, Scope: lim.Scope.ceilingScope(), Used: req.Used, Limit: req.Cap, Extra: req.Extra})
	}
	risk := classes.ClassifyRisk(classes.Env{}, nil, classes.RiskRequest{Type: classes.RequestExtraCost, AboveCostCeiling: above})
	target, err := json.Marshal(map[string]any{"scope": lim.Scope, "key": lim.Key, "period": req.Period, "unit": lim.Unit, "limit": req.Cap, "extra": req.Extra})
	if err != nil {
		return 0, err
	}
	return s.o.Extras.OpenExtra(ctx, ExtraRequest{Workspace: r.Workspace, Table: r.Table, Scope: lim.Scope, Unit: lim.Unit, Used: req.Used, Limit: req.Cap, Extra: req.Extra,
		About: fmt.Sprintf("custo extra: %s chegou a %s de %s; pede mais %s", s.what(lim, r), amount(lim.Unit, req.Used), amount(lim.Unit, req.Cap), amount(lim.Unit, req.Extra)),
		Risk:  risk, Target: target})
}

// pause pauses what the scope covers. For the total of the day it returns
// the workspaces it paused, to resume them if the extra is approved.
func (s *Service) pause(ctx context.Context, lim Limit, r Record) ([]string, error) {
	reason := "limite de uso"
	switch lim.Scope {
	case ScopeQuest:
		return nil, s.o.Pauser.PauseQuest(ctx, lim.Key, reason)
	case ScopeTable:
		return nil, s.o.Pauser.PauseTable(ctx, r.Workspace, r.Table, reason)
	case ScopeWorkspace, ScopeDay:
		did, err := s.o.Pauser.PauseWorkspace(ctx, lim.Key)
		if did {
			return []string{lim.Key}, err
		}
		return nil, err
	}
	all, err := s.o.Pauser.Workspaces(ctx)
	if err != nil {
		return nil, err
	}
	var paused []string
	var errs []error
	for _, ws := range all {
		did, err := s.o.Pauser.PauseWorkspace(ctx, ws)
		if err != nil {
			errs = append(errs, err)
		}
		if did {
			paused = append(paused, ws)
		}
	}
	return paused, errors.Join(errs...)
}

// tell warns the users of the workspace and the Grand Master.
func (s *Service) tell(ctx context.Context, workspace, table, text string) {
	if s.o.Alerts != nil && workspace != "" {
		if err := s.o.Alerts.Notify(ctx, workspace, text); err != nil {
			s.log.Warn("usage: the users were not told", "workspace", workspace, "err", err)
		}
	}
	if s.o.Notifier != nil && workspace != "" {
		n := characters.Notice{Kind: characters.NoticeSystem, To: characters.Recipient{Kind: characters.ToGrandMaster}, Workspace: workspace, Table: table, Text: text}
		if err := s.o.Notifier.Notify(ctx, n); err != nil {
			s.log.Warn("usage: the Grand Master was not told", "workspace", workspace, "err", err)
		}
	}
}

// Decided is told what became of a custo_extra request the limits opened
// (the owner of the type in the chain of approval). Approved, the limit of
// the period goes up by the extra and the pause is lifted; denied or expired,
// it stays paused and the people are told.
func (s *Service) Decided(ctx context.Context, d Decision) error {
	req, ok, err := s.st.request(ctx, d.Request)
	if err != nil || !ok || req.State != reqOpen {
		return err
	}
	won, err := s.st.settle(ctx, req, d.Approved)
	if err != nil || !won {
		return err
	}
	lim := Limit{Scope: req.Scope, Key: req.Key, Unit: req.Unit}
	what := s.what(lim, Record{Workspace: req.Workspace, Table: req.Table})
	if !d.Approved {
		why := d.Reason
		if why == "" {
			why = "negado ou expirado"
		}
		s.tell(ctx, req.Workspace, req.Table, fmt.Sprintf("O pedido de custo extra de %s não foi aprovado (%s): continua pausado.", what, why))
		return nil
	}
	err = s.resume(ctx, req)
	s.tell(ctx, req.Workspace, req.Table, fmt.Sprintf("Custo extra aprovado para %s: o limite sobe %s e a pausa foi retirada.", what, amount(req.Unit, req.Extra)))
	return err
}

func (s *Service) resume(ctx context.Context, r request) error {
	// another limit still at 100% on the same thing keeps it paused
	others, err := s.st.openRequests(ctx)
	if err != nil {
		return err
	}
	for _, o := range others {
		if o.ID != r.ID && o.target() == r.target() {
			return nil
		}
	}
	switch r.Scope {
	case ScopeQuest:
		return s.o.Pauser.ResumeQuest(ctx, r.Key)
	case ScopeTable:
		return s.o.Pauser.ResumeTable(ctx, r.Workspace, r.Table)
	}
	var errs []error
	for _, ws := range r.Paused {
		errs = append(errs, s.o.Pauser.ResumeWorkspace(ctx, ws))
	}
	return errors.Join(errs...)
}

// SetLimit saves the limit of a scope. An Administrator or above sets the
// limits of a workspace; the total of the day, only the Owner. A limit above
// the cost ceiling of the workspace is refused when saved.
func (s *Service) SetLimit(ctx context.Context, by classes.Level, workspace string, l Limit) error {
	switch {
	case !l.Scope.Valid():
		return fmt.Errorf("%w: unknown scope %q", ErrInvalid, l.Scope)
	case l.Unit != classes.UnitUSD && l.Unit != classes.UnitTokens && l.Unit != classes.UnitHours:
		return fmt.Errorf("%w: unknown unit %q", ErrInvalid, l.Unit)
	case math.IsNaN(l.Value) || math.IsInf(l.Value, 0) || l.Value <= 0:
		return fmt.Errorf("%w: %v is not a finite positive number", ErrInvalid, l.Value)
	case l.Unit == classes.UnitTokens && l.Value != math.Trunc(l.Value):
		return fmt.Errorf("%w: %v is not a whole number of tokens", ErrInvalid, l.Value)
	case l.Scope == ScopeDayTotal && l.Key != "":
		return fmt.Errorf("%w: the total of the day has no key", ErrInvalid)
	case l.Scope != ScopeDayTotal && l.Key == "":
		return fmt.Errorf("%w: the limit of a %s needs its key", ErrInvalid, l.Scope)
	}
	if l.Scope == ScopeDayTotal {
		if by != classes.LevelOwner {
			return fmt.Errorf("%w: only the Owner sets the total of the day", ErrForbidden)
		}
	} else if by != classes.LevelOwner && by != classes.LevelAdmin {
		return fmt.Errorf("%w: an Administrator or above sets the limits", ErrForbidden)
	}
	if s.o.Ceilings != nil && l.Scope != ScopeDayTotal {
		if workspace == "" {
			return fmt.Errorf("%w: the workspace of the limit", ErrInvalid)
		}
		c, err := s.o.Ceilings.Ceiling(ctx, workspace)
		if err != nil {
			return fmt.Errorf("usage: the ceiling of %s: %w", workspace, err)
		}
		if err := c.CheckLimit(l.Unit, l.Scope.ceilingScope(), l.Value); err != nil {
			return err
		}
	}
	if l.Scope == ScopeWorkspace || l.Scope == ScopeDay {
		if l.Key != workspace {
			return fmt.Errorf("%w: the key %q is not the workspace %q", ErrInvalid, l.Key, workspace)
		}
	} else if l.Scope == ScopeTable && !strings.HasPrefix(l.Key, workspace+"/") {
		return fmt.Errorf("%w: the table %q is not in the workspace %q", ErrInvalid, l.Key, workspace)
	}
	return s.st.PutLimit(ctx, l)
}

// SetThreshold sets the start threshold of a workspace, in percent.
func (s *Service) SetThreshold(ctx context.Context, by classes.Level, workspace string, pct float64) error {
	switch {
	case by != classes.LevelOwner && by != classes.LevelAdmin:
		return fmt.Errorf("%w: an Administrator or above sets the threshold", ErrForbidden)
	case workspace == "":
		return fmt.Errorf("%w: the workspace", ErrInvalid)
	case math.IsNaN(pct) || pct <= 0 || pct > 100:
		return fmt.Errorf("%w: %v is not a percentage", ErrInvalid, pct)
	}
	return s.st.putThreshold(ctx, workspace, pct)
}

// Consumption is the use of a scope: today for the scopes per day.
func (s *Service) Consumption(ctx context.Context, scope Scope, key string) (Totals, error) {
	period := ""
	if scope.perDay() {
		period = s.Today(ctx)
	}
	return s.st.Sum(ctx, scope, key, period)
}

// OfCharacter is the use of a character.
func (s *Service) OfCharacter(ctx context.Context, id string) (Totals, error) {
	return s.st.SumOf(ctx, id)
}

// OfAccount is the use of an account profile of a provider today.
func (s *Service) OfAccount(ctx context.Context, provider, profile string) (Totals, error) {
	return s.st.SumAccount(ctx, provider, profile, s.Today(ctx))
}

// View is a limit with how much is used and left, for the panel; HasData
// false is the gap to show next to a limit in a unit the provider gave
// nothing for.
func (s *Service) View(ctx context.Context, l Limit, workspace string) (LimitView, error) {
	period := ""
	if l.Scope.perDay() {
		period = s.Today(ctx)
	}
	eff, err := s.effective(ctx, l, workspace, period)
	if err != nil {
		return LimitView{}, err
	}
	tot, err := s.st.Sum(ctx, l.Scope, l.Key, period)
	if err != nil {
		return LimitView{}, err
	}
	v := LimitView{Limit: l, Period: period, Effective: eff, Used: tot.Used(l.Unit), HasData: tot.Has(l.Unit)}
	if eff > 0 {
		v.Percent = v.Used / eff * 100
	}
	return v, nil
}

// Limits lists the limits that are set.
func (s *Service) Limits(ctx context.Context) ([]Limit, error) { return s.st.Limits(ctx) }
