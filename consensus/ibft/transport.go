package ibft

import (
	"github.com/0xPolygon/go-ibft/messages/proto"
	"github.com/w-chain-team/node/network"
	"github.com/w-chain-team/node/types"
	"github.com/libp2p/go-libp2p/core/peer"
	gproto "google.golang.org/protobuf/proto"
)

type transport interface {
	Multicast(msg *proto.Message) error
}

type gossipTransport struct {
	topic *network.Topic
}

func (g *gossipTransport) Multicast(msg *proto.Message) error {
	return g.topic.Publish(msg)
}

// dropOutgoingForTest lets local fault-injection builds (build tag
// faultinject, see fault_inject.go) drop chosen outgoing consensus messages
// to force failed rounds. It is always nil in release builds.
var dropOutgoingForTest func(*proto.Message) bool

// tamperOutgoingForTest lets local fault-injection builds also publish changed
// copies of an outgoing message, as a peer re-gossiping it would. Always nil
// in release builds.
var tamperOutgoingForTest func(*proto.Message) []*proto.Message

func (i *backendIBFT) Multicast(msg *proto.Message) {
	if dropOutgoingForTest != nil && dropOutgoingForTest(msg) {
		return
	}

	if err := i.transport.Multicast(msg); err != nil {
		i.logger.Error("fail to gossip", "err", err)
	}

	if tamperOutgoingForTest != nil {
		for _, bad := range tamperOutgoingForTest(msg) {
			_ = i.transport.Multicast(bad)
		}
	}
}

// setupTransport sets up the gossip transport protocol
func (i *backendIBFT) setupTransport() error {
	// Define a new topic
	topic, err := i.network.NewTopic(ibftProto, &proto.Message{})
	if err != nil {
		return err
	}

	// Malformed, out-of-window, non-validator or badly signed consensus
	// messages are dropped before they are processed here or relayed to
	// peers, so they no longer fan out across the whole mesh (audits M4, C-M1).
	if err := i.network.RegisterTopicValidator(ibftProto, i.isRelayableIBFTMessage); err != nil {
		return err
	}

	// Subscribe to the newly created topic
	if err := topic.Subscribe(
		func(obj interface{}, _ peer.ID) {
			if !i.isActiveValidator() {
				return
			}

			msg, ok := obj.(*proto.Message)
			if !ok {
				i.logger.Error("invalid type assertion for message request")

				return
			}

			if err := validateIBFTMessage(msg); err != nil {
				i.logger.Debug("dropping malformed consensus message", "err", err)

				return
			}

			head := i.blockchain.Header().Number

			if !isWithinMessageWindow(msg.View, head) {
				i.logger.Debug("dropping consensus message outside height/round window",
					"height", msg.View.Height, "round", msg.View.Round)

				return
			}

			if !i.storedMessages.allowMessage(msg, head) {
				i.logger.Debug("dropping consensus message: sender over its stored-message budget",
					"addr", types.BytesToAddress(msg.From), "height", msg.View.Height)

				return
			}

			i.consensus.AddMessage(msg)

			i.logger.Debug(
				"validator message received",
				"type", msg.Type.String(),
				"height", msg.GetView().GetHeight(),
				"round", msg.GetView().GetRound(),
				"addr", types.BytesToAddress(msg.From).String(),
			)
		},
	); err != nil {
		return err
	}

	i.transport = &gossipTransport{topic: topic}

	return nil
}

// isWellFormedIBFTMessage is the gossip validator for the consensus topic.
func isWellFormedIBFTMessage(data []byte) bool {
	msg := &proto.Message{}
	if err := gproto.Unmarshal(data, msg); err != nil {
		return false
	}

	return validateIBFTMessage(msg) == nil
}
