//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lucasndas/mestre/internal/realaccount"
)

// The Linux machine: two system users (the engine's and the agents'), the
// packaged unit of the launcher with a drop-in that swaps its binary and
// account, three launchers (the installation's, one without a delegated cgroup
// and one the tests restart), and the tests run as the engine in a transient
// unit, the cgroup the launchers accept it from. Everything runs through sudo
// (the runners of GitHub have it without a password).

const (
	unitDir     = "/etc/systemd/system"
	sudoersFile = "/etc/sudoers.d/mestre-ci-ra"
	ptraceScope = "/proc/sys/kernel/yama/ptrace_scope"
)

// sudo runs a command as root, or directly when this process is root.
func sudo(ctx context.Context, out io.Writer, name string, args ...string) error {
	if os.Geteuid() == 0 {
		return runCmd(ctx, out, nil, name, args...)
	}
	return runCmd(ctx, out, nil, "sudo", append([]string{"-n", name}, args...)...)
}

// install writes content to path through sudo, with mode and owner.
func install(ctx context.Context, out io.Writer, path, mode, content string) error {
	f, err := os.CreateTemp("", "realaccount-file")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(content); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return sudo(ctx, out, "install", "-D", "-m", mode, "-o", "root", "-g", "root", f.Name(), path)
}

func userExists(ctx context.Context, name string) bool {
	_, err := output(ctx, "id", "-u", name)
	return err == nil
}

// dropIn is the override of the packaged unit for one instance: the account
// and the binary of the test, with the capabilities, Delegate, KillMode and
// the runtime folder of the packaged unit untouched. The instance without a
// cgroup also drops Delegate: it is the launcher of a machine where LIN-11 of
// the checklist failed.
func dropIn(inst string, engineUID int) string {
	args := fmt.Sprintf("helper-launcher --instance %s --bin %s/bin --agent %s --engine-uid %d --engine-cgroup /system.slice/%s",
		inst, realaccount.LinuxStage, realaccount.LinuxAgentUser, engineUID, realaccount.LinuxEngineUnit)
	s := "[Service]\nUser=" + realaccount.LinuxEngineUser + "\nExecStart=\nExecStart=" + realaccount.LinuxStage + "/realaccount.test " + args
	if inst == "nocg" {
		s += " --no-cgroup\nDelegate=no\nDelegateSubgroup=\n"
	} else {
		s += "\n"
	}
	return s
}

