package ibft

import (
	"testing"

	"github.com/0xPolygon/go-ibft/messages/proto"
	"github.com/stretchr/testify/require"
	gproto "google.golang.org/protobuf/proto"
)

func TestIsWithinMessageWindow(t *testing.T) {
	t.Parallel()

	const head = uint64(1000) // building 1001

	cases := []struct {
		name   string
		height uint64
		round  uint64
		want   bool
	}{
		{"height being built, round 0", head + 1, 0, true},
		{"late message for the block just committed", head, 0, true},
		{"a few blocks ahead (node slightly behind)", head + 5, 0, true},
		{"furthest accepted future height", head + 1 + maxFutureHeights, 0, true},
		{"highest accepted round", head + 1, maxRound, true},

		// M4: arbitrary heights force a validator-set EVM lookup per message.
		{"already finished height", head - 1, 0, false},
		{"ancient height (validator-set cache miss)", 1, 0, false},
		{"just past the future window", head + 2 + maxFutureHeights, 0, false},
		{"absurd future height", 999_999_999_999, 0, false},
		{"round no honest node reaches", head + 1, maxRound + 1, false},
	}

	for _, tc := range cases {
		require.Equal(t, tc.want,
			isWithinMessageWindow(&proto.View{Height: tc.height, Round: tc.round}, head), tc.name)
	}

	require.False(t, isWithinMessageWindow(nil, head), "nil view")
}

// The window must stay well inside one epoch, so every height it accepts
// resolves to a validator set already in the 3-epoch cache (testnet epochs are
// 600 blocks, mainnet 1800).
func TestMessageWindowSmallerThanEpoch(t *testing.T) {
	t.Parallel()

	require.Less(t, maxFutureHeights+1, 600)
}

func TestIsWellFormedIBFTMessage(t *testing.T) {
	t.Parallel()

	encode := func(m *proto.Message) []byte {
		b, err := gproto.Marshal(m)
		require.NoError(t, err)

		return b
	}

	require.True(t, isWellFormedIBFTMessage(encode(prepareMsg())), "honest prepare is relayed")
	require.True(t, isWellFormedIBFTMessage(encode(preprepareMsg(nil))), "honest preprepare is relayed")

	// The C1 attack message (View == nil) must not even be relayed.
	c1 := &proto.Message{From: testFrom, Signature: testSig, Type: proto.MessageType_PREPREPARE}
	require.False(t, isWellFormedIBFTMessage(encode(c1)), "C1 message is dropped at the gossip layer")

	require.False(t, isWellFormedIBFTMessage([]byte{0xff, 0xff, 0xff}), "garbage bytes")
}
