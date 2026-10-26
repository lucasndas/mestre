package rituals_test

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/rituals"
)

func TestTheScheduleFollowsTheDocument(t *testing.T) {
	t.Parallel()
	now := start
	utc := time.UTC
	for name, sp := range map[string]rituals.Spec{
		"daily and every together": {Daily: "09:00", Every: "30m"},
		"none":                     {},
		"every under the minimum":  {Every: "4m"},
		"every not a duration":     {Every: "trinta"},
		"daily out of range":       {Daily: "25:00"},
		"daily without minutes":    {Daily: "9"},
		"weekly without a day":     {Weekly: "09:00"},
		"weekly with a bad day":    {Weekly: "monday 09:00"},
		"once in the past":         {Once: "2026-10-14T14:00"},
		"once not a date":          {Once: "amanhã"},
	} {
		if _, err := rituals.ParseSchedule(sp, utc, now); !errors.Is(err, rituals.ErrInvalidSchedule) {
			t.Errorf("%s: err = %v; want an invalid schedule", name, err)
		}
	}
	for name, c := range map[string]struct {
		sp   rituals.Spec
		want string
	}{
		"every":  {rituals.Spec{Every: "30m"}, "every 30m0s"},
		"daily":  {rituals.Spec{Daily: "02:00"}, "daily 02:00"},
		"weekly": {rituals.Spec{Weekly: "mon,thu 09:00"}, "weekly mon,thu 09:00"},
		"once":   {rituals.Spec{Once: "2026-10-20T14:00"}, "once 2026-10-20T14:00Z"},
	} {
		s, err := rituals.ParseSchedule(c.sp, utc, now)
		if err != nil || s.String() != c.want {
			t.Errorf("%s: %q, %v; want %q", name, s.String(), err, c.want)
		}
	}
	// The zone of the workspace decides the hour: 02:00 in São Paulo is 05:00 UTC.
	sp, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skip("no tzdata")
	}
	s, _ := rituals.ParseSchedule(rituals.Spec{Daily: "02:00"}, sp, now)
	if got := s.Next(now, sp); !got.Equal(time.Date(2026, 10, 16, 5, 0, 0, 0, time.UTC)) {
		t.Errorf("02:00 in São Paulo after %s = %s; want 05:00 UTC the next day", now, got)
	}
	w, _ := rituals.ParseSchedule(rituals.Spec{Weekly: "mon,thu 09:00"}, utc, now)
	if got := w.Next(now, utc); !got.Equal(time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)) { // Thursday 09:00 has passed: Monday is next
		t.Errorf("weekly next = %s", got)
	}
}

// The decision is the one the target's own hook takes: the effective rules of the
// character, here with a command that is allowed, one that is denied and one
// that would ask.
func TestThePolicyOfTheTargetDecidesThePreRun(t *testing.T) {
	t.Parallel()
	layout := classes.DefaultLayout(runtime.GOOS, `C:\ProgramData`, "principal")
	wt := filepath.Join(layout.Workspaces, "loja", "mesas", "42-login")
	p, err := classes.ParsePermissions([]byte("version: 1\ncommands:\n  allow: [\"npm test\"]\n  deny: [\"curl *\"]\n  default: ask\n"))
	if err != nil {
		t.Fatal(err)
	}
	rules, err := classes.Build(classes.Layers{AgentID: "c1", Class: "dev", Permissions: p, Now: start})
	if err != nil {
		t.Fatal(err)
	}
	pol := &classes.Policy{Source: rulesOf{"c1": rules}, Env: func(_ context.Context, id string) (classes.Env, error) {
		return classes.Env{AgentID: id, TableID: "42-login", Worktree: wt, Layout: layout}, nil
	}}
	perm := rituals.PolicyPermissions{Policy: pol}
	c := characters.Character{ID: "c1", Name: "Dev", Table: "42-login", Dir: wt}
	for cmd, want := range map[string]bool{"npm test": true, "curl evil.example": false, "docker ps": false, "git push": false} {
		d, err := perm.Allow(t.Context(), c, cmd)
		if err != nil || d.Allow != want || (!want && d.Reason == "") {
			t.Errorf("%q: %+v, %v; want allow=%v with a reason when refused", cmd, d, err, want)
		}
	}
	// A character without rules is denied: the decision fails closed.
	if d, _ := perm.Allow(t.Context(), characters.Character{ID: "c2", Table: "42-login", Dir: wt}, "npm test"); d.Allow {
		t.Error("a character without rules was allowed")
	}
	if d, _ := (rituals.PolicyPermissions{}).Allow(t.Context(), c, "npm test"); d.Allow {
		t.Error("no policy allowed a command")
	}
}

type rulesOf map[string]*classes.Rules

func (r rulesOf) Rules(_ context.Context, id string) (*classes.Rules, error) {
	if x, ok := r[id]; ok {
		return x, nil
	}
	return nil, errors.New("no rules")
}
