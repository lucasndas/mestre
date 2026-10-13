//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// cmdInstall copies this binary to bin\callerid.exe and points the
// MestreSpike service (created by spikesvc install) at it.
func cmdInstall() error {
	if err := stopService(serviceName, 15*time.Second); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if !strings.EqualFold(self, exePath) {
		if err := copyFile(self, exePath); err != nil {
			return err
		}
	}
	return setBinary(fmt.Sprintf(`"%s" service`, exePath))
}

func cmdRestore() error {
	if err := stopService(serviceName, 15*time.Second); err != nil {
		return err
	}
	return setBinary(fmt.Sprintf(`"%s" service %s`, filepath.Join(binDir, "spikesvc.exe"), serviceName))
}

func setBinary(cmdline string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("%s not installed (run spikesvc install first): %w", serviceName, err)
	}
	defer s.Close()
	cfg, err := s.Config()
	if err != nil {
		return err
	}
	cfg.BinaryPathName = cmdline
	if err := s.UpdateConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("%s (%s) now runs %s\n", serviceName, cfg.ServiceStartName, cmdline)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	// A stopped service's process may still hold the file for a moment.
	var out *os.File
	for end := time.Now().Add(10 * time.Second); ; {
		if out, err = os.Create(dst); err == nil || time.Now().After(end) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func stopService(name string, deadline time.Duration) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return nil
	}
	defer s.Close()
	st, err := s.Control(svc.Stop)
	if err != nil {
		return nil // not running
	}
	end := time.Now().Add(deadline)
	for st.State != svc.Stopped && time.Now().Before(end) {
		// Polling the SCM is the documented way to wait for a stop.
		time.Sleep(200 * time.Millisecond)
		if st, err = s.Query(); err != nil {
			return err
		}
	}
	if st.State != svc.Stopped {
		return fmt.Errorf("%s did not stop in %v", name, deadline)
	}
	return nil
}
