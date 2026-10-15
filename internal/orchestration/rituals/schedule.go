package rituals

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ScheduleKind is how a ritual is scheduled.
type ScheduleKind string

// The kinds of schedule.
const (
	// Every: --every <duration>, from the creation of the ritual.
	Every ScheduleKind = "every"
	// Daily: --daily HH:MM.
	Daily ScheduleKind = "daily"
	// Weekly: --weekly "<days> HH:MM".
	Weekly ScheduleKind = "weekly"
	// Once: --once <date and time>.
	Once ScheduleKind = "once"
)

// MinEvery is the shortest --every (the default of the document is the
// minimum).
const MinEvery = 5 * time.Minute

// MaxLateness caps how late a missed slot of a recurring ritual may still fire.
const MaxLateness = time.Hour

// maxSlots is how many missed slots a look keeps; older ones are forgotten
// (the history of a ritual that was down for months is not worth a row each).
const maxSlots = 200

// ErrInvalidSchedule is a schedule that does not follow the document.
var ErrInvalidSchedule = errors.New("invalid schedule")

// Schedule says when a ritual fires, in the time zone of the workspace.
type Schedule struct {
	Kind  ScheduleKind  `json:"kind"`
	Every time.Duration `json:"every,omitempty"`
	// Hour and Minute are the time of day of a daily or weekly ritual.
	Hour   int            `json:"hour,omitempty"`
	Minute int            `json:"minute,omitempty"`
	Days   []time.Weekday `json:"days,omitempty"`
	// At is the instant of a --once.
	At time.Time `json:"at,omitzero"`
}

// Spec is the schedule as the CLI gives it: exactly one field is set.
type Spec struct {
	Every, Daily, Weekly, Once string
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSchedule, fmt.Sprintf(format, a...))
}

// ParseSchedule reads the four flags. loc is the time zone of the workspace.
func ParseSchedule(sp Spec, loc *time.Location, now time.Time) (Schedule, error) {
	set := 0
	for _, v := range []string{sp.Every, sp.Daily, sp.Weekly, sp.Once} {
		if strings.TrimSpace(v) != "" {
			set++
		}
	}
	if set != 1 {
		return Schedule{}, invalid("use exactly one of --every, --daily, --weekly and --once")
	}
	var s Schedule
	switch {
	case sp.Every != "":
		d, err := time.ParseDuration(strings.TrimSpace(sp.Every))
		if err != nil {
			return Schedule{}, invalid("--every %q is not a duration (30m, 2h)", sp.Every)
		}
		s = Schedule{Kind: Every, Every: d}
	case sp.Daily != "":
		h, m, err := parseClock(sp.Daily)
		if err != nil {
			return Schedule{}, err
		}
		s = Schedule{Kind: Daily, Hour: h, Minute: m}
	case sp.Weekly != "":
		f := strings.Fields(sp.Weekly)
		if len(f) != 2 {
			return Schedule{}, invalid(`--weekly is "<days> HH:MM", for example "mon,thu 09:00"`)
		}
		h, m, err := parseClock(f[1])
		if err != nil {
			return Schedule{}, err
		}
		days, err := parseDays(f[0])
		if err != nil {
			return Schedule{}, err
		}
		s = Schedule{Kind: Weekly, Hour: h, Minute: m, Days: days}
	default:
		at, err := parseOnce(sp.Once, loc)
		if err != nil {
			return Schedule{}, err
		}
		s = Schedule{Kind: Once, At: at}
	}
	return s, s.Validate(now)
}

func parseClock(v string) (h, m int, err error) {
	hs, ms, ok := strings.Cut(strings.TrimSpace(v), ":")
	h, e1 := strconv.Atoi(hs)
	m, e2 := strconv.Atoi(ms)
	if !ok || e1 != nil || e2 != nil || len(ms) != 2 || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, invalid("%q is not a time of day (HH:MM)", v)
	}
	return h, m, nil
}

var dayNames = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

