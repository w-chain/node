package types

import "errors"

const (
	// MaxRawTxSize bounds an encoded transaction received from the network
	// before it is decoded. The txpool accepts at most 128 KB, so twice that
	// never turns away a transaction the pool would take.
	MaxRawTxSize = 256 * 1024

	// minBytesPerRLPItem is the lowest average item size accepted from the
	// network. Real transactions and blocks average well above 7 bytes per
	// item (a legacy tx is ~11, a bare access-list entry ~8); a list of one-byte
	// items is how the decoder was made to allocate ~490x its input (audit
	// P2-H1), since each item costs it far more memory than its one byte.
	minBytesPerRLPItem = 4

	// rlpItemAllowance lets small inputs through regardless of density.
	rlpItemAllowance = 1024
)

var (
	ErrRawTxTooLarge   = errors.New("transaction is too large")
	ErrRLPTooManyItems = errors.New("rlp input has too many items for its size")
)

// CheckRLPDensity rejects input with more RLP items than its size can hold
// honestly, before it reaches the decoder. It only counts item headers and
// never allocates. Malformed input is left to the decoder to reject.
func CheckRLPDensity(b []byte) error {
	limit := len(b)/minBytesPerRLPItem + rlpItemAllowance

	items := 0

	for pos := 0; pos < len(b); {
		items++
		if items > limit {
			return ErrRLPTooManyItems
		}

		prefix := b[pos]

		switch {
		case prefix < 0x80: // single byte
			pos++
		case prefix <= 0xb7: // short string
			pos += 1 + int(prefix-0x80)
		case prefix <= 0xbf: // long string
			size, ok := rlpLength(b, pos, int(prefix-0xb7))
			if !ok {
				return nil
			}

			pos += 1 + int(prefix-0xb7) + size
		case prefix <= 0xf7: // short list: step inside
			pos++
		default: // long list: step inside
			pos += 1 + int(prefix-0xf7)
		}
	}

	return nil
}

// rlpLength reads a big-endian length of n bytes following b[pos].
func rlpLength(b []byte, pos, n int) (int, bool) {
	if n > 4 || pos+1+n > len(b) {
		return 0, false
	}

	size := 0
	for _, c := range b[pos+1 : pos+1+n] {
		size = size<<8 | int(c)
	}

	return size, true
}

// CheckRawTx applies the pre-decode limits to a transaction from the network.
func CheckRawTx(b []byte) error {
	if len(b) > MaxRawTxSize {
		return ErrRawTxTooLarge
	}

	return CheckRLPDensity(b)
}
