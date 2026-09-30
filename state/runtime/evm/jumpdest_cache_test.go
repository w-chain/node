package evm

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/state/runtime"
	"github.com/w-chain-team/node/types"
)

type codeHashHost struct {
	mockHost
	hash types.Hash
}

func (h *codeHashHost) GetCodeHash(types.Address) types.Hash { return h.hash }

// E-M1: a cached analysis must equal a fresh one, and a pooled state that
// borrowed it must never zero the shared copy on release.
func TestJumpdestCache_SameResultAndNotClobbered(t *testing.T) {
	// JUMPDEST at 0, PUSH2 0x5b5b (not jumpdests), JUMPDEST at 4, ...
	code := append([]byte{0x5b, 0x61, 0x5b, 0x5b, 0x5b}, bytes.Repeat([]byte{0x5b, 0x60, 0x5b}, 2000)...)
	host := &codeHashHost{hash: types.StringToHash("0xc0de")}
	c := &runtime.Contract{Type: runtime.Call, Code: code, CodeAddress: types.StringToAddress("0x1")}

	fresh := &bitmap{}
	fresh.setCode(code)

	for i := 0; i < 3; i++ {
		b := &bitmap{}
		loadJumpdests(b, c, host)
		require.Equal(t, fresh.buf, b.buf, "run %d", i)

		b.reset() // what releaseState does
	}

	cached, ok := jumpdestCache.Get(host.hash)
	require.True(t, ok)
	require.Equal(t, fresh.buf, cached, "releasing a state must not zero the cached analysis")
}

// Init code has no stored hash and is never cached.
func TestJumpdestCache_CreateNotCached(t *testing.T) {
	host := &codeHashHost{hash: types.StringToHash("0xc1ea7e")}
	b := &bitmap{}
	loadJumpdests(b, &runtime.Contract{Type: runtime.Create, Code: []byte{0x5b}}, host)

	_, ok := jumpdestCache.Get(host.hash)
	require.False(t, ok)
	require.True(t, b.isSet(0))
}
