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
	from, _ := strconv.ParseUint(os.Getenv("WCHAIN_FI_FROM"), 10, 64)

	var (
		mu      sync.Mutex
		largest = map[proto.MessageType]int{}
	)

	if os.Getenv("WCHAIN_FI_TAMPER") == "1" {
		tamperOutgoingForTest = tamperCopies
	}

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
			m.View.Height%every == 0 && m.View.Height >= from && m.View.Round < below
	}
}

// tamperCopies returns changed copies of a PREPREPARE or ROUND_CHANGE that keep
// the original signature: the block swapped, and the certificate (or prepared
// certificate) cut. Logged so the run can count them.
func tamperCopies(m *proto.Message) []*proto.Message {
	var out []*proto.Message

	switch m.Type {
	case proto.MessageType_PREPREPARE:
		a := gproto.Clone(m).(*proto.Message)
		a.GetPreprepareData().Proposal.RawProposal = []byte("not-the-signed-block")
		out = append(out, a)

		if c := m.GetPreprepareData().GetCertificate(); c != nil && len(c.RoundChangeMessages) > 1 {
			b := gproto.Clone(m).(*proto.Message)
			b.GetPreprepareData().Certificate.RoundChangeMessages = b.GetPreprepareData().Certificate.RoundChangeMessages[:1]
			out = append(out, b)
		}
	case proto.MessageType_ROUND_CHANGE:
		if pc := m.GetRoundChangeData().GetLatestPreparedCertificate(); pc != nil && len(pc.PrepareMessages) > 1 {
			a := gproto.Clone(m).(*proto.Message)
			a.GetRoundChangeData().LatestPreparedCertificate.PrepareMessages = pc.PrepareMessages[:1]
			out = append(out, a)

			b := gproto.Clone(m).(*proto.Message)
			b.GetRoundChangeData().LastPreparedProposal.RawProposal = []byte("not-the-signed-block")
			out = append(out, b)
		}
	}

	if len(out) > 0 {
		fmt.Fprintf(os.Stderr, "FAULTINJECT tamper %s height %d round %d copies %d\n",
			m.Type, m.GetView().GetHeight(), m.GetView().GetRound(), len(out))
	}

	return out
}
