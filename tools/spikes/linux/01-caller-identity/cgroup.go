//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// DelegatedCgroup returns the cgroup v2 directory of this process if the
// process may create child cgroups in it (a delegated subtree: systemd
// Delegate=yes, or `systemd-run --user --scope -p Delegate=yes`).
func DelegatedCgroup() (string, error) {
	p, err := Cgroup(os.Getpid())
	if err != nil {
		return "", err
	}
	base := filepath.Join("/sys/fs/cgroup", p)
	probe := filepath.Join(base, fmt.Sprintf("spike-probe-%d", os.Getpid()))
	if err := os.Mkdir(probe, 0o755); err != nil {
		return "", err
	}
	return base, os.Remove(probe)
}

// killCgroup kills every process of the cgroup (cgroup.kill, Linux 5.14+),
// waits for "populated 0" in cgroup.events (inotify, no polling) and
// removes the directory.
func killCgroup(ctx context.Context, dir string) error {
	if err := os.WriteFile(filepath.Join(dir, "cgroup.kill"), []byte("1"), 0); err != nil {
		return err
	}
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "inotify")
	defer f.Close()
	stop := context.AfterFunc(ctx, func() { f.Close() })
	defer stop()
	events := filepath.Join(dir, "cgroup.events")
	if _, err := syscall.InotifyAddWatch(fd, events, syscall.IN_MODIFY); err != nil {
		return err
	}
	buf := make([]byte, 4096)
	for {
		b, err := os.ReadFile(events)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), "populated 0") {
			return os.Remove(dir)
		}
		if _, err := f.Read(buf); err != nil {
			return errors.Join(fmt.Errorf("cgroup %s still populated", dir), err)
		}
	}
}
