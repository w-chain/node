package ibft

import (
	"testing"

	"github.com/0xPolygon/go-ibft/messages/proto"
	"github.com/hashicorp/go-hclog"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/consensus/ibft/signer"
	"github.com/w-chain-team/node/crypto"
	"github.com/w-chain-team/node/validators"
)

// W-M1: the watcher records only messages signed by a real validator.
func TestObserve_OnlyValidatorSignedMessages(t *testing.T) {
	t.Parallel()

	validatorKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)

	outsiderKey, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)

	validatorSigner := signer.NewSigner(signer.NewECDSAKeyManagerFromKey(validatorKey), nil)
	outsiderSigner := signer.NewSigner(signer.NewECDSAKeyManagerFromKey(outsiderKey), nil)
	validatorAddr := crypto.PubKeyToAddress(&validatorKey.PublicKey)
	outsiderAddr := crypto.PubKeyToAddress(&outsiderKey.PublicKey)

	var observed []*proto.Message

	i := &backendIBFT{
		logger: hclog.NewNullLogger(),
		forkManager: &staticForkManager{
			signer:     validatorSigner,
			validators: validators.NewECDSAValidatorSet(validators.NewECDSAValidator(validatorAddr)),
		},
		observer: func(msg *proto.Message, _ peer.ID) { observed = append(observed, msg) },
	}
	require.NoError(t, i.updateCurrentModules(1))

	signed := func(s signer.Signer, from []byte) *proto.Message {
		msg := &proto.Message{
			View: &proto.View{Height: 5}, From: from, Type: proto.MessageType_PREPARE,
			Payload: &proto.Message_PrepareData{PrepareData: &proto.PrepareMessage{ProposalHash: []byte{1}}},
		}

		raw, err := msg.PayloadNoSig()
		require.NoError(t, err)

		msg.Signature, err = s.SignIBFTMessage(raw)
		require.NoError(t, err)

		return msg
	}

	honest := signed(validatorSigner, validatorAddr.Bytes())

	forgedSender := signed(outsiderSigner, validatorAddr.Bytes()) // claims to be the validator
	notValidator := signed(outsiderSigner, outsiderAddr.Bytes())  // correctly signed, not in the set

	randomSig := signed(validatorSigner, validatorAddr.Bytes())
	randomSig.Signature = make([]byte, 65)

	for _, msg := range []*proto.Message{forgedSender, notValidator, randomSig, honest} {
		i.observe(msg, "")
	}

	require.Equal(t, []*proto.Message{honest}, observed)
}
