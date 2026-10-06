package ibft

import (
	"testing"

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

// Replaying a validator's own signed messages must not use up its budget:
// otherwise any peer could have that validator's real votes dropped.
func TestStoredMessageBudget_ReplaysAreFree(t *testing.T) {
	t.Parallel()

	const head = uint64(1000)

	var b storedMessageBudget

	msg := func(sig string, size int) *proto.Message {
		return &proto.Message{
			From: []byte("v"), View: &proto.View{Height: head + 1}, Type: proto.MessageType_ROUND_CHANGE,
			Signature: []byte(sig),
			Payload: &proto.Message_RoundChangeData{RoundChangeData: &proto.RoundChangeMessage{
				LastPreparedProposal: &proto.Proposal{RawProposal: make([]byte, size)},
			}},
		}
	}

	// A 1 MiB round change replayed far past the budget...
	for i := 0; i < 2*maxStoredBytesPerSender/(1<<20); i++ {
		require.True(t, b.allowMessage(msg("rc", 1<<20), head), "replay %d", i)
	}

	// ...also with its unsigned parts changed (same signature)...
	require.True(t, b.allowMessage(msg("rc", 2<<20), head))

	// ...still leaves the validator's next real messages accepted.
	require.True(t, b.allowMessage(msg("prepare", 200), head))
	require.True(t, b.allowMessage(msg("commit", 200), head))

	// Distinct signed messages are still charged and capped.
	stored := 0
	for i := 0; i < 200; i++ {
		if b.allowMessage(msg(string(rune('a'+i%26))+string(rune(i)), 1<<20), head) {
			stored++
		}
	}

	require.Less(t, stored, 200)
	require.LessOrEqual(t, stored, maxStoredBytesPerSender/(1<<20))
}
