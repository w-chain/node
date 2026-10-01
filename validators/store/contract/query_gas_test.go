package contract

import (
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/umbracle/ethgo/abi"
	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/contracts/staking"
	"github.com/w-chain-team/node/crypto"
	"github.com/w-chain-team/node/helper/hex"
	"github.com/w-chain-team/node/state"
	itrie "github.com/w-chain-team/node/state/immutable-trie"
	"github.com/w-chain-team/node/types"
)

// W Chain Staking.sol (flattened), solc 0.8.22, --optimize-runs 200, evm london.
const stakingArtifact = "testdata/staking.bin"

// IB-H1: deploys the real Staking.sol, stakes N validators with real BLS keys,
// then runs the node's own FetchBLSValidators. With the old 1M query gas the
// key query ran out of gas at ~150 validators (contract max 499): every node
// would fail the next epoch's validator set and the chain would halt.
func TestFetchBLSValidators_UpToContractMax(t *testing.T) {
	raw, err := os.ReadFile(stakingArtifact)
	if err != nil {
		t.Fatal(err)
	}

	creation, err := hex.DecodeHex(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}

	orig := staking.AddrStakingContract
	defer func() { staking.AddrStakingContract = orig }()

	for _, n := range []int{12, 160, 499} {
		tr := newQueryTransition(t)
		tr.SetNonPayable(true)

		admin := types.StringToAddress("0xad00000000000000000000000000000000000001")
		tr.Txn().AddBalance(admin, new(big.Int).Lsh(big.NewInt(1), 200))

		adminArg := make([]byte, 32)
		copy(adminArg[12:], admin.Bytes())

		res, err := tr.Apply(&types.Transaction{
			From: admin, Input: append(append([]byte{}, creation...), adminArg...),
			Gas: 9_000_000, GasPrice: big.NewInt(0), Value: big.NewInt(0), Nonce: tr.GetNonce(admin),
		})
		if err != nil || res.Failed() {
			t.Fatalf("deploy failed: %v %v", err, res)
		}

		staking.AddrStakingContract = res.Address
		if len(tr.Txn().GetCode(res.Address)) == 0 {
			t.Fatalf("no code at %s", res.Address)
		}

		stakeSel := crypto.Keccak256([]byte("stake()"))[:4]
		regM, _ := abi.NewMethod("function registerBLSPublicKey(bytes blsPubKey)")
		minStake, _ := new(big.Int).SetString("10000000000000000000000000", 10)

		for i := 0; i < n; i++ {
			sk, _ := crypto.GenerateBLSKey()
			pk, _ := sk.GetPublicKey()
			pkb, _ := pk.MarshalBinary()

			key, _ := crypto.GenerateECDSAKey()
			addr := crypto.PubKeyToAddress(&key.PublicKey)
			tr.Txn().AddBalance(addr, new(big.Int).Mul(minStake, big.NewInt(2)))

			to := staking.AddrStakingContract
			res, err := tr.Apply(&types.Transaction{From: addr, To: &to, Input: stakeSel, Gas: 1_000_000,
				GasPrice: big.NewInt(0), Value: minStake, Nonce: tr.GetNonce(addr)})
			if err != nil || res.Failed() {
				t.Fatalf("stake %d failed: %v %v", i, err, res)
			}

			in, _ := regM.Encode(map[string]interface{}{"blsPubKey": pkb})
			res, err = tr.Apply(&types.Transaction{From: addr, To: &to, Input: in, Gas: 1_000_000,
				GasPrice: big.NewInt(0), Value: big.NewInt(0), Nonce: tr.GetNonce(addr)})
			if err != nil || res.Failed() {
				t.Fatalf("register %d failed: %v %v", i, err, res)
			}
		}

		vals, err := FetchBLSValidators(tr, types.ZeroAddress)
		if err != nil {
			t.Fatalf("N=%d validators: FetchBLSValidators: %v", n, err)
		}

		if vals.Len() != n {
			t.Fatalf("N=%d validators: got %d", n, vals.Len())
		}
	}
}

func newQueryTransition(t *testing.T) *state.Transition {
	t.Helper()

	st := itrie.NewState(itrie.NewMemoryStorage())
	ex := state.NewExecutor(&chain.Params{
		Forks:        chain.AllForksEnabled,
		BurnContract: map[uint64]types.Address{0: types.ZeroAddress},
	}, st, hclog.NewNullLogger())

	rootHash, err := ex.WriteGenesis(nil, types.Hash{})
	if err != nil {
		t.Fatal(err)
	}

	ex.GetHash = func(h *types.Header) state.GetHashByNumber {
		return func(i uint64) types.Hash { return rootHash }
	}

	tr, err := ex.BeginTxn(rootHash, &types.Header{GasLimit: 1 << 62} /* setup stakes 499 validators in this transition */, types.ZeroAddress)
	if err != nil {
		t.Fatal(err)
	}

	return tr
}
