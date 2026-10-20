package workspaces

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/safefile"
)

// mestreDir is the folder of the Mestre in the project. When the users
// version it, the engine's own files in it must not hold the main clone back
// (docs/sessao-zero/workspace.md#estrutura-de-mestre).
const mestreDir = ".mestre/"

// maxMestreFile bounds what is read of a file of .mestre/ to compare it.
const maxMestreFile = 4 << 20

// entry is what the scheduler keeps in memory about a workspace; what must
// survive a restart (the outage, the alerts) is in the database.
type entry struct {
	next         time.Time // the next fetch
	failures     int       // failed fetches in a row, for the backoff
	alertRetry   time.Time // an alert that failed is not sent again before this
	movedAlerted bool      // the alert of a main clone that moved was sent
}

// entryLocked returns the entry of a workspace, made on first sight. The
// workspaces this process finds when it starts are fetched at once, whatever
// the last fetch was (the new Citadel, or the restarted one, fetches before
// anything else); one that appears later, fetched less than FetchInterval
// ago, waits for the rest of it.
func (s *Service) entryLocked(w Workspace, now time.Time) *entry {
	e, ok := s.sched[w.Slug]
	if !ok {
		e = &entry{next: now}
		if !w.Fetch.LastAt.IsZero() && s.booted {
			e.next = w.Fetch.LastAt.Add(s.o.FetchInterval)
		}
		s.sched[w.Slug] = e
	}
	return e
}

// Poke asks for a fetch of the mirror as soon as the loop can: the poll of
// the GitHub events saw a push to the main branch. It never blocks.
func (s *Service) Poke(slug string) {
	s.mu.Lock()
	if e, ok := s.sched[slug]; ok {
		e.next = s.o.Clock.Now()
	} else {
		s.sched[slug] = &entry{next: s.o.Clock.Now()}
	}
	s.mu.Unlock()
	s.signal()
}

// Fetch fetches the mirror now and, if it worked, advances the main clone:
// before opening a table and before sending the branch of a table. The
// failure is returned (and counted for the backoff and the alert).
func (s *Service) Fetch(ctx context.Context, slug string) error {
	w, err := s.store.Get(ctx, slug)
	if err != nil {
		return err
	}
	if !w.MirrorReady() {
		return fmt.Errorf("workspace %s: the mirror does not exist yet (stage %s)", slug, w.Stage)
	}
	err = s.fetchOne(ctx, w)
	s.alerts(ctx, slug)
	return err
}

// OnFetched sets what is told, after a fetch of the mirror that worked, which
// workspace it was (the base of the tables may have moved: basesync). It is
// called outside the lock of the fetch; nil: nobody is told.
func (s *Service) OnFetched(f func(ctx context.Context, slug string)) {
	s.mu.Lock()
	s.afterFetch = f
	s.mu.Unlock()
}

// fetchOne is one fetch of the mirror, the advance of the main clone after it,
// and then the tell of OnFetched.
func (s *Service) fetchOne(ctx context.Context, w Workspace) error {
	err := s.fetchLocked(ctx, w)
	if err != nil {
		return err
	}
	s.mu.Lock()
	f := s.afterFetch
	s.mu.Unlock()
	if f != nil {
		f(ctx, w.Slug)
	}
	return nil
}

func (s *Service) fetchLocked(ctx context.Context, w Workspace) error {
	defer s.lock("fetch/" + w.Slug)()
	if s.o.Gate != nil {
		if err := s.o.Gate(); err != nil {
			return fmt.Errorf("fetch %s: %w", w.Slug, err)
		}
	}
	w, err := s.store.Get(ctx, w.Slug)
	if err != nil {
		return err
	}
	repo, err := s.o.Git.Open(ctx, s.layout.Mirror(w.Slug))
	if err == nil {
		err = s.fetch(ctx, repo, w)
	}
	if ctx.Err() != nil {
		return ctx.Err() // shutting down is not a failure of the mirror
	}
	now := s.o.Clock.Now()
	if err != nil {
		return s.fetchFailed(ctx, w, now, err)
	}
	s.mu.Lock()
	e := s.entryLocked(w, now)
	e.failures, e.next = 0, now.Add(s.o.FetchInterval)
	s.mu.Unlock()
	if err := s.store.FetchOK(ctx, w.Slug, now); err != nil {
		return err
	}
	if !w.Fetch.FailingSince.IsZero() {
		s.log.Info("workspace mirror fetch recovered", "workspace", w.Slug, "since", w.Fetch.FailingSince)
	}
	s.advance(ctx, w, now)
	return nil
}

