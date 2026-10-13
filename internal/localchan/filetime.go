package localchan

import (
	"fmt"
	"strconv"
	"time"
)

// filetimeToUnix is the distance between 1601-01-01, the origin of a
// FILETIME, and 1970-01-01, in 100 ns ticks.
const filetimeToUnix = 116444736000000000

// FiletimeToTime converts the start identity of a Windows process, the
// decimal FILETIME of GetProcessTimes (terminal.System.Identify), to a
// time.
func FiletimeToTime(start string) (time.Time, error) {
	ticks, err := strconv.ParseUint(start, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("creation time %q: %w", start, err)
	}
	if ticks < filetimeToUnix {
		return time.Time{}, fmt.Errorf("creation time %q is before 1970", start)
	}
	unix := ticks - filetimeToUnix
	return time.Unix(int64(unix/1e7), int64(unix%1e7)*100).UTC(), nil
}
