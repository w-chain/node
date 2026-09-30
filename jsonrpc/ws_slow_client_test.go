package jsonrpc

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
)

// newSilentClientConn returns the server side of a real WS connection whose
// client never reads, so the server's socket buffers fill up.
func newSilentClientConn(t *testing.T) *websocket.Conn {
	t.Helper()

	serverConn := make(chan *websocket.Conn, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{WriteBufferSize: 1024}

		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)

			return
		}

		serverConn <- c
	}))
	t.Cleanup(srv.Close)

	dialer := websocket.Dialer{ReadBufferSize: 1024}

	client, _, err := dialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	// Shrink the client's kernel receive buffer so it fills quickly.
	if tcp, ok := client.UnderlyingConn().(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(1024)
	}

	return <-serverConn
}

// P2-C3: a subscriber that stops reading must never block the writer. Before
// the fix WriteMessage blocked forever and froze the filter manager, and with
// it block import on the node.
func TestWSWrapper_SilentClientNeverBlocksWriter(t *testing.T) {
	t.Parallel()

	w := newWSWrapper(newSilentClientConn(t), hclog.NewNullLogger())
	defer w.close()

	payload := []byte(strings.Repeat("x", 64*1024))

	var gotSlow bool

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		start := time.Now()
		err := w.WriteMessage(websocket.TextMessage, payload)
		require.Less(t, time.Since(start), time.Second, "WriteMessage must not block")

		if err != nil {
			require.True(t, errors.Is(err, net.ErrClosed), "a dropped client is reported as closed: %v", err)

			gotSlow = true

			break
		}
	}

	require.True(t, gotSlow, "a client that never reads must be disconnected")

	select {
	case <-w.closeCh:
	case <-time.After(time.Second):
		t.Fatal("the connection must be closed")
	}
}

// P2-M1: disconnecting removes every subscription of the connection, not only
// the last one, and one connection cannot hold unlimited subscriptions.
func TestFilterManager_RemoveAllFiltersOfWs(t *testing.T) {
	t.Parallel()

	f := NewFilterManager(hclog.NewNullLogger(), newMockStore(), 1000)
	defer f.Close()

	conn := &nopWsConn{}
	other := &nopWsConn{}

	for i := 0; i < 50; i++ {
		f.NewBlockFilter(conn)
		f.NewPendingTxFilter(conn)
	}

	keep := f.NewBlockFilter(other)

	require.Equal(t, 100, f.WsFilterCount(conn))

	f.RemoveFilterByWs(conn)

	require.Equal(t, 0, f.WsFilterCount(conn))
	require.True(t, f.Exists(keep), "another connection's subscription stays")

	f.RLock()
	require.Len(t, f.filters, 1)
	f.RUnlock()

	// Uninstalling one subscription by id keeps the index in step.
	f.Uninstall(keep)
	require.Equal(t, 0, f.WsFilterCount(other))
}

func TestDispatcher_SubscriptionCapPerConnection(t *testing.T) {
	t.Parallel()

	d := newTestDispatcher(t, hclog.NewNullLogger(), newMockStore(), &dispatcherParams{})
	conn := &nopWsConn{}

	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_subscribe","params":["newHeads"]}`)

	for i := 0; i < maxWSSubscriptionsPerConn; i++ {
		resp, err := d.HandleWs(req, conn)
		require.NoError(t, err)
		require.NotContains(t, string(resp), "error", "subscription %d", i)
	}

	resp, err := d.HandleWs(req, conn)
	require.NoError(t, err)
	require.Contains(t, string(resp), "too many subscriptions")
}

// nopWsConn is a WS connection that accepts and discards every message.
type nopWsConn struct{ filterID string }

func (c *nopWsConn) WriteMessage(int, []byte) error { return nil }
func (c *nopWsConn) GetFilterID() string            { return c.filterID }
func (c *nopWsConn) SetFilterID(id string)          { c.filterID = id }
