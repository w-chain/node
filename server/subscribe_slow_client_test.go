package server

import (
	"testing"
	"time"

	"github.com/w-chain-team/node/blockchain"
	"github.com/w-chain-team/node/server/proto"
	"github.com/w-chain-team/node/types"
	"google.golang.org/grpc"
	empty "google.golang.org/protobuf/types/known/emptypb"
)

// blockingSubscribeStream models an unauthenticated gRPC Subscribe client that
// stops reading: its Send() blocks forever (as grpc-go's stream.Send does once
// the HTTP/2 flow-control window is exhausted by a non-reading client).
type blockingSubscribeStream struct {
	grpc.ServerStream
	firstSend chan struct{}
	release   chan struct{}
}

func (b *blockingSubscribeStream) Send(_ *proto.BlockchainEvent) error {
	select {
	case <-b.firstSend:
	default:
		close(b.firstSend)
	}
	<-b.release // never released: the client is not reading
	return nil
}

// G-C1: an operator Subscribe client that stops reading must not stop block
// import. This drives the real systemService.Subscribe handler.
//
// A client that opens Subscribe and stops reading leaves the handler blocked in
// stream.Send(), so it stops draining its blockchain subscription. After the
// subscription buffer (5) fills, the next block write (dispatchEvent ->
// eventStream.push, blocking send) blocks forever -> the node can no longer
// import/produce blocks (chain halt on a validator).
func TestSubscribe_SilentClientDoesNotHaltBlockWrites(t *testing.T) {
	headers := blockchain.NewTestHeaders(40)

	const start = 10
	bc := blockchain.NewTestBlockchain(t, headers[:start])
	defer bc.Close()

	svc := &systemService{server: &Server{blockchain: bc}}

	stream := &blockingSubscribeStream{
		firstSend: make(chan struct{}),
		release:   make(chan struct{}),
	}

	// Run the real handler. It calls bc.SubscribeEvents() and loops on
	// GetEvent()/Send().
	go func() { _ = svc.Subscribe(&empty.Empty{}, stream) }()

	// Wait until the handler has subscribed and is blocked in its first Send.
	select {
	case <-stream.firstSend:
	case <-time.After(3 * time.Second):
		// Nudge it by writing one block so GetEvent returns.
	}

	const writeTimeout = 3 * time.Second

	for i := start; i < len(headers); i++ {
		done := make(chan error, 1)
		go func(h *types.Header) {
			done <- bc.WriteHeadersWithBodies([]*types.Header{h})
		}(headers[i])

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("unexpected write error at height %d: %v", headers[i].Number, err)
			}
			t.Logf("block %d written", headers[i].Number)
		case <-time.After(writeTimeout):
			t.Fatalf("CHAIN HALT via real systemService.Subscribe: block %d write BLOCKED "+
				"FOREVER. An unauthenticated gRPC Subscribe client that stopped reading "+
				"holds the handler in stream.Send(); its subscription buffer (5) filled and "+
				"eventStream.push now blocks every block write.", headers[i].Number)
		}
	}
}
