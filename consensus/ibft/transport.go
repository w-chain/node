package ibft

import (
	"github.com/0xPolygon/go-ibft/messages/proto"
	"github.com/w-chain-team/node/network"
	"github.com/w-chain-team/node/types"
	"github.com/libp2p/go-libp2p/core/peer"
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
