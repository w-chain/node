package precompiled

import (
	"math/big"

	"math"

	"github.com/w-chain-team/node/chain"
	"github.com/w-chain-team/node/state/runtime"
	"github.com/w-chain-team/node/types"
)

type modExp struct {
	p *Precompiled
}

var (
	big1      = big.NewInt(1)
	big2      = big.NewInt(2)
	big3      = big.NewInt(3)
	big7      = big.NewInt(7)
	big4      = big.NewInt(4)
	big8      = big.NewInt(8)
	big16     = big.NewInt(16)
	big32     = big.NewInt(32)
	big64     = big.NewInt(64)
	big96     = big.NewInt(96)
	big480    = big.NewInt(480)
	big1024   = big.NewInt(1024)
	big3072   = big.NewInt(3072)
	big199680 = big.NewInt(199680)
)

var (
	divisor = big.NewInt(20)
)

func adjustedExponentLength(expLen, head *big.Int) *big.Int {
	bitlength := uint64(0)
	if head.Sign() != 0 {
		bitlength = uint64(head.BitLen() - 1)
	}

	if expLen.Cmp(big32) <= 0 {
		// return the index of the highest bit
		return new(big.Int).SetUint64(bitlength)
	}

	head.Sub(expLen, big32)
	head.Mul(head, big8)
	head.Add(head, new(big.Int).SetUint64(bitlength))

	return head
}

func subMul(x, a, b, c *big.Int) *big.Int {
	// x ** 2 // a + b * x - c
	tmp := new(big.Int)

	// x ** 2 / a
	tmp.Mul(x, x)
	tmp.Div(tmp, a)

	// b * x - c
	x.Mul(x, b)
	x.Sub(x, c)

	return x.Add(x, tmp)
}

func multComplexity(x *big.Int) *big.Int {
	if x.Cmp(big64) <= 0 {
		// x ** x
		x.Mul(x, x)
	} else if x.Cmp(big1024) <= 0 {
		// x ** 2 // 4 + 96 * x - 3072
		x = subMul(x, big4, big96, big3072)
	} else {
		// x ** 2 // 16 + 480 * x - 199680
		x = subMul(x, big16, big480, big199680)
	}

	return x
}

func (m *modExp) gas(input []byte, config *chain.ForksInTime) uint64 {
	var val, tail []byte

	val, tail = m.p.get(input, 32)
	baseLen := new(big.Int).SetBytes(val)

	val, tail = m.p.get(tail, 32)
	expLen := new(big.Int).SetBytes(val)

	val, _ = m.p.get(tail, 32)
	modLen := new(big.Int).SetBytes(val)

	if len(input) > 96 {
		input = input[96:]
	} else {
		input = input[:0]
	}

	expHeadLen := uint64(32)
	if expLen.Cmp(big32) < 0 {
		expHeadLen = expLen.Uint64()
	}

	expHead := new(big.Int)

	if bLen := baseLen.Uint64(); bLen < uint64(len(input)) {
		val, _ = m.p.get(input[bLen:], int(expHeadLen))
		expHead.SetBytes(val)
	}

	// a := mult_complexity(max(length_of_MODULUS, length_of_BASE)
	gasCost := new(big.Int)
	if modLen.Cmp(baseLen) >= 0 {
		gasCost.Set(modLen)
	} else {
		gasCost.Set(baseLen)
	}

	if config != nil && config.WChainV110 {
		return modExpGasEIP7883(gasCost, expLen, expHead)
	}

	if config != nil && config.WChainV109 {
		return modExpGasEIP2565(gasCost, adjustedExponentLength(expLen, expHead))
	}

	gasCost = multComplexity(gasCost)

	// a = a * max(ADJUSTED_EXPONENT_LENGTH, 1)
	adjExpLen := adjustedExponentLength(expLen, expHead)
	if adjExpLen.Cmp(big1) >= 0 {
		gasCost.Mul(gasCost, adjExpLen)
	} else {
		gasCost.Mul(gasCost, big1)
	}

	// a = a / div
	gasCost.Div(gasCost, divisor)

	// cap to the max uint64
	if !gasCost.IsUint64() {
		return math.MaxUint64
	}

	return gasCost.Uint64()
}

// modExpGasEIP2565 prices modexp as in EIP-2565 (and geth), used from
// WChainV109. The older EIP-198 formula made a 1-byte modulus with a long
// exponent cost ~260 ns per gas, enough to stall proposers (audit E-H1).
func modExpGasEIP2565(maxLen, adjExpLen *big.Int) uint64 {
	// mult_complexity = ceil(max_length / 8) ^ 2
	gas := new(big.Int).Add(maxLen, big7)
	gas.Div(gas, big8)
	gas.Mul(gas, gas)

	if adjExpLen.Cmp(big1) > 0 {
		gas.Mul(gas, adjExpLen)
	}

	gas.Div(gas, big3)

	if !gas.IsUint64() {
		return math.MaxUint64
	}

	if gas.Uint64() < 200 {
		return 200
	}

	return gas.Uint64()
}

// modExpGasEIP7883 prices modexp as in EIP-7883 (and geth's Osaka rules),
// used from WChainV110. Under EIP-2565 a small modulus with a long
// zero-padded exponent still cost 8-10x more time per gas than ecrecover
// (audit PS-M1): EIP-7883 has a 500 gas minimum, a 16 gas floor on the
// multiplication cost, no division by 3, and 16 (not 8) per exponent byte
// beyond the first 32.
func modExpGasEIP7883(maxLen, expLen, expHead *big.Int) uint64 {
	// multiplication complexity: 16 up to 32 bytes, else 2 * ceil(len/8)^2
	mult := new(big.Int).SetUint64(16)
	if maxLen.Cmp(big32) > 0 {
		words := new(big.Int).Add(maxLen, big7)
		words.Div(words, big8)
		mult.Mul(words, words)
		mult.Mul(mult, big2)
	}

	// iteration count: 16 per exponent byte beyond 32, plus the highest set
	// bit of the first 32 bytes, at least 1
	iterations := new(big.Int)
	if expLen.Cmp(big32) > 0 {
		iterations.Sub(expLen, big32)
		iterations.Mul(iterations, big16)
	}

	if bitLen := expHead.BitLen(); bitLen > 0 {
		iterations.Add(iterations, big.NewInt(int64(bitLen-1)))
	}

	if iterations.Cmp(big1) < 0 {
		iterations.Set(big1)
	}

	gas := mult.Mul(mult, iterations)
	if !gas.IsUint64() {
		return math.MaxUint64
	}

	if gas.Uint64() < 500 {
		return 500
	}

	return gas.Uint64()
}

func (m *modExp) run(input []byte, _ types.Address, _ runtime.Host) ([]byte, error) {
	// get the lengths
	var baseLen, exponentLen, modulusLen uint64

	baseLen, input = m.p.getUint64(input)
	exponentLen, input = m.p.getUint64(input)
	modulusLen, input = m.p.getUint64(input)

	if baseLen == 0 && modulusLen == 0 {
		return nil, nil
	}

	// get the values
	var val []byte

	val, input = m.p.get(input, int(baseLen))
	base := new(big.Int).SetBytes(val)

	val, input = m.p.get(input, int(exponentLen))
	exponent := new(big.Int).SetBytes(val)

	val, _ = m.p.get(input, int(modulusLen))
	modulus := new(big.Int).SetBytes(val)

	var res []byte
	if modulus.Sign() != 0 {
		res = base.Exp(base, exponent, modulus).Bytes()
	}

	return m.p.leftPad(res, int(modulusLen)), nil
}
