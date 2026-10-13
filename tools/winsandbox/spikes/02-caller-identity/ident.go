//go:build windows

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The four ways the spike identifies the terminal of a pipe client (PID from
// GetNamedPipeClientProcessId):
//
//   - tree_toolhelp: parent PIDs from a Toolhelp snapshot, creation times by
//     OpenProcess + GetProcessTimes (the method in the task);
//   - tree_ntqsi: parent PIDs and creation times from one
//     NtQuerySystemInformation(SystemProcessInformation) call, no process
//     handles;
//   - job_isprocessinjob: IsProcessInJob with a handle to the caller and the
//     job handle each terminal keeps (plan B);
//   - job_pidlist: the caller's PID in JobObjectBasicProcessIdList of each
//     terminal's job (plan B without opening the caller).
//
// The tree walk follows docs/arquitetura/seguranca.md: at each step the parent
// may not have started after the child (else its PID was reused); then the
// walk restarts from the caller, up to 3 times.

type methodResult struct {
	Terminal   string   `json:"terminal"` // "" = refused
	Path       []string `json:"path,omitempty"`
	Err        string   `json:"err,omitempty"`
	MeanMicros float64  `json:"mean_us"`
	Runs       int      `json:"runs"`
}

type identity struct {
	Label        string       `json:"label"`
	Note         string       `json:"note,omitempty"`
	PID          uint32       `json:"pid"`
	Image        string       `json:"image"`
	OpenErr      string       `json:"open_err,omitempty"` // OpenProcess(QUERY_LIMITED) by the service
	TreeToolhelp methodResult `json:"tree_toolhelp"`
	TreeNtqsi    methodResult `json:"tree_ntqsi"`
	JobInJob     methodResult `json:"job_isprocessinjob"`
	JobPIDList   methodResult `json:"job_pidlist"`
	At           time.Time    `json:"at"`
}

type method func(pid uint32) (string, []string, error)

// measure runs m once for the result, then repeats it for the cost: Go's
// clock on Windows ticks in ~0.5 ms, so one call alone reads as 0 or 500 µs.
// It repeats until 20 ms have passed (at least 20 runs, at most 5000).
func measure(m method, pid uint32) methodResult {
	term, path, err := m(pid)
	r := methodResult{Terminal: term, Path: path}
	if err != nil {
		r.Err = err.Error()
	}
	t := time.Now()
	for r.Runs < 20 || (time.Since(t) < 20*time.Millisecond && r.Runs < 5000) {
		_, _, _ = m(pid)
		r.Runs++
	}
	r.MeanMicros = float64(time.Since(t).Nanoseconds()) / 1e3 / float64(r.Runs)
	return r
}

func identify(pid uint32, label, note string) *identity {
	id := &identity{Label: label, Note: note, PID: pid, At: time.Now()}
	if h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); err != nil {
		id.OpenErr = err.Error()
	} else {
		id.Image = filepath.Base(processImage(h))
		windows.CloseHandle(h)
	}
	if id.Image == "" {
		if p, ok := ntqsiSnapshot()[pid]; ok {
			id.Image = p.name
		}
	}
	id.TreeToolhelp = measure(treeToolhelp, pid)
	id.TreeNtqsi = measure(treeNtqsi, pid)
	id.JobInJob = measure(jobInJob, pid)
	id.JobPIDList = measure(jobPIDList, pid)
	return id
}

type procInfo struct {
	ppid    uint32
	created int64 // 0 = unknown
	name    string
}

// walk climbs from pid using parentOf and createdOf.
func walk(pid uint32, parentOf func(uint32) (uint32, string, bool), createdOf func(uint32) (int64, error)) (string, []string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		cur := pid
		curCT, err := createdOf(cur)
		if err != nil {
			return "", nil, fmt.Errorf("caller %d: %w", cur, err)
		}
		var path []string
		reused := false
		for depth := 0; depth < 64; depth++ {
			ppid, name, ok := parentOf(cur)
			if !ok {
				return "", path, fmt.Errorf("process %d is gone", cur)
			}
			path = append(path, fmt.Sprintf("%d:%s", cur, name))
			if t := rootTerminal(cur, curCT); t != "" {
				return t, path, nil
			}
			if ppid == 0 || ppid == 4 {
				return "", path, errors.New("reached the top without a terminal root")
			}
			if _, _, ok := parentOf(ppid); !ok {
				return "", path, fmt.Errorf("parent %d is not running (orphan)", ppid)
			}
			pct, err := createdOf(ppid)
			if err != nil {
				return "", path, fmt.Errorf("parent %d: %w", ppid, err)
			}
			if pct > curCT {
				// The parent died and its PID went to a newer process.
				path = append(path, fmt.Sprintf("%d:(reused PID, started after the child)", ppid))
				reused = true
				break
			}
			cur, curCT = ppid, pct
		}
		if !reused {
			return "", path, errors.New("too deep")
		}
		if attempt == 2 {
			return "", path, errors.New("parent PID reused 3 times: not_identified")
		}
	}
	return "", nil, errors.New("unreachable")
}

