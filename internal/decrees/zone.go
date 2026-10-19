package decrees

import (
	"context"
	"time"
)

// guildZone is the time zone of the Guild (guild.timezone); nil when it is not
// kept or not valid, and then the moments are shown in UTC.
func (s *Service) guildZone(ctx context.Context) *time.Location {
	var name string
	if err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT timezone FROM guild WHERE id = 1`).Scan(&name); err != nil || name == "" || name == "Local" {
		return nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil
	}
	return loc
}

// Moment is how a user sees the instant a decree ends: in the time zone of the
// Guild with its name and offset ("25/10/2026 23:59 (America/Sao_Paulo,
// UTC-03:00)"), or in UTC, saying so, when that zone is not available. It is
// only the text; the decree ends at the absolute instant, to the second.
func (s *Service) Moment(ctx context.Context, t time.Time) string {
	return momentIn(t, s.guildZone(ctx))
}

func momentIn(t time.Time, loc *time.Location) string {
	layout := "02/01/2006 15:04"
	if t.Second() != 0 || t.Nanosecond() != 0 {
		layout = "02/01/2006 15:04:05"
	}
	if loc == nil || loc.String() == "UTC" {
		return t.UTC().Format(layout) + " UTC"
	}
	l := t.In(loc)
	return l.Format(layout) + " (" + loc.String() + ", UTC" + l.Format("-07:00") + ")"
}
