package evm

import (
	lru "github.com/hashicorp/golang-lru"

	"github.com/w-chain-team/node/state/runtime"
	"github.com/w-chain-team/node/types"
)

// jumpdestCacheSize bounds the cache: an analysis is at most 24KB/8 = 3KB, so
// the cache stays under ~12MB.
const jumpdestCacheSize = 4096

// jumpdestCache maps a contract's code hash to its JUMPDEST analysis. The
// analysis walks the whole code, so re-doing it on every call let a contract
// that repeatedly calls a 24KB code cost ~1s of CPU per 20M-gas block (audit
// E-M1). The result depends only on the code, so reusing it cannot change
// execution.
var jumpdestCache, _ = lru.New(jumpdestCacheSize)

// loadJumpdests fills b for the contract's code, from the cache when the code
// is stored at an address (every call; never init code, which has no hash).
func loadJumpdests(b *bitmap, c *runtime.Contract, host runtime.Host) {
	if c.Type == runtime.Create || c.Type == runtime.Create2 || len(c.Code) == 0 {
		b.setCode(c.Code)

		return
	}

	codeHash := host.GetCodeHash(c.CodeAddress)
	if codeHash == types.ZeroHash || codeHash == types.EmptyCodeHash {
		b.setCode(c.Code)

		return
	}

	if cached, ok := jumpdestCache.Get(codeHash); ok {
		if buf, ok := cached.([]byte); ok && len(buf) == len(c.Code)/bitmapSize+1 {
			b.useShared(buf)

			return
		}
	}

	b.setCode(c.Code)
	jumpdestCache.Add(codeHash, append([]byte(nil), b.buf...))
}