func toolhelpSnapshot() (map[uint32]procInfo, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	m := map[uint32]procInfo{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		m[e.ProcessID] = procInfo{ppid: e.ParentProcessID, name: windows.UTF16ToString(e.ExeFile[:])}
	}
	return m, nil
}

func openCreated(pid uint32) (int64, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, fmt.Errorf("OpenProcess: %w", err)
	}
	defer windows.CloseHandle(h)
	return processCreated(h)
}

func treeToolhelp(pid uint32) (string, []string, error) {
	snap, err := toolhelpSnapshot()
	if err != nil {
		return "", nil, err
	}
	return walk(pid, func(p uint32) (uint32, string, bool) {
		i, ok := snap[p]
		return i.ppid, i.name, ok
	}, openCreated)
}

func ntqsiSnapshot() map[uint32]procInfo {
	size := uint32(512 << 10)
	for {
		buf := make([]byte, size)
		var n uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&buf[0]), size, &n)
		if errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) {
			size = n + 64<<10
			continue
		}
		if err != nil {
			return nil
		}
		m := map[uint32]procInfo{}
		for off := uint32(0); ; {
			p := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[off]))
			name := "System Idle Process"
			if p.ImageName.Buffer != nil {
				name = p.ImageName.String()
			}
			m[uint32(p.UniqueProcessID)] = procInfo{ppid: uint32(p.InheritedFromUniqueProcessID), created: p.CreateTime, name: name}
			if p.NextEntryOffset == 0 {
				return m
			}
			off += p.NextEntryOffset
		}
	}
}

func treeNtqsi(pid uint32) (string, []string, error) {
	snap := ntqsiSnapshot()
	if snap == nil {
		return "", nil, errors.New("NtQuerySystemInformation failed")
	}
	return walk(pid, func(p uint32) (uint32, string, bool) {
		i, ok := snap[p]
		return i.ppid, i.name, ok
	}, func(p uint32) (int64, error) {
		i, ok := snap[p]
		if !ok {
			return 0, errors.New("not running")
		}
		return i.created, nil
	})
}

func jobInJob(pid uint32) (string, []string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", nil, fmt.Errorf("OpenProcess: %w", err)
	}
	defer windows.CloseHandle(h)
	var found []string
	for _, t := range terminalsSnapshot() {
		in, err := isProcessInJob(h, t.job)
		if err != nil {
			return "", nil, err
		}
		if in {
			found = append(found, t.name)
		}
	}
	return pickOne(found)
}

func jobPIDList(pid uint32) (string, []string, error) {
	var found []string
	for _, t := range terminalsSnapshot() {
		if slices.Contains(jobPIDs(t.job), pid) {
			found = append(found, t.name)
		}
	}
	return pickOne(found)
}

func pickOne(found []string) (string, []string, error) {
	switch len(found) {
	case 0:
		return "", nil, errors.New("in no terminal's job")
	case 1:
		return found[0], found, nil
	}
	return "", found, errors.New("in more than one terminal's job")
}

func jobPIDs(job windows.Handle) []uint32 {
	const max = 512
	buf := make([]uintptr, 2+max) // two ULONGs pack into the first uintptr on amd64
	err := windows.QueryInformationJobObject(job, windows.JobObjectBasicProcessIdList,
		uintptr(unsafe.Pointer(&buf[0])), uint32(len(buf))*uint32(unsafe.Sizeof(buf[0])), nil)
	if err != nil {
		return nil
	}
	hdr := (*[2]uint32)(unsafe.Pointer(&buf[0]))
	var out []uint32
	for _, p := range buf[1 : 1+int(hdr[1])] {
		out = append(out, uint32(p))
	}
	return out
}