func platformRun(ctx context.Context, out io.Writer, o options) (err error) {
	b, err := build(ctx, out)
	defer os.RemoveAll(b.dir)
	if err != nil {
		return err
	}
	scope, _ := os.ReadFile(ptraceScope)
	if !o.keep {
		defer cleanup(out, strings.TrimSpace(string(scope)))
	}

	for _, u := range []string{realaccount.LinuxEngineUser, realaccount.LinuxAgentUser} {
		if userExists(ctx, u) {
			return fmt.Errorf("the account %s exists: this machine already ran the tests, or it is not disposable", u)
		}
		home := "/nonexistent"
		if err := sudo(ctx, out, "useradd", "--system", "--user-group", "--no-create-home", "--home-dir", home, "--shell", "/usr/sbin/nologin", u); err != nil {
			return err
		}
	}
	uid, err := output(ctx, "id", "-u", realaccount.LinuxEngineUser)
	if err != nil {
		return err
	}
	engineUID, err := strconv.Atoi(strings.TrimSpace(uid))
	if err != nil {
		return err
	}

	// What the tests run: read-only for every account.
	stage := realaccount.LinuxStage
	bin := filepath.Join(stage, "bin", realaccount.PTYVersion)
	for _, step := range [][]string{
		{"install", "-d", "-m", "0755", stage, filepath.Dir(bin), bin},
		{"install", "-m", "0755", b.test, filepath.Join(stage, "realaccount.test")},
		{"install", "-m", "0755", b.pty, filepath.Join(bin, "mestre-pty")},
		{"install", "-d", "-m", "0700", "-o", realaccount.LinuxEngineUser, "-g", realaccount.LinuxEngineUser, "/var/lib/mestre-ci"},
	} {
		if err := sudo(ctx, out, step[0], step[1:]...); err != nil {
			return err
		}
	}

	// The packaged unit, as the installer will put it.
	unit, err := os.ReadFile("packaging/systemd/mestre-launcher@.service")
	if err != nil {
		return err
	}
	if err := install(ctx, out, filepath.Join(unitDir, "mestre-launcher@.service"), "0644", string(unit)); err != nil {
		return err
	}
	for _, inst := range realaccount.LinuxLaunchers {
		if err := install(ctx, out, filepath.Join(unitDir, "mestre-launcher@"+inst+".service.d", "ci.conf"), "0644", dropIn(inst, engineUID)); err != nil {
			return err
		}
	}
	// The engine may restart the launcher of one instance, which the restart
	// test does; the real engine is restarted by the Guardian or the installer.
	rule := fmt.Sprintf("%s ALL=(root) NOPASSWD: /usr/bin/systemctl restart mestre-launcher@rst.service\n", realaccount.LinuxEngineUser)
	if err := install(ctx, out, sudoersFile, "0440", rule); err != nil {
		return err
	}
	// With the default ptrace_scope of the distributions only the descendants
	// of a process can be traced, and the denial of ptrace to the mestre-pty
	// would prove nothing: lower it for the run, so only PR_SET_DUMPABLE
	// stands between another agent and the mestre-pty.
	if err := sudo(ctx, out, "sysctl", "-w", "kernel.yama.ptrace_scope=0"); err != nil {
		fmt.Fprintf(out, "realaccount: ptrace_scope not lowered (%v): the ptrace control of CA-TERM-11 will say so\n", err)
	}

	if err := sudo(ctx, out, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	for _, inst := range realaccount.LinuxLaunchers {
		if err := sudo(ctx, out, "systemctl", "start", "mestre-launcher@"+inst+".service"); err != nil {
			dumpJournal(out)
			return err
		}
	}

	// The tests run twice: as the engine, in the unit the launchers accept, and
	// as the same account in another unit, which they must refuse.
	const outsider = "TestLinuxTheLauncherRefusesTheEngineAccountOutsideItsUnit"
	for _, r := range []struct{ unit, flag, value string }{
		{strings.TrimSuffix(realaccount.LinuxEngineUnit, ".service"), "-test.skip", "^" + outsider + "$"},
		{"mestre-ci-outsider", "-test.run", "^" + outsider + "$"},
	} {
		args := []string{"-n", "systemd-run", "--unit=" + r.unit,
			"--uid=" + realaccount.LinuxEngineUser, "--gid=" + realaccount.LinuxEngineUser,
			"--wait", "--pipe", "--collect", "--quiet",
			"-E", realaccount.EnvDisposable + "=1", "-E", realaccount.EnvRole + "=engine",
			"-p", "WorkingDirectory=/var/lib/mestre-ci",
			filepath.Join(stage, "realaccount.test"), "-test.v", "-test.count=1", "-test.timeout=" + o.timeout.String(), r.flag, r.value}
		cmd := "sudo"
		if os.Geteuid() == 0 {
			cmd, args = args[1], args[2:]
		}
		if err := runCmd(ctx, out, nil, cmd, args...); err != nil {
			dumpJournal(out)
			return fmt.Errorf("the tests failed: %w", err)
		}
	}
	return nil
}

func dumpJournal(out io.Writer) {
	fmt.Fprintln(out, "--- journal of the launchers")
	_ = runCmd(context.Background(), out, nil, "sudo", "-n", "journalctl", "--no-pager", "-n", "200", "-u", "mestre-launcher@*", "-u", realaccount.LinuxEngineUnit)
}

// cleanup removes what the run made, best effort: the machine is disposable.
func cleanup(out io.Writer, scope string) {
	ctx := context.Background()
	var errs []error
	try := func(name string, args ...string) {
		if err := sudo(ctx, io.Discard, name, args...); err != nil {
			errs = append(errs, err)
		}
	}
	for _, inst := range realaccount.LinuxLaunchers {
		try("systemctl", "stop", "mestre-launcher@"+inst+".service")
	}
	try("systemctl", "stop", realaccount.LinuxEngineUnit)
	try("pkill", "-KILL", "-u", realaccount.LinuxAgentUser)
	try("rm", "-rf", filepath.Join(unitDir, "mestre-launcher@.service"), sudoersFile, "/opt/mestre-ci", "/var/lib/mestre-ci", "/run/mestre")
	for _, inst := range realaccount.LinuxLaunchers {
		try("rm", "-rf", filepath.Join(unitDir, "mestre-launcher@"+inst+".service.d"))
	}
	try("systemctl", "daemon-reload")
	try("systemctl", "reset-failed")
	for _, u := range []string{realaccount.LinuxAgentUser, realaccount.LinuxEngineUser} {
		try("userdel", u)
	}
	if scope != "" {
		try("sysctl", "-w", "kernel.yama.ptrace_scope="+scope)
	}
	if err := errors.Join(errs...); err != nil {
		fmt.Fprintf(out, "realaccount: cleanup left something behind (ignored, the machine is disposable): %v\n", err)
	}
}
