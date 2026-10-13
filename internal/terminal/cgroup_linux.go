package terminal

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const cgroupRoot = "/sys/fs/cgroup"

// DelegatedCgroup returns the cgroup v2 directory of this process if it may
// create child cgroups in it: a delegated subtree (Delegate=yes on the
// service, or `systemd-run --user --scope -p Delegate=yes`).
func DelegatedCgroup() (string, error) {
	rel, err := procCgroup(os.Getpid())
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoCgroup, err)
	}
	base := filepath.Join(cgroupRoot, rel)
	probe, err := os.MkdirTemp(base, "probe-")
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoCgroup, err)
	}
	if err := os.Remove(probe); err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoCgroup, err)
	}
	return base, nil
}

// procCgroup returns the cgroup v2 path of pid ("0::<path>" in
// /proc/<pid>/cgroup).
func procCgroup(pid int) (string, error) {
	f, err := os.Open("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if p, ok := strings.CutPrefix(sc.Text(), "0::"); ok {
			return p, nil
		}
	}
	return "", errors.New("no cgroup v2 entry")
}

// createCgroup makes base/term-<id> and opens it for CgroupFD.
func createCgroup(base, id string) (string, *os.File, error) {
	dir := filepath.Join(base, "term-"+id)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", nil, fmt.Errorf("create cgroup: %w", err)
	}
	f, err := os.Open(dir)
	if err != nil {
		_ = os.Remove(dir)
		return "", nil, fmt.Errorf("open cgroup: %w", err)
	}
	return dir, f, nil
}

// killCgroup ends every process of the cgroup with cgroup.kill (Linux
// 5.14+), waits for "populated 0" in cgroup.events through inotify, with no
// polling, and removes the directory. A directory that is already gone is
// success.
func killCgroup(ctx context.Context, dir string) error {
	if err := os.WriteFile(filepath.Join(dir, "cgroup.kill"), []byte("1"), 0); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("cgroup.kill %s: %w", dir, err)
	}
	if err := waitUnpopulated(ctx, dir); err != nil {
		return err
	}
	if err := os.Remove(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove cgroup %s: %w", dir, err)
	}
	return nil
}

func waitUnpopulated(ctx context.Context, dir string) error {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return fmt.Errorf("inotify: %w", err)
	}
	f := os.NewFile(uintptr(fd), "inotify")
	defer f.Close()
	stop := context.AfterFunc(ctx, func() { _ = f.Close() })
	defer stop()
	events := filepath.Join(dir, "cgroup.events")
	// The watch comes before the first read, so a change in between is not lost.
	if _, err := unix.InotifyAddWatch(fd, events, unix.IN_MODIFY); err != nil {
		return fmt.Errorf("watch %s: %w", events, err)
	}
	buf := make([]byte, 4096)
	for {
		b, err := os.ReadFile(events)
		if err != nil {
			return fmt.Errorf("read %s: %w", events, err)
		}
		if strings.Contains(string(b), "populated 0") {
			return nil
		}
		if _, err := f.Read(buf); err != nil {
			return errors.Join(fmt.Errorf("cgroup %s still has processes", dir), err)
		}
	}
}
