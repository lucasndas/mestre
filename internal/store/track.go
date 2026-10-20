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
type connTracker struct{ open sync.WaitGroup }

// wait returns when every connection is closed, or after closeWait.
func (t *connTracker) wait(clk clock.Clock) {
	done := make(chan struct{})
	go func() {
		t.open.Wait()
		close(done)
	}()
	tm := clk.NewTimer(closeWait)
	defer tm.Stop()
	select {
	case <-done:
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
	c.t.open.Add(1)
	return &trackConn{innerConn: inner, t: c.t}, nil
}

type trackConn struct {
	innerConn
	t    *connTracker
	once sync.Once
}

func (c *trackConn) Close() error {
	err := c.innerConn.Close()
	c.once.Do(c.t.open.Done)
	return err
}