func (s *Service) fetchFailed(ctx context.Context, w Workspace, now time.Time, cause error) error {
	since := w.Fetch.FailingSince
	if since.IsZero() {
		since = now
	}
	s.mu.Lock()
	e := s.entryLocked(w, now)
	e.failures++
	e.next = now.Add(backoff(e.failures))
	n := e.failures
	s.mu.Unlock()
	if err := s.store.FetchFailed(ctx, w.Slug, now, since, reasonOf(cause)); err != nil {
		return errors.Join(cause, err)
	}
	s.log.Warn("workspace mirror fetch failed", "workspace", w.Slug, "failures", n, "err", cause)
	return fmt.Errorf("fetch %s: %w", w.Slug, cause)
}

// advance moves the main clone to origin/<branch> with a fast-forward, when
// that is safe: the Grand Master is not working (idle or waiting) and the
// clone has no local changes. Otherwise it stays as it is until the next
// fetch, and after AdvanceAlertAfter the Grand Master is told why.
func (s *Service) advance(ctx context.Context, w Workspace, now time.Time) {
	hold := func(format string, args ...any) {
		reason := fmt.Sprintf(format, args...)
		since := w.Advance.BlockedSince
		if since.IsZero() {
			since = now
		}
		if w.Advance.Reason == reason && !w.Advance.BlockedSince.IsZero() {
			return
		}
		if err := s.store.AdvanceBlocked(ctx, w.Slug, now, since, reason); err != nil {
			s.log.Error("workspace advance: record", "workspace", w.Slug, "err", err)
		}
	}
	rec := s.layout.record(w.Slug)
	wt, err := s.o.Git.OpenWorktree(ctx, rec)
	if err != nil {
		if errors.Is(err, gitx.ErrWorktreeMoved) {
			s.moved(ctx, w, now, err)
			hold("o clone principal não aponta mais para o espelho registrado (%v)", err)
			return
		}
		hold("não foi possível abrir o clone principal: %v", err)
		return
	}
	target := "refs/remotes/origin/" + w.DefaultBranch
	tip, err := wt.ResolveRef(ctx, target)
	if err != nil {
		hold("o espelho não tem origin/%s: %v", w.DefaultBranch, err)
		return
	}
	if upToDate, err := wt.IsAncestor(ctx, string(tip), "HEAD"); err != nil {
		hold("não foi possível comparar o clone principal com origin/%s: %v", w.DefaultBranch, err)
		return
	} else if upToDate {
		s.caughtUp(ctx, w, now)
		return
	}
	state, err := s.o.GrandMaster.State(ctx, w.Slug)
	if err != nil {
		hold("não foi possível ler o estado do Grão-Mestre: %v", err)
		return
	}
	if !state.still() {
		hold("o Grão-Mestre está trabalhando (%s)", state)
		return
	}
	changes, err := wt.Status(ctx)
	if err != nil {
		hold("não foi possível ler o estado do clone principal: %v", err)
		return
	}
	// The files of .mestre/ are the engine's: they do not make the clone
	// dirty. Those the project now versions, with the very content the engine
	// made, are removed so that the fast-forward can bring them (git refuses
	// to overwrite an untracked file, identical or not); a different one
	// stays and holds the merge back.
	var dirty int
	for _, c := range changes {
		switch {
		case !strings.HasPrefix(c.Path, mestreDir):
			dirty++
		case c.Untracked():
			s.dropIdentical(ctx, wt, rec.Dir, target, c.Path)
		}
	}
	if dirty > 0 {
		hold("o clone principal tem %d arquivo(s) com alterações locais", dirty)
		return
	}
	// MergeFFOnly moves whatever branch is checked out: only the main branch
	// is advanced to origin's.
	if head, err := wt.HeadBranch(ctx); err != nil {
		hold("não foi possível ler a branch do clone principal: %v", err)
		return
	} else if head != w.DefaultBranch {
		if head == "" {
			head = "HEAD solto"
		}
		hold("o clone principal não está na branch %s (está em %s)", w.DefaultBranch, head)
		return
	}
	switch _, err := wt.MergeFFOnly(ctx, target); {
	case err == nil:
		s.log.Info("workspace main clone advanced", "workspace", w.Slug, "to", tip.Short())
		s.caughtUp(ctx, w, now)
	case errors.Is(err, gitx.ErrNotFastForward):
		hold("o clone principal tem commits que origin/%s não tem: não é um avanço simples", w.DefaultBranch)
	case errors.Is(err, gitx.ErrLocalChanges):
		hold("alterações locais do clone principal (inclusive em .mestre/) seriam sobrescritas")
	default:
		hold("o avanço do clone principal falhou: %v", err)
	}
}

