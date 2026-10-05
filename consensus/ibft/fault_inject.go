//go:build faultinject

package ibft

import (
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/0xPolygon/go-ibft/messages/proto"
	gproto "google.golang.org/protobuf/proto"
)

// Local test builds only (go build -tags faultinject). With
// WCHAIN_FI_EVERY=N and WCHAIN_FI_COMMIT_ROUNDS_BELOW=R, every validator drops
// its own COMMIT messages for rounds below R at every height divisible by N:
// those rounds fail after the block was prepared, so the next round must
// re-propose the prepared block through a round change certificate.
func init() {
	every, _ := strconv.ParseUint(os.Getenv("WCHAIN_FI_EVERY"), 10, 64)
	below, _ := strconv.ParseUint(os.Getenv("WCHAIN_FI_COMMIT_ROUNDS_BELOW"), 10, 64)

	var (
		mu      sync.Mutex
		largest = map[proto.MessageType]int{}
	)

	dropOutgoingForTest = func(m *proto.Message) bool {
		// Report each new largest outgoing message per type.
		size := gproto.Size(m)

		mu.Lock()
		if size > largest[m.Type] {
			largest[m.Type] = size
			fmt.Fprintf(os.Stderr, "FAULTINJECT largest %s %d bytes at height %d round %d\n",
				m.Type, size, m.GetView().GetHeight(), m.GetView().GetRound())
		}
		mu.Unlock()

		return every != 0 && below != 0 && m.Type == proto.MessageType_COMMIT && m.View != nil &&
			m.View.Height%every == 0 && m.View.Round < below
	}
}
