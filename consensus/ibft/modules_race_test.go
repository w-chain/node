package ibft

import (
	"sync"
	"testing"

	"github.com/0xPolygon/go-ibft/messages/proto"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"

	"github.com/w-chain-team/node/consensus/ibft/fork"
	"github.com/w-chain-team/node/consensus/ibft/signer"
	"github.com/w-chain-team/node/crypto"
	"github.com/w-chain-team/node/validators"
)

// staticForkManager hands out one fixed set of modules for every height.
type staticForkManager struct {
	signer     signer.Signer
	validators validators.Validators
}

func (m *staticForkManager) Initialize() error                                     { return nil }
func (m *staticForkManager) Close() error                                          { return nil }
func (m *staticForkManager) GetSigner(uint64) (signer.Signer, error)               { return m.signer, nil }
func (m *staticForkManager) GetValidatorStore(uint64) (fork.ValidatorStore, error) { return nil, nil }
func (m *staticForkManager) GetValidators(uint64) (validators.Validators, error) {
	return m.validators, nil
}
func (m *staticForkManager) GetHooks(uint64) fork.HooksInterface { return nil }

// C-H1: the modules are swapped at every height while the gossip and
// consensus goroutines read them. Run with -race: the old code, three plain
// fields written without a lock, is reported as a data race here.
func TestUpdateCurrentModules_ConcurrentWithValidation(t *testing.T) {
	t.Parallel()

	key, err := crypto.GenerateECDSAKey()
	require.NoError(t, err)

	s := signer.NewSigner(signer.NewECDSAKeyManagerFromKey(key), nil)
	addr := crypto.PubKeyToAddress(&key.PublicKey)

	i := &backendIBFT{
		logger: hclog.NewNullLogger(),
		forkManager: &staticForkManager{
			signer:     s,
			validators: validators.NewECDSAValidatorSet(validators.NewECDSAValidator(addr)),
		},
	}
	require.NoError(t, i.updateCurrentModules(1))

	msg := &proto.Message{
		View: &proto.View{Height: 5}, From: addr.Bytes(), Type: proto.MessageType_PREPARE,
		Payload: &proto.Message_PrepareData{PrepareData: &proto.PrepareMessage{ProposalHash: []byte{1}}},
	}
	raw, err := msg.PayloadNoSig()
	require.NoError(t, err)
	msg.Signature, err = s.SignIBFTMessage(raw)
	require.NoError(t, err)

	var (
		wg   sync.WaitGroup
		stop = make(chan struct{})
	)

	wg.Add(1)

	go func() {
		defer wg.Done()

		for h := uint64(2); ; h++ {
			select {
			case <-stop:
				return
			default:
				_ = i.updateCurrentModules(h)
			}
		}
	}()

	for n := 0; n < 2000; n++ {
		require.True(t, i.IsValidValidator(msg), "a valid message must never be rejected mid-swap")
		require.Equal(t, addr.Bytes(), i.ID())
		require.True(t, i.isActiveValidator())
	}

	close(stop)
	wg.Wait()
}