func parseDays(v string) ([]time.Weekday, error) {
	var out []time.Weekday
	for _, d := range strings.Split(v, ",") {
		w, ok := dayNames[strings.ToLower(strings.TrimSpace(d))]
		if !ok {
			return nil, invalid("%q is not a day (mon, tue, wed, thu, fri, sat, sun)", d)
		}
		if !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out, nil
}

func parseOnce(v string, loc *time.Location) (time.Time, error) {
	v = strings.TrimSpace(v)
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, v, loc); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, invalid("--once %q is not a date and time (2026-10-20T14:00)", v)
}

// Validate checks a schedule: the minimum of --every, the time of day, the
// days, and a --once that is not in the past.
func (s Schedule) Validate(now time.Time) error {
	switch s.Kind {
	case Every:
		if s.Every < MinEvery {
			return invalid("--every must be at least %s", MinEvery)
		}
	case Daily, Weekly:
		if s.Hour < 0 || s.Hour > 23 || s.Minute < 0 || s.Minute > 59 {
			return invalid("the time of day is out of range")
		}
		if s.Kind == Weekly && len(s.Days) == 0 {
			return invalid("--weekly needs at least one day")
		}
	case Once:
		if s.At.IsZero() || !s.At.After(now) {
			return invalid("--once is in the past")
		}
	default:
		return invalid("unknown kind %q", s.Kind)
	}
	return nil
}

// Interval is the time between two slots: the one that bounds the timeout in
// the queue and the lateness a missed slot may have. A --once has none (0).
func (s Schedule) Interval() time.Duration {
	switch s.Kind {
	case Every:
		return s.Every
	case Daily:
		return 24 * time.Hour
	case Weekly:
		return 7 * 24 * time.Hour
	}
	return 0
}

// MaxLate is how late a missed slot may be and still fire: less than half the
// interval, at most one hour.
func (s Schedule) MaxLate() time.Duration {
	return min(s.Interval()/2, MaxLateness)
}

// String writes the schedule the way the CLI takes it.
func (s Schedule) String() string {
	switch s.Kind {
	case Every:
		return "every " + s.Every.String()
	case Daily:
		return fmt.Sprintf("daily %02d:%02d", s.Hour, s.Minute)
	case Weekly:
		days := make([]string, len(s.Days))
		for i, d := range s.Days {
			days[i] = strings.ToLower(d.String()[:3])
		}
		return fmt.Sprintf("weekly %s %02d:%02d", strings.Join(days, ","), s.Hour, s.Minute)
	case Once:
		return "once " + s.At.UTC().Format("2006-01-02T15:04Z")
	}
	return string(s.Kind)
}

// Next is the first slot after the instant, in the zone; zero when there is
// none (a --once that has passed).
func (s Schedule) Next(after time.Time, loc *time.Location) time.Time {
	switch s.Kind {
	case Every:
		return after.Add(s.Every)
	case Once:
		if s.At.After(after) {
			return s.At
		}
		return time.Time{}
	case Daily, Weekly:
		t := after.In(loc)
		for i := 0; i <= 8; i++ {
			c := time.Date(t.Year(), t.Month(), t.Day()+i, s.Hour, s.Minute, 0, 0, loc)
			if !c.After(after) {
				continue
			}
			if s.Kind == Weekly && !slices.Contains(s.Days, c.Weekday()) {
				continue
			}
			return c.UTC()
		}
	}
	return time.Time{}
}

// Slots lists the slots from `from` (included) up to `upto` (included), the
// oldest first, and the first one after them. At most the latest maxSlots are
// kept.
func (s Schedule) Slots(from, upto time.Time, loc *time.Location) (slots []time.Time, next time.Time) {
	if from.IsZero() || from.After(upto) {
		return nil, from
	}
	switch s.Kind {
	case Every:
		n := int64(upto.Sub(from)/s.Every) + 1
		first := max(int64(0), n-maxSlots)
		for i := first; i < n; i++ {
			slots = append(slots, from.Add(time.Duration(i)*s.Every))
		}
		return slots, from.Add(time.Duration(n) * s.Every)
	case Once:
		return []time.Time{from}, time.Time{}
	}
	slot := from
	for i := 0; !slot.After(upto) && !slot.IsZero() && i < 100000; i++ {
		slots = append(slots, slot)
		slot = s.Next(slot, loc)
	}
	if len(slots) > maxSlots {
		slots = slots[len(slots)-maxSlots:]
	}
	return slots, slot
}
