package jsonrpc

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/w-chain-team/node/types"
)

func newGuardTestDispatcher(t *testing.T) *Dispatcher {
	t.Helper()

	return newTestDispatcher(t,
		hclog.NewNullLogger(),
		newMockStore(),
		&dispatcherParams{
			jsonRPCBatchLengthLimit: 20,
			blockRangeLimit:         1000,
		},
	)
}

// C2: eth_newFilter [null] used to store a nil query that panicked on the next log.
func TestCrashGuard_NewFilterNullIsRejected(t *testing.T) {
	t.Parallel()

	d := newGuardTestDispatcher(t)

	for _, method := range []string{"eth_newFilter", "eth_getLogs"} {
		resp, err := d.Handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":[null]}`))
		require.NoError(t, err)
		require.Contains(t, string(resp), `"error"`, method)
		require.Contains(t, string(resp), ErrNilLogQuery.Error(), method)
	}
}

func TestCrashGuard_NilLogQueryMatchesNothing(t *testing.T) {
	t.Parallel()

	var q *LogQuery

	require.False(t, q.Match(&types.Log{}))
	require.False(t, (&LogQuery{}).Match(nil))
}

// C3: eth_subscribe ["logs"] without a filter used to index params[1] out of range.
func TestCrashGuard_SubscribeLogsWithoutFilter(t *testing.T) {
	t.Parallel()

	d := newGuardTestDispatcher(t)

	for _, params := range []string{`["logs"]`, `["logs", null]`} {
		conn, _ := newMockWsConnWithMsgCh()

		resp, err := d.HandleWs([]byte(`{"jsonrpc":"2.0","id":1,"method":"eth_subscribe","params":`+params+`}`), conn)
		require.NoError(t, err)
		require.Contains(t, string(resp), "logs subscription requires a filter object", params)
	}
}

func TestCrashGuard_SafeDispatchRecoversPanic(t *testing.T) {
	t.Parallel()

	f := &FilterManager{}

	// A nil event with a nil store panics inside dispatchEvent.
	require.NotPanics(t, func() {
		_ = f.safeDispatchEvent(nil)
	})
}

// H5: request bodies above the limit are refused instead of read into memory.
func TestCrashGuard_RequestBodyLimit(t *testing.T) {
	t.Parallel()

	j := &JSONRPC{logger: hclog.NewNullLogger(), dispatcher: newGuardTestDispatcher(t)}

	big := bytes.Repeat([]byte("a"), maxRequestBodySize+1)
	rec := httptest.NewRecorder()
	j.handleJSONRPCRequest(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(big)))
	require.Contains(t, rec.Body.String(), "request body too large")

	rec = httptest.NewRecorder()
	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"web3_clientVersion","params":[]}`)
	j.handleJSONRPCRequest(rec, httptest.NewRequest(http.MethodPost, "/", body))
	require.Contains(t, rec.Body.String(), `"result"`)
}
