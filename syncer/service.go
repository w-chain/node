package syncer

import (
	"context"
	"errors"

	"github.com/w-chain-team/node/network/grpc"
	"github.com/w-chain-team/node/syncer/proto"
	"github.com/w-chain-team/node/types"
	"github.com/armon/go-metrics"
	"github.com/golang/protobuf/ptypes/empty"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrBlockNotFound = errors.New("block not found")
)

type syncPeerService struct {
	proto.UnimplementedSyncPeerServer

	blockchain Blockchain       // reference to the blockchain module
	network    Network          // reference to the network module
	stream     *grpc.GrpcStream // reference to the grpc stream
}

func NewSyncPeerService(
	network Network,
	blockchain Blockchain,
) SyncPeerService {
	return &syncPeerService{
		blockchain: blockchain,
		network:    network,
	}
}

// Start starts syncPeerService
func (s *syncPeerService) Start() {
	s.setupGRPCServer()
}

// Close closes syncPeerService
func (s *syncPeerService) Close() error {
	return s.stream.Close()
}

// setupGRPCServer setup GRPC server
func (s *syncPeerService) setupGRPCServer() {
	s.stream = grpc.NewGrpcStream()

	proto.RegisterSyncPeerServer(s.stream.GrpcServer(), s)
	s.stream.Serve()
	s.network.RegisterProtocol(syncerProto, s.stream)
}

// maxConcurrentGetBlocks bounds block streams this node serves at once.
const maxConcurrentGetBlocks = 32

var getBlocksSlots = make(chan struct{}, maxConcurrentGetBlocks)

// GetBlocks is a gRPC endpoint to return blocks from the specific height via stream
func (s *syncPeerService) GetBlocks(
	req *proto.GetBlocksRequest,
	stream proto.SyncPeer_GetBlocksServer,
) error {
	// Each GetBlocks stream walks the chain from its start block on its own
	// goroutine. Unbounded, peers could open thousands and exhaust memory
	// (audit SYN-H1). When all slots are busy, the requester syncs elsewhere.
	select {
	case getBlocksSlots <- struct{}{}:
		defer func() { <-getBlocksSlots }()
	default:
		return status.Error(codes.ResourceExhausted, "too many concurrent block streams")
	}

	// from to latest
	for i := req.From; i <= s.blockchain.Header().Number; i++ {
		block, ok := s.blockchain.GetBlockByNumber(i, true)
		if !ok {
			return ErrBlockNotFound
		}

		resp := toProtoBlock(block)
		metrics.SetGauge([]string{syncerMetrics, "egress_bytes"}, float32(len(resp.Block)))

		// if client closes stream, context.Canceled is given
		if err := stream.Send(resp); err != nil {
			break
		}
	}

	return nil
}

// GetStatus is a gRPC endpoint to return the latest block number as a node status
func (s *syncPeerService) GetStatus(
	ctx context.Context,
	req *empty.Empty,
) (*proto.SyncPeerStatus, error) {
	var number uint64
	if header := s.blockchain.Header(); header != nil {
		number = header.Number
	}

	return &proto.SyncPeerStatus{
		Number: number,
	}, nil
}

// toProtoBlock converts type.Block -> proto.Block
func toProtoBlock(block *types.Block) *proto.Block {
	return &proto.Block{
		Block: block.MarshalRLP(),
	}
}
