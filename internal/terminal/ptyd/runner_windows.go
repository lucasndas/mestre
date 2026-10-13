package ptyd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"syscall"
	"unsafe"

	"github.com/aymanbagabas/go-pty"
	"golang.org/x/sys/windows"

	"github.com/lucasndas/mestre/internal/winsvc/logonsweep"
)

// sysAgent is the Windows part of an agent: the Job Object of the terminal.
// The mestre-pty creates it with KILL_ON_JOB_CLOSE and keeps the only
// handle, so the tree dies with the mestre-pty. The agent starts suspended,
// is put in the Job and only then resumes, so no descendant is born outside
// it (docs/arquitetura/windows.md#conpty).
type sysAgent struct {
	job windows.Handle
	// sweep ends what is left of the logon session when the tree is killed.
	sweep bool
}

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// jobSDDL is the DACL of the Job Object: SYSTEM and the logon session of the
// mestre-pty, which the launcher made for this terminal alone. The account
// (mestre-agente) is not in it, because every terminal runs with it: the
// Job is anonymous and only this process holds a handle, and the DACL keeps
// it that way even if another process of the account got hold of one.
func jobSDDL(logon *windows.SID) string {
	return "D:P(A;;GA;;;SY)(A;;GA;;;" + logon.String() + ")"
}

// jobAttributes are the security attributes of the Job Object. Without a
// logon SID in the token (not a session made by LogonUser) the Job is only
// for SYSTEM and the user of the process.
func jobAttributes() (*windows.SecurityAttributes, error) {
	t := windows.GetCurrentProcessToken()
	sid, err := logonSID(t)
	if err != nil {
		u, uerr := t.GetTokenUser()
		if uerr != nil {
			return nil, fmt.Errorf("user of the mestre-pty: %w", uerr)
		}
		sid = u.User.Sid
	}
	sd, err := windows.SecurityDescriptorFromString(jobSDDL(sid))
	if err != nil {
		return nil, fmt.Errorf("security descriptor of the job: %w", err)
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}

// logonSID is the logon SID of the token (SE_GROUP_LOGON_ID).
func logonSID(t windows.Token) (*windows.SID, error) {
	g, err := t.GetTokenGroups()
	if err != nil {
		return nil, err
	}
	for _, s := range g.AllGroups() {
		if s.Attributes&seGroupLogonID == seGroupLogonID {
			return s.Sid.Copy()
		}
	}
	return nil, errors.New("token has no logon SID")
}

const seGroupLogonID = 0xC0000000

func prepare(cmd *pty.Cmd, sweep bool) (*sysAgent, error) {
	sa, err := jobAttributes()
	if err != nil {
		return nil, err
	}
	job, err := windows.CreateJobObject(sa, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	// No JOB_OBJECT_LIMIT_BREAKAWAY_OK: nothing leaves the Job.
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("set job limits: %w", err)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	return &sysAgent{job: job, sweep: sweep}, nil
}

func (s *sysAgent) abort() { _ = windows.CloseHandle(s.job) }

// started puts the suspended agent in the Job and resumes it.
func (s *sysAgent) started(cmd *pty.Cmd, _ pty.Pty) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false, uint32(cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("open agent: %w", err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if err := windows.AssignProcessToJobObject(s.job, h); err != nil {
		return fmt.Errorf("assign agent to the job: %w", err)
	}
	if r, _, _ := ntResumeProcess.Call(uintptr(h)); r != 0 {
		return fmt.Errorf("resume agent: NTSTATUS 0x%x", r)
	}
	return nil
}

// release keeps the Job: closing the handle would end the tree, and the
// handle goes away with the mestre-pty.
func (*sysAgent) release() {}

// signal is ClosePseudoConsole: the console host ends the clients attached
// to it.
func (*sysAgent) signal(a *ptyAgent) error { return a.closePTY() }

// kill is TerminateJobObject, mandatory: a process with its own console and
// the conhost.exe of that console survive ClosePseudoConsole. Then, when the
// launcher gave this terminal a logon session of its own, it ends what is
// left of the session: the conhost.exe of the ConPTY (outside the Job) and
// whatever escaped the Job through WMI, which carries the logon session of
// the account that asked (docs/arquitetura/terminal.md#encerramento).
func (s *sysAgent) kill(*ptyAgent) error {
	if err := windows.TerminateJobObject(s.job, 1); err != nil {
		return fmt.Errorf("terminate job: %w", err)
	}
	if !s.sweep {
		return nil
	}
	session, err := logonsweep.SessionOfSelf()
	if err != nil {
		return fmt.Errorf("logon session of the mestre-pty: %w", err)
	}
	if _, err := logonsweep.Run(session, logonsweep.Options{}); err != nil {
		return fmt.Errorf("sweep the logon session: %w", err)
	}
	return nil
}

var procIsProcessInJob = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

// inTree is IsProcessInJob on the Job of the terminal, on the very handle
// whose creation time was compared with start, so a PID that was reused is
// never taken for the process asked about (the start identity is the
// decimal FILETIME of GetProcessTimes, as terminal.System.Identify gives).
func (s *sysAgent) inTree(pid int, start string) (bool, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false, nil // no such process
		}
		return false, fmt.Errorf("open process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &e, &k, &u); err != nil {
		return false, fmt.Errorf("process times of %d: %w", pid, err)
	}
	if start != "" && strconv.FormatUint(uint64(c.HighDateTime)<<32|uint64(c.LowDateTime), 10) != start {
		return false, nil
	}
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false, fmt.Errorf("exit code of %d: %w", pid, err)
	}
	if code != stillActive {
		return false, nil
	}
	var in int32
	r, _, callErr := procIsProcessInJob.Call(uintptr(h), uintptr(s.job), uintptr(unsafe.Pointer(&in)))
	if r == 0 {
		return false, fmt.Errorf("IsProcessInJob: %w", callErr)
	}
	return in != 0, nil
}

// stillActive is STILL_ACTIVE, the exit code of a running process.
const stillActive = 259

func endOfOutput(err error) error {
	if errors.Is(err, os.ErrClosed) || errors.Is(err, windows.ERROR_BROKEN_PIPE) {
		return io.EOF
	}
	return err
}

func exitInfo(ps *os.ProcessState) ExitInfo { return ExitInfo{Code: ps.ExitCode()} }
