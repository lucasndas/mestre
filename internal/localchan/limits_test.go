package localchan_test

import (
	"fmt"
	"io"
	"net"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/testkit/localrig"
)

// A process that floods the engine with connections does not take it from
// the others: past MaxConnsPerPeer the extra connections of that process are
// refused with `limit` before anything is read, the other processes are
// served, and once its connections close it is served again; past MaxConns
// a new connection is closed at once.
func TestTheConnectionsOfOneProcessAreBounded(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{Config: localchan.Config{MaxConnsPerPeer: 2, MaxConns: 4}}, func(t *testing.T, r *localrig.Rig) {
		r.Process("flood", "")
		r.Process("other", "")
		debug := `{"v":1,"id":"%s","agent_id":"","cmd":"debug","args":[],"flags":{},"cwd":""}`
		hold := func(host string) net.Conn {
			c, err := r.Dialer(host)(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			return c
		}
		a, b := hold("flood"), hold("flood")
		synctest.Wait()
		if got := final(t, rawCall(t, r, "flood", fmt.Sprintf(debug, "f3"))); got.OK || got.Error.Code != localchan.CodeLimit {
			t.Fatalf("the third connection of the process: %+v; want limit", got)
		}
		if got := final(t, rawCall(t, r, "other", fmt.Sprintf(debug, "o1"))); !got.OK {
			t.Fatalf("another process: %+v", got)
		}
		_ = a.Close()
		synctest.Wait()
		if got := final(t, rawCall(t, r, "flood", fmt.Sprintf(debug, "f4"))); !got.OK {
			t.Fatalf("the process again, below the limit: %+v", got)
		}
		// The whole server: b, two of other and one more are 4; the next is
		// closed with nothing read or answered.
		o1, o2, o3 := hold("other"), hold("other"), hold("flood")
		synctest.Wait()
		c := hold("other")
		if n, err := c.Read(make([]byte, 1)); n != 0 || err != io.EOF {
			t.Fatalf("a connection over the limit of the server: read %d, %v; want it closed", n, err)
		}
		for _, x := range []net.Conn{b, o1, o2, o3, c} {
			_ = x.Close()
		}
	})
}
