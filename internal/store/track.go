package store

import (
	"context"
	"database/sql/driver"
	"sync"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
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

// wait returns when every connection is closed, or after closeWait.
func (t *connTracker) wait(clk clock.Clock) {
	t.mu.Lock()
	if t.n == 0 {
		t.mu.Unlock()
		return
	}
	idle := make(chan struct{})
	t.idle = idle
	t.mu.Unlock()
	tm := clk.NewTimer(closeWait)
	defer tm.Stop()
	select {
	case <-idle:
	case <-tm.C():
	}
}

// trackConnector wraps a connector so that every connection it opens is
// counted until it is closed.
type trackConnector struct {
	driver.Connector
	t *connTracker
}

func (c trackConnector) Connect(ctx context.Context) (driver.Conn, error) {
	raw, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	inner, ok := raw.(innerConn)
	if !ok {
		return raw, nil // not one this package knows how to follow
	}
	c.t.add()
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
