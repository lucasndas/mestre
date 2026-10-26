package store

import (
	"context"
	"database/sql/driver"
	"fmt"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/pkg/clock"
)

// closeWait bounds how long closing the pools waits for the queries that were
// still running (a leaked Rows would otherwise hold the close for good).
const closeWait = 10 * time.Second

// connTracker counts the connections of the driver that are open for the pools
// of one file. sql.DB.Close closes the idle connections at once, but one that
// is serving a query is closed only when the query ends: on Windows the file
// (and its -wal) stays open until then, and neither Replace nor the Wipe of a
// Tower can remove it. wait is how they know the file is free.
type connTracker struct {
	mu   sync.Mutex
	n    int
	idle chan struct{} // closed when n reaches 0 while wait is waiting
}

func (t *connTracker) add() {
	t.mu.Lock()
	t.n++
	t.mu.Unlock()
}

func (t *connTracker) done() {
	t.mu.Lock()
	t.n--
	if t.n == 0 && t.idle != nil {
		close(t.idle)
		t.idle = nil
	}
	t.mu.Unlock()
}

// wait returns nil when every connection is closed, and an error with the
// count that is still open after closeWait.
func (t *connTracker) wait(clk clock.Clock) error {
	t.mu.Lock()
	if t.n == 0 {
		t.mu.Unlock()
		return nil
	}
	idle := make(chan struct{})
	t.idle = idle
	t.mu.Unlock()
	tm := clk.NewTimer(closeWait)
	defer tm.Stop()
	select {
	case <-idle:
		return nil
	case <-tm.C():
		t.mu.Lock()
		n := t.n
		t.idle = nil
		t.mu.Unlock()
		if n == 0 {
			return nil
		}
		return fmt.Errorf("store: %d connection(s) still open after %s: a query or transaction was not ended", n, closeWait)
	}
}

// trackConnector wraps a connector so that every connection it opens is
// counted until it is closed.
type trackConnector struct {
	driver.Connector
	t *connTracker
}

func (c trackConnector) Connect(ctx context.Context) (driver.Conn, error) {
	c.t.add()
	raw, err := c.Connector.Connect(ctx)
	if err != nil {
		c.t.done()
		return nil, err
	}
	inner, ok := raw.(innerConn)
	if !ok {
		_ = raw.Close()
		c.t.done()
		return nil, fmt.Errorf("store: the sqlite connection %T cannot be followed", raw)
	}
	return &trackConn{innerConn: inner, t: c.t}, nil
}

type trackConn struct {
	innerConn
	t    *connTracker
	once sync.Once
}

func (c *trackConn) Close() error {
	err := c.innerConn.Close()
	c.once.Do(c.t.done)
	return err
}