// dropIdentical removes the untracked file rel of the main clone when the
// commit the clone is advancing to has the file with the same content. A file
// the commit does not have, or has with other content, stays. The clone is a
// folder the agents write, so the file is read and removed through safefile,
// and only if it is still the one that was read.
func (s *Service) dropIdentical(ctx context.Context, wt gitx.Repo, dir, target, rel string) {
	want, err := wt.ReadFile(ctx, target, rel)
	if err != nil {
		return // not in the commit (or unreadable): nothing to clear
	}
	file := filepath.Join(dir, filepath.FromSlash(rel))
	have, info, err := safefile.Read(file, maxMestreFile)
	if err != nil || string(have) != string(want) {
		return
	}
	same := func(now safefile.Info) error {
		if !now.Same(info) {
			return errors.New("the file changed")
		}
		return nil
	}
	if err := safefile.Remove(file, same); err != nil {
		s.log.Warn("workspace advance: remove the copy of a versioned file", "path", rel, "err", err)
	}
}

// caughtUp records that the main clone is where origin is.
func (s *Service) caughtUp(ctx context.Context, w Workspace, now time.Time) {
	if w.Advance.BlockedSince.IsZero() && w.Advance.Reason == "" {
		return
	}
	if err := s.store.AdvanceClear(ctx, w.Slug, now); err != nil {
		s.log.Error("workspace advance: record", "workspace", w.Slug, "err", err)
	}
}

// moved raises the alert of a main clone that no longer points at its
// record, once for the process: an agent rewrote its .git file or commondir.
func (s *Service) moved(ctx context.Context, w Workspace, now time.Time, cause error) {
	s.mu.Lock()
	e := s.entryLocked(w, now)
	done := e.movedAlerted
	s.mu.Unlock()
	if done {
		return
	}
	err := s.o.Alerts.Operational(ctx, Alert{Workspace: w.Slug, Kind: AlertWorktreeMoved, Since: now,
		Detail: fmt.Sprintf("O clone principal do workspace %s não aponta mais para o espelho do Mestre; o motor não o usa até alguém conferir. %v", w.Slug, cause)})
	if err != nil {
		s.log.Error("workspace alert", "workspace", w.Slug, "kind", AlertWorktreeMoved, "err", err)
		return
	}
	s.mu.Lock()
	e.movedAlerted = true
	s.mu.Unlock()
}

// alerts sends what is due for a workspace: the operational alert of a mirror
// that has not been fetched for AlertAfter, and the item system for the Grand
// Master when the advance of the main clone has been held back for
// AdvanceAlertAfter, each once per occurrence. One that fails is sent again
// after BackoffFirst.
func (s *Service) alerts(ctx context.Context, slug string) {
	w, err := s.store.Get(ctx, slug)
	if err != nil {
		return
	}
	now := s.o.Clock.Now()
	if due, _ := s.fetchAlertDue(w, now); due {
		err := s.o.Alerts.Operational(ctx, Alert{Workspace: w.Slug, Kind: AlertFetchFailing, Since: w.Fetch.FailingSince,
			Detail: fmt.Sprintf("O espelho do workspace %s não consegue buscar o GitHub desde %s: %s. As mesas seguem trabalhando localmente.",
				w.Slug, w.Fetch.FailingSince.Format(time.RFC3339), w.Fetch.Error)})
		if err == nil {
			err = s.store.FetchAlerted(ctx, w.Slug, now)
		}
		s.alertResult(w, now, AlertFetchFailing, err)
	}
	if due, _ := s.advanceAlertDue(w, now); due {
		err := s.o.GrandMaster.NotifySystem(ctx, w.Slug, fmt.Sprintf(
			"O clone principal do workspace %s não avança para origin/%s desde %s: %s.",
			w.Slug, w.DefaultBranch, w.Advance.BlockedSince.Format(time.RFC3339), w.Advance.Reason))
		if err == nil {
			err = s.store.AdvanceAlerted(ctx, w.Slug, now)
		}
		s.alertResult(w, now, "advance_held", err)
	}
}

func (s *Service) alertResult(w Workspace, now time.Time, kind AlertKind, err error) {
	if err == nil {
		return
	}
	s.log.Error("workspace alert", "workspace", w.Slug, "kind", kind, "err", err)
	s.mu.Lock()
	s.entryLocked(w, now).alertRetry = now.Add(BackoffFirst)
	s.mu.Unlock()
}

// fetchAlertDue says whether the alert of the fetch outage is due now, and
// when it is due (zero when there is none to send).
func (s *Service) fetchAlertDue(w Workspace, now time.Time) (bool, time.Time) {
	if w.Fetch.FailingSince.IsZero() || w.Fetch.Alerted {
		return false, time.Time{}
	}
	at := s.notBeforeRetry(w, w.Fetch.FailingSince.Add(s.o.AlertAfter), now)
	return !now.Before(at), at
}

