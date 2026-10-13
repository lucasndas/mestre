//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// cli.sock: the future local channel of the CLI
// (docs/arquitetura/seguranca.md#identidade-de-quem-chama-a-cli). The engine
// reads the caller's PID with SIO_AF_UNIX_GETPEERPID, refuses a process that
// started after the connection was accepted, and asks each ptyhost whether
// that PID, with that creation time, is in its job (IsProcessInJob there).

type probeMsg struct {
	Label   string `json:"label"`
	Note    string `json:"note,omitempty"`
	SelfPID uint32 `json:"self_pid"`
}

type identResult struct {
	Label     string            `json:"label"`
	Note      string            `json:"note,omitempty"`
	PeerPID   uint32            `json:"peer_pid"`
	SelfPID   uint32            `json:"self_pid"`
	Image     string            `json:"image,omitempty"`
	Terminal  string            `json:"terminal"` // "" = refused
	Answers   map[string]string `json:"answers,omitempty"`
	Err       string            `json:"err,omitempty"`
	CostMicro float64           `json:"cost_us"`
}

func serveCLI() {
	_ = os.Remove(cliSock) // a socket left by the previous engine
	ln, err := net.Listen("unix", cliSock)
	if err != nil {
		bootf("cli.sock: %v", err)
		return
	}
	bootf("cli.sock listening at %s; SD %s", cliSock, pathSD(cliSock))
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Printf("cli accept: %v", err)
			return
		}
		go handleCLI(c, nowFiletime())
	}
}

func handleCLI(c net.Conn, accepted int64) {
	defer c.Close()
	r := &identResult{}
	start := time.Now()
	peer, perr := peerPID(c)
	r.PeerPID = peer
	l := newLineConn(c)
	var m probeMsg
	if err := l.recv(&m, 10*time.Second); err != nil {
		log.Printf("cli: read: %v", err)
		return
	}
	r.Label, r.Note, r.SelfPID = m.Label, m.Note, m.SelfPID
	if perr != nil {
		r.Err = perr.Error()
	} else {
		identify(r, accepted)
	}
	r.CostMicro = float64(time.Since(start).Nanoseconds()) / 1e3
	b, _ := json.Marshal(r)
	log.Printf("cli identity: %s", b)
	probesMu.Lock()
	probes[r.Label] = r
	close(probesChanged)
	probesChanged = make(chan struct{})
	probesMu.Unlock()
	_ = l.send(r)
}

func identify(r *identResult, accepted int64) {
	h, created, err := openCreated(r.PeerPID, 0)
	if err != nil {
		r.Err = "OpenProcess: " + err.Error()
		return
	}
	r.Image = filepath.Base(processImage(h))
	windows.CloseHandle(h)
	if created > accepted {
		r.Err = "process started after the connection was accepted (PID reused)"
		return
	}
	termsMu.Lock()
	ts := make([]*term, 0, len(terms))
	for _, t := range terms {
		ts = append(ts, t)
	}
	termsMu.Unlock()
	r.Answers = map[string]string{}
	var found []string
	for _, t := range ts {
		resp, err := t.call(hostReq{Op: "injob", PID: r.PeerPID, Created: created}, 5*time.Second)
		switch {
		case err != nil:
			r.Answers[t.rec.ID] = "error: " + err.Error()
		case resp.In:
			r.Answers[t.rec.ID] = "in job"
			found = append(found, t.rec.ID)
		case resp.Error != "":
			r.Answers[t.rec.ID] = "no (" + resp.Error + ")"
		default:
			r.Answers[t.rec.ID] = "no"
		}
	}
	switch len(found) {
	case 0:
		r.Err = "in no terminal's job"
	case 1:
		r.Terminal = found[0]
	default:
		r.Err = fmt.Sprintf("in more than one terminal's job: %v", found)
	}
}

func waitProbes(labels []string, d time.Duration) (map[string]*identResult, string) {
	start := time.Now()
	deadline := start.Add(d)
	for {
		probesMu.Lock()
		out := map[string]*identResult{}
		for _, l := range labels {
			if r, ok := probes[l]; ok {
				out[l] = r
			}
		}
		ch := probesChanged
		probesMu.Unlock()
		left := time.Until(deadline)
		if len(out) == len(labels) || left <= 0 {
			return out, time.Since(start).Round(time.Millisecond).String()
		}
		t := time.NewTimer(left)
		select {
		case <-ch:
			t.Stop()
		case <-t.C:
		}
	}
}
