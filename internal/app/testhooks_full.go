//go:build full

package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The hooks of the full suite (docs/qualidade/testes.md#troca-de-cidadela-e-replicação-na-completa),
// only in binaries compiled with the tag full:
//
//	MESTRE_TEST_CLOCK=<socket>     the clock of the test (clock.Remote)
//	MESTRE_TEST_LOCK=<folder>      the fake lock of the epoch the instances share:
//	                               a file epoca-<N> per epoch, created only if absent
//	MESTRE_TEST_OWNER=accept       the fake Owner: every entry and promotion is approved
//	MESTRE_TEST_EXTERNAL=<folder>  a fake external function: <folder>/<instance>.external
//	                               gets a line when the Citadel acts outside and when it stops
//	MESTRE_TEST_CONTROL=<socket>   the control of the test, one JSON line per request:
//	                               status, promote, handover, exec and query
func testHooks(ctx context.Context, d *Deps, cfg Config, getenv func(string) string) (after func(*Engine) error, cleanup func(), err error) {
	var (
		closers []func()
		eng     atomic.Pointer[Engine]
	)
	cleanup = func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
	if p := getenv("MESTRE_TEST_CLOCK"); p != "" {
		f, closeClock, err := clock.Remote(p)
		if err != nil {
			return nil, cleanup, err
		}
		d.Clock = f
		closers = append(closers, func() { _ = closeClock() })
	}
	if dir := getenv("MESTRE_TEST_LOCK"); dir != "" {
		d.Lock = dirLock{dir: dir}
	}
	if getenv("MESTRE_TEST_OWNER") == "accept" {
		o := &testOwner{e: &eng}
		d.Approver, d.Promotions = o, o
	}
	if dir := getenv("MESTRE_TEST_EXTERNAL"); dir != "" {
		path := filepath.Join(dir, cfg.Instance+".external")
		appendLine(path, fmt.Sprintf("start %d", os.Getpid()))
		d.External = append(d.External, recordExternal(path, &eng))
	}
	control := getenv("MESTRE_TEST_CONTROL")
	after = func(e *Engine) error {
		eng.Store(e)
		if control == "" {
			return nil
		}
		ln, err := localchan.Listen(control, localchan.NoAccess{})
		if err != nil {
			return err
		}
		closers = append(closers, func() { _ = ln.Close() })
		go serveControl(ctx, e, ln)
		return nil
	}
	return after, cleanup, nil
}

// dirLock is the fake lock of the instances of a test: a file per epoch,
// which only one process creates (O_EXCL), and wins the one it names.
type dirLock struct{ dir string }

func (l dirLock) Check(context.Context) (uint64, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return 0, err
	}
	var n uint64
	for _, en := range entries {
		if s, ok := strings.CutPrefix(en.Name(), "epoca-"); ok {
			if v, err := strconv.ParseUint(s, 10, 64); err == nil {
				n = max(n, v)
			}
		}
	}
	return n, nil
}

func (l dirLock) Claim(_ context.Context, n uint64, machineID string) (bool, error) {
	p := filepath.Join(l.dir, fmt.Sprintf("epoca-%d", n))
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	switch {
	case err == nil:
		_, werr := f.WriteString(machineID)
		cerr := f.Close()
		if err := errors.Join(werr, cerr); err != nil {
			return false, err
		}
	case !errors.Is(err, fs.ErrExist):
		return false, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false, err
	}
	return string(b) == machineID, nil
}

// testOwner approves everything, as the Owner with the PIN would.
type testOwner struct {
	e *atomic.Pointer[Engine]
	n atomic.Int64
}

func (o *testOwner) Ask(ctx context.Context, r guild.ApprovalRequest) (string, error) {
	e := o.e.Load()
	if e == nil {
		return "", errors.New("test owner: no engine")
	}
	e.mu.Lock()
	run := e.cit
	e.mu.Unlock()
	if run == nil {
		return "", errors.New("test owner: not the Citadel")
	}
	if err := run.citadel.Decide(ctx, r.JoinID, guild.Decision{Accept: true, ApprovedBy: "dono"}); err != nil {
		return "", err
	}
	return fmt.Sprintf("entrada-%d", o.n.Add(1)), nil
}

