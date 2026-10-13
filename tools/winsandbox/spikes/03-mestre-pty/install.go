//go:build windows

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// cmdInstall copies this binary to bin\mestrepty.exe and bin\ptyhost.exe,
// optionally a new spikesvc.exe (the launcher with start-pty) and the fake
// agent, sets the ACLs of the pty and cli folders and points MestreSpike at
// mestrepty.exe. It does not run "spikesvc install": that resets
// mestre-agente's password, which would lose the Credential Manager login.
func cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	spikesvc := fs.String("spikesvc", "", "spikesvc.exe built from 01-agent-service (launcher with start-pty)")
	fake := fs.String("fakeagent", "", "fakeagent.exe built from cmd/fakeagent")
	_ = fs.Parse(args)
	for _, n := range []string{serviceName, launcherSvc} {
		if err := stopService(n, 15*time.Second); err != nil {
			return err
		}
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	copies := map[string]string{exePath: self, hostPath: self}
	if *spikesvc != "" {
		copies[filepath.Join(binDir, "spikesvc.exe")] = *spikesvc
	}
	if *fake != "" {
		copies[filepath.Join(binDir, "fakeagent.exe")] = *fake
	}
	for dst, src := range copies {
		if strings.EqualFold(src, dst) {
			continue
		}
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copy %s: %w", dst, err)
		}
		fmt.Printf("copied %s\n", dst)
	}

	svcSID, _, _, err := windows.LookupSID("", `NT SERVICE\`+serviceName)
	if err != nil {
		return err
	}
	agent, _, _, err := windows.LookupSID("", agentUser)
	if err != nil {
		return err
	}
	s, a := svcSID.String(), agent.String()
	dirs := []struct{ path, sddl string }{
		// pty: full control to the engine and SYSTEM, inherited; OWNER RIGHTS
		// only READ_CONTROL, inherited (the creator of a socket file cannot
		// change its ACL); mestre-agente may only add files, this folder only.
		{ptyDir, "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + s + ")(A;OICI;RC;;;OW)(A;;0x100002;;;" + a + ")"},
		// cli: mestre-agente reads and writes (connects); Administrators too,
		// for the "outside any terminal" identification case.
		{cliDir, "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + s + ")(A;OICI;FA;;;BA)(A;OICI;0x12019f;;;" + a + ")"},
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d.path, 0o755); err != nil {
			return err
		}
		if err := setPathDACL(d.path, d.sddl); err != nil {
			return fmt.Errorf("ACL %s: %w", d.path, err)
		}
		fmt.Printf("ACL %s: %s\n", d.path, d.sddl)
	}
	if err := os.MkdirAll(hostLogDir, 0o755); err != nil {
		return err
	}
	if err := setBinary(fmt.Sprintf(`"%s" service`, exePath)); err != nil {
		return err
	}
	for _, n := range []string{launcherSvc, serviceName} {
		if err := startService(n); err != nil {
			return err
		}
		fmt.Printf("%s started\n", n)
	}
	return nil
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

func startService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	return waitState(s, svc.Running, 15*time.Second)
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
	if _, err := s.Control(svc.Stop); err != nil {
		return nil // not running
	}
	return waitState(s, svc.Stopped, deadline)
}

func waitState(s *mgr.Service, want svc.State, d time.Duration) error {
	end := time.Now().Add(d)
	for {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.State == want {
			return nil
		}
		if time.Now().After(end) {
			return errors.New(s.Name + " did not reach the state in " + d.String())
		}
		// Polling the SCM is the documented way to wait for a state.
		time.Sleep(200 * time.Millisecond)
	}
}
