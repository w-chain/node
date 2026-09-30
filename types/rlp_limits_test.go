package types

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

// smallestTx is about as small, per RLP item, as a real transaction gets.
func smallestTx(typ TxType, to *Address) *Transaction {
	return &Transaction{
		Type:      typ,
		ChainID:   big.NewInt(71117),
		GasPrice:  big.NewInt(800_000_000_000),
		GasTipCap: big.NewInt(0),
		GasFeeCap: big.NewInt(800_000_000_000),
		Gas:       21_000,
		To:        to,
		Value:     big.NewInt(0),
		V:         big.NewInt(1),
		R:         new(big.Int).Lsh(big.NewInt(1), 255),
		S:         new(big.Int).Lsh(big.NewInt(1), 254),
	}
}

func rlpFlatList(n int, elem byte) []byte {
	return append([]byte{0xfb, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}, bytes.Repeat([]byte{elem}, n)...)
}

// P2-H1: honest data always passes, however it is shaped.
func TestCheckRLPDensity_HonestData(t *testing.T) {
	t.Parallel()

	to := StringToAddress("0x1")

	var txs []*Transaction

	for i := 0; i < 5000; i++ {
		for _, typ := range []TxType{LegacyTx, DynamicFeeTx} {
			txs = append(txs, smallestTx(typ, &to), smallestTx(typ, nil))
		}
	}

	for _, tx := range txs[:4] {
		require.NoError(t, CheckRawTx(tx.MarshalRLP()))
	}

	block := &Block{
		Header:       &Header{Number: 1, ExtraData: make([]byte, 600), BaseFee: 800_000_000_000},
		Transactions: txs,
	}
	enc := block.MarshalRLP()

	require.NoError(t, CheckRLPDensity(enc), "a block full of minimal txs (%d bytes)", len(enc))
	require.NoError(t, (&Block{}).UnmarshalRLP(enc))

	require.NoError(t, CheckRLPDensity((&Block{Header: &Header{}}).MarshalRLP()), "empty block")
	require.NoError(t, CheckRLPDensity(nil))
}

// P2-H1: dense inputs that made the decoder allocate ~490x are refused first.
func TestCheckRLPDensity_RejectsAmplification(t *testing.T) {
	t.Parallel()

	oneMiB := 1<<20 - 16

	require.ErrorIs(t, CheckRLPDensity(rlpFlatList(oneMiB, 0x01)), ErrRLPTooManyItems, "flat single bytes")
	require.ErrorIs(t, CheckRLPDensity(rlpFlatList(oneMiB, 0x80)), ErrRLPTooManyItems, "flat empty strings")
	require.ErrorIs(t, CheckRLPDensity(rlpFlatList(oneMiB, 0xc0)), ErrRLPTooManyItems, "flat empty lists")

	nested := append(bytes.Repeat([]byte{0xc1}, 100_000), 0xc0)
	require.ErrorIs(t, CheckRLPDensity(nested), ErrRLPTooManyItems, "deeply nested lists")

	require.ErrorIs(t, CheckRawTx(make([]byte, MaxRawTxSize+1)), ErrRawTxTooLarge)

	// Truncated or malformed input is left for the decoder to reject.
	require.NoError(t, CheckRLPDensity([]byte{0xbf, 0xff}))
	require.NoError(t, CheckRLPDensity([]byte{0xb8}))
}
