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

func (i *backendIBFT) Multicast(msg *proto.Message) {
	if err := i.transport.Multicast(msg); err != nil {
		i.logger.Error("fail to gossip", "err", err)
	}
}

// setupTransport sets up the gossip transport protocol
func (i *backendIBFT) setupTransport() error {
	// Define a new topic
	topic, err := i.network.NewTopic(ibftProto, &proto.Message{})
	if err != nil {
		return err
	}

	// Structurally malformed consensus messages are never honest: drop them
	// before they are processed here or relayed to peers, so one bad message
	// no longer fans out across the whole mesh (audit M4). Only the structural
	// check runs here — not the height window, which depends on this node's
	// own head and must not stop a lagging node from relaying current traffic.
	if err := i.network.RegisterTopicValidator(ibftProto, isWellFormedIBFTMessage); err != nil {
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

			if !isWithinMessageWindow(msg.View, i.blockchain.Header().Number) {
				i.logger.Debug("dropping consensus message outside height/round window",
					"height", msg.View.Height, "round", msg.View.Round)

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