func (o *testOwner) Withdraw(context.Context, string) {}

func (o *testOwner) AskPromotion(_ context.Context, _ guild.PromotionRequest) (string, error) {
	id := fmt.Sprintf("promocao-%d", o.n.Add(1))
	return id, o.e.Load().DecidePromotion(id, guild.Decision{Accept: true, ApprovedBy: "dono"})
}

func (o *testOwner) WithdrawPromotion(context.Context, string) {}

var appendMu sync.Mutex

func appendLine(path, line string) {
	appendMu.Lock()
	defer appendMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line + "\n")
	_ = f.Close()
}

// recordExternal is the fake external function: every line it writes goes
// through the fence, as a call to the Telegram would.
func recordExternal(path string, eng *atomic.Pointer[Engine]) ExternalFunc {
	return func(ctx context.Context, fence *guild.Fence) error {
		e := eng.Load()
		if e == nil {
			return nil
		}
		_, err := fence.Do(ctx, guild.ExternalTelegram, "test.on", func(context.Context) error {
			appendLine(path, fmt.Sprintf("on %d %d", e.epochs.Epoch(), e.clk.Now().UnixMilli()))
			return nil
		})
		<-ctx.Done()
		appendLine(path, fmt.Sprintf("off %d %d", e.epochs.Epoch(), e.clk.Now().UnixMilli()))
		return err
	}
}

func query(ctx context.Context, db *store.DB, q string) ([][]any, error) {
	rows, err := db.Reader().QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

// controlRequest and controlAnswer are the lines of MESTRE_TEST_CONTROL.
type controlRequest struct {
	Op     string `json:"op"`
	SQL    string `json:"sql,omitempty"`
	Target string `json:"target,omitempty"`
}

type controlAnswer struct {
	OK     bool    `json:"ok"`
	Error  string  `json:"error,omitempty"`
	Status *Status `json:"status,omitempty"`
	LSN    uint64  `json:"lsn,omitempty"`
	Rows   [][]any `json:"rows,omitempty"`
}

func serveControl(ctx context.Context, e *Engine, ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			sc := bufio.NewScanner(c)
			sc.Buffer(make([]byte, 64<<10), 1<<20)
			for sc.Scan() {
				var req controlRequest
				a := controlAnswer{}
				if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
					a.Error = err.Error()
				} else {
					a = control(ctx, e, req)
				}
				b, _ := json.Marshal(a)
				if _, err := c.Write(append(b, '\n')); err != nil {
					return
				}
			}
		}()
	}
}

func control(ctx context.Context, e *Engine, req controlRequest) controlAnswer {
	fail := func(err error) controlAnswer { return controlAnswer{Error: err.Error()} }
	switch req.Op {
	case "status":
		st := e.Status()
		return controlAnswer{OK: true, Status: &st}
	case "promote":
		go func() {
			if err := e.Promote(context.WithoutCancel(ctx), "test"); err != nil {
				e.log.Error("test: promote", "err", err)
			}
		}()
		return controlAnswer{OK: true}
	case "handover":
		go func() {
			if err := e.Handover(context.WithoutCancel(ctx), req.Target, "aprovacao-teste", 0); err != nil {
				e.log.Error("test: handover", "err", err)
			}
		}()
		return controlAnswer{OK: true}
	case "exec":
		var lsn uint64
		err := e.withGuildDB(func(db *store.DB) error {
			if db.IsReplica() {
				return errors.New("not the Citadel")
			}
			if _, err := db.Writer().ExecContext(ctx, req.SQL); err != nil {
				return err
			}
			var err error
			lsn, err = db.LastLSN(ctx)
			return err
		})
		if err != nil {
			return fail(err)
		}
		return controlAnswer{OK: true, LSN: lsn}
	case "query":
		var out [][]any
		if err := e.withGuildDB(func(db *store.DB) error {
			var err error
			out, err = query(ctx, db, req.SQL)
			return err
		}); err != nil {
			return fail(err)
		}
		return controlAnswer{OK: true, Rows: out}
	}
	return fail(fmt.Errorf("unknown op %q", req.Op))
}
