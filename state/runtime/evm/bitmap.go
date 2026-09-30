package evm

import "github.com/w-chain-team/node/helper/common"

const bitmapSize = 8

type bitmap struct {
	buf []byte

	// shared marks buf as borrowed from the jumpdest cache: read-only, and
	// dropped instead of zeroed on reset.
	shared bool
}

// useShared points the bitmap at a cached analysis without copying it.
func (b *bitmap) useShared(buf []byte) {
	b.buf = buf
	b.shared = true
}

func (b *bitmap) isSet(i uint64) bool {
	return b.buf[i/bitmapSize]&(1<<(i%bitmapSize)) != 0
}

func (b *bitmap) set(i uint64) {
	b.buf[i/bitmapSize] |= 1 << (i % bitmapSize)
}

func (b *bitmap) reset() {
	if b.shared {
		b.buf, b.shared = nil, false

		return
	}

	for i := range b.buf {
		b.buf[i] = 0
	}

	b.buf = b.buf[:0]
}

func (b *bitmap) setCode(code []byte) {
	if b.shared {
		b.buf, b.shared = nil, false
	}

	codeSize := len(code)
	b.buf = common.ExtendByteSlice(b.buf, codeSize/bitmapSize+1)

	for i := 0; i < codeSize; {
		c := code[i]

		if isPushOp(c) {
			// push op
			i += int(c) - 0x60 + 2
		} else {
			if c == JUMPDEST {
				// jumpdest
				b.set(uint64(i))
			}
			i++
		}
	}
}

func isPushOp(i byte) bool {
	// From PUSH1 (0x60) to PUSH32(0x7F)
	return i>>5 == 3
}
