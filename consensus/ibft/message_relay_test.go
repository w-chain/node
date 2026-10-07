package ibft

import (
	"testing"

	gproto "google.golang.org/protobuf/proto"

	"github.com/0xPolygon/go-ibft/messages/proto"
	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/types"
)

// C-M1: forged consensus messages are no longer relayed, and the cheap
// checks run before the signature is recovered.
func TestRelayCheck(t *testing.T) {
	t.Parallel()

	const head = uint64(1000)

	validator := types.StringToAddress("0x1")
	msgAt := func(height uint64, from types.Address) *proto.Message {
		m := prepareMsg()
		m.View = &proto.View{Height: height}
		m.From = from.Bytes()

		return m
	}

	var verified int

	isValidatorAt := func(addr types.Address, _ uint64) bool { return addr == validator }
	verify := func(ok bool) func(*proto.Message) bool {
		return func(*proto.Message) bool { verified++; return ok }
	}

	// Honest: in window, a validator, good signature.
	require.True(t, relayCheck(msgAt(head+1, validator), head, isValidatorAt, verify(true)))

	verified = 0

	// Forged sender (the audit's case): rejected without an ecrecover.
	require.False(t, relayCheck(msgAt(head+1, types.StringToAddress("0xbad")), head, isValidatorAt, verify(true)))
	// Far outside the window: rejected without an ecrecover or validator lookup.
	require.False(t, relayCheck(msgAt(head+500, validator), head, isValidatorAt, verify(true)))
	// Malformed.
	require.False(t, relayCheck(&proto.Message{}, head, isValidatorAt, verify(true)))
	require.Equal(t, 0, verified, "cheap checks come before the signature")

	// A validator's address with a bad signature.
	require.False(t, relayCheck(msgAt(head+1, validator), head, isValidatorAt, verify(false)))
	require.Equal(t, 1, verified)
}

// C-M3: one validator cannot make go-ibft hold unbounded future messages.
func TestStoredMessageBudget(t *testing.T) {
	t.Parallel()

	const (
		head = uint64(1000)
		mib  = 1 << 20
	)

	var b storedMessageBudget

	attacker, honest := []byte("attacker"), []byte("honest")

	// The attacker fills its budget across future heights and rounds...
	stored := 0
	for h := head + 1; h <= head+17; h++ {
		for i := 0; i < 33*4; i++ {
			if b.allow(attacker, h, head, mib) {
				stored++
			}
		}
	}

	require.Equal(t, maxStoredBytesPerSender/mib, stored, "capped at the per-sender budget")

	// ...without affecting anyone else: an honest long halt (33 rounds of a
	// full-block round change plus small votes) is accepted in full.
	for round := 0; round <= 32; round++ {
		require.True(t, b.allow(honest, head+1, head, mib), "round change %d", round)
		require.True(t, b.allow(honest, head+1, head, 200), "prepare %d", round)
		require.True(t, b.allow(honest, head+1, head, 200), "commit %d", round)
	}

	// Late messages for finished heights are free: go-ibft drops them itself.
	require.True(t, b.allow(attacker, head, head, mib))

	// Once the chain passes those heights, go-ibft has pruned them and the
	// budget is freed.
	require.True(t, b.allow(attacker, head+18, head+17, mib))
}

// Copies of one validator's message for one view (replays, other signature
// encodings, relayers' variants) must not use up its budget: go-ibft holds one
// message per sender, height, round and type, so that is what is charged.
// Otherwise any peer could have the validator's real votes dropped (audit
// review C2).
func TestStoredMessageBudget_ChargedPerView(t *testing.T) {
	t.Parallel()

	const head = uint64(1000)

	var b storedMessageBudget

	msg := func(typ proto.MessageType, round uint64, sig byte, size int) *proto.Message {
		return &proto.Message{
			From: []byte("v"), View: &proto.View{Height: head + 1, Round: round}, Type: typ,
			Signature: []byte{sig},
			Payload: &proto.Message_RoundChangeData{RoundChangeData: &proto.RoundChangeMessage{
				LastPreparedProposal: &proto.Proposal{RawProposal: make([]byte, size)},
			}},
		}
	}

	// A 1 MiB round change, copied far past the budget under different
	// signature bytes, and grown copies of it: charged once, at its largest.
	for i := 0; i < 2*maxStoredBytesPerSender/(1<<20); i++ {
		require.True(t, b.allowMessage(msg(proto.MessageType_ROUND_CHANGE, 1, byte(i), 1<<20), head), "copy %d", i)
	}

	require.True(t, b.allowMessage(msg(proto.MessageType_ROUND_CHANGE, 1, 0, 3<<20), head))
	require.Equal(t, gproto.Size(msg(proto.MessageType_ROUND_CHANGE, 1, 0, 3<<20)), b.used[head+1]["v"])

	// The validator's real votes still get through.
	require.True(t, b.allowMessage(msg(proto.MessageType_PREPARE, 1, 0, 200), head))
	require.True(t, b.allowMessage(msg(proto.MessageType_COMMIT, 1, 0, 200), head))

	// Messages for distinct views are still charged and capped.
	stored := 0
	for r := uint64(2); r < 200; r++ {
		if b.allowMessage(msg(proto.MessageType_ROUND_CHANGE, r, 0, 1<<20), head) {
			stored++
		}
	}

	require.Less(t, stored, 198)
	require.LessOrEqual(t, stored, maxStoredBytesPerSender/(1<<20))
}