func (s *Service) advanceAlertDue(w Workspace, now time.Time) (bool, time.Time) {
	if w.Advance.BlockedSince.IsZero() || w.Advance.Alerted {
		return false, time.Time{}
	}
	at := s.notBeforeRetry(w, w.Advance.BlockedSince.Add(s.o.AdvanceAlertAfter), now)
	return !now.Before(at), at
}

func (s *Service) notBeforeRetry(w Workspace, at, now time.Time) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.entryLocked(w, now).alertRetry; r.After(at) {
		return r
	}
	return at
}

// Tick does what is due now (stages that can run, fetches whose time came,
// alerts) and returns when the next thing is due, if any. Run calls it; the
// tests call it after moving the clock.
func (s *Service) Tick(ctx context.Context) (next time.Time, ok bool, err error) {
	now := s.o.Clock.Now()
	if s.o.Gate != nil {
		if gerr := s.o.Gate(); gerr != nil {
			return now.Add(BackoffFirst), true, nil // the Citadel does not act now: look again soon
		}
	}
	list, err := s.store.List(ctx)
	if err != nil {
		return now.Add(BackoffFirst), true, err
	}
	var errs []error
	note := func(at time.Time) {
		if !ok || at.Before(next) {
			next, ok = at, true
		}
	}
	for _, w := range list {
		if ctx.Err() != nil {
			return next, ok, ctx.Err()
		}
		if w.State == StatePreparing {
			var perr error
			if w, perr = s.driveStage(ctx, w, now); perr != nil {
				errs = append(errs, perr)
			}
			if w.StageFailure != nil && w.State == StatePreparing && s.hasRunner(w.Stage) {
				note(w.StageFailure.RetryAt)
			}
			if at, ok := s.waitUntil(w); ok {
				note(at)
			}
		}
		if !w.MirrorReady() {
			continue
		}
		s.mu.Lock()
		due := !now.Before(s.entryLocked(w, now).next)
		s.mu.Unlock()
		if due {
			if ferr := s.fetchOne(ctx, w); ferr != nil && ctx.Err() == nil {
				s.log.Debug("workspace tick: fetch", "workspace", w.Slug, "err", ferr)
			}
		}
		s.alerts(ctx, w.Slug)
		if w, err = s.store.Get(ctx, w.Slug); err != nil {
			errs = append(errs, err)
			continue
		}
		s.mu.Lock()
		note(s.entryLocked(w, now).next)
		s.mu.Unlock()
		if d, at := s.fetchAlertDue(w, now); d || !at.IsZero() {
			note(at)
		}
		if d, at := s.advanceAlertDue(w, now); d || !at.IsZero() {
			note(at)
		}
	}
	s.mu.Lock()
	s.booted = true
	s.mu.Unlock()
	if len(errs) > 0 {
		note(now.Add(BackoffFirst))
	}
	return next, ok, errors.Join(errs...)
}

// waitUntil is when a waiting stage of the workspace runs again.
func (s *Service) waitUntil(w Workspace) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wt, ok := s.waits[w.Slug]
	return wt.until, ok && w.State == StatePreparing && wt.stage == w.Stage
}

func (s *Service) hasRunner(stage Stage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stages[stage] != nil
}

// driveStage runs the stages of a preparing workspace when they can run: a
// stage with a runner that did not fail, or failed and whose backoff is over.
func (s *Service) driveStage(ctx context.Context, w Workspace, now time.Time) (Workspace, error) {
	if !s.hasRunner(w.Stage) || (w.StageFailure != nil && now.Before(w.StageFailure.RetryAt)) {
		return w, nil
	}
	if at, ok := s.waitUntil(w); ok && now.Before(at) {
		return w, nil
	}
	err := s.Prepare(ctx, w.Slug)
	cur, gerr := s.store.Get(ctx, w.Slug)
	if gerr != nil {
		return w, gerr
	}
	if errors.Is(err, ErrNoRunner) || errors.Is(err, ErrWaiting) || (cur.StageFailure != nil && err != nil) {
		err = nil // the workspace waits, or the failure is recorded with its retry time
	}
	return cur, err
}

// Run keeps the workspaces going until ctx ends: it does what is due and
// sleeps, on the clock, until the next thing is due or someone asks for a
// fetch (Poke) or creates a workspace.
func (s *Service) Run(ctx context.Context) error {
	for {
		next, ok, err := s.Tick(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			s.log.Error("workspaces tick", "err", err)
		}
		wait := s.o.FetchInterval // nothing scheduled: look again after a while
		if ok {
			wait = max(next.Sub(s.o.Clock.Now()), 0)
		}
		t := s.o.Clock.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C():
		case <-s.wake:
			t.Stop()
		}
	}
}
