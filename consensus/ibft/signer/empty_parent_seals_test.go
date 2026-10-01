package signer

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/fastrlp"

	"github.com/w-chain-team/node/types"
)

// emptyParentSealsExtra is an IBFT extra whose ParentCommittedSeals is an
// empty RLP list, which decodes to a seal with no bitmap.
func emptyParentSealsExtra() []byte {
	ar := &fastrlp.Arena{}
	vv := ar.NewArray()
	vv.Set(ar.NewArray()) // validators
	vv.Set(ar.NewNull())  // proposer seal
	vv.Set(ar.NewArray()) // committed seals
	vv.Set(ar.NewArray()) // parent committed seals: empty list

	return vv.MarshalTo(make([]byte, IstanbulExtraVanity))
}

// IB-C1: header hashing, block decoding and the parent-seal check all called
// Num() on that nil bitmap and crashed the node. One such block from a sync
// peer, or one proposal from a validator, took down every node that read it.
func TestEmptyParentCommittedSeals_NoPanic(t *testing.T) {
	km, _, _ := newTestBLSKeyManager(t)
	s := NewSigner(km, km)

	require.Equal(t, 0, (&AggregatedSeal{}).Num())

	h := &types.Header{Number: 5, ExtraData: emptyParentSealsExtra()}

	require.NotPanics(t, func() {
		_, err := s.CalculateHeaderHash(h)
		require.NoError(t, err)
	}, "header hash")

	UseIstanbulHeaderHashInTest(t, s)

	raw := (&types.Block{Header: &types.Header{Number: 5, ExtraData: emptyParentSealsExtra()}}).MarshalRLP()

	require.NotPanics(t, func() {
		_ = (&types.Block{}).UnmarshalRLP(raw)
	}, "block decode")

	require.NotPanics(t, func() {
		err := s.VerifyParentCommittedSeals(types.Hash{}, h, nil, 1, true)
		require.ErrorIs(t, err, ErrEmptyParentCommittedSeals)
	}, "parent seal check treats it as missing")
}
