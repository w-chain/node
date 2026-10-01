package gasprice

import (
	"encoding/binary"
	"errors"
	"math"
	"math/big"
	"sort"
)

var (
	ErrInvalidPercentile  = errors.New("invalid percentile")
	ErrBlockCount         = errors.New("blockCount must be greater than 0")
	ErrBlockNotFound      = errors.New("could not find block")
	ErrTooManyPercentiles = errors.New("too many reward percentiles (max 100)")
)

const (
	maxBlockRequest = 1024

	// maxRewardPercentiles caps the reward percentiles one request may ask for.
	maxRewardPercentiles = 100
)

type cacheKey struct {
	number      uint64
	percentiles string
}

// processedFees contains the results of a processed block.
type processedFees struct {
	reward       []uint64
	baseFee      uint64
	gasUsedRatio float64
}

type txGasAndReward struct {
	gasUsed *big.Int
	reward  *big.Int
}

type FeeHistoryReturn struct {
	OldestBlock   uint64
	BaseFeePerGas []uint64
	GasUsedRatio  []float64
	Reward        [][]uint64
}

func (g *GasHelper) FeeHistory(blockCount uint64, newestBlock uint64, rewardPercentiles []float64) (
	*FeeHistoryReturn, error) {
	if blockCount < 1 {
		return &FeeHistoryReturn{0, nil, nil, nil}, ErrBlockCount
	}

	if newestBlock > g.backend.Header().Number {
		newestBlock = g.backend.Header().Number
	}

	if blockCount > maxBlockRequest {
		blockCount = maxBlockRequest
	}

	if blockCount > newestBlock {
		blockCount = newestBlock
	}

	// Every block in the range gets one reward entry per percentile, so an
	// uncapped list made one small request allocate gigabytes (audit RPC-H1).
	// Same limit as geth.
	if len(rewardPercentiles) > maxRewardPercentiles {
		return &FeeHistoryReturn{0, nil, nil, nil}, ErrTooManyPercentiles
	}

	for i, p := range rewardPercentiles {
		if p < 0 || p > 100 {
			return &FeeHistoryReturn{0, nil, nil, nil}, ErrInvalidPercentile
		}

		if i > 0 && p < rewardPercentiles[i-1] {
			return &FeeHistoryReturn{0, nil, nil, nil}, ErrInvalidPercentile
		}
	}

	var (
		oldestBlock   = newestBlock - blockCount + 1
		baseFeePerGas = make([]uint64, blockCount+1)
		gasUsedRatio  = make([]float64, blockCount)
		reward        = make([][]uint64, blockCount)
	)

	if oldestBlock < 1 {
		oldestBlock = 1
	}

	percentileKey := make([]byte, 8*len(rewardPercentiles))
	for i, p := range rewardPercentiles {
		binary.LittleEndian.PutUint64(percentileKey[i*8:(i+1)*8], math.Float64bits(p))
	}

	for i := oldestBlock; i <= newestBlock; i++ {
		cacheKey := cacheKey{number: i, percentiles: string(percentileKey)}
		//cache is hit, load from cache and continue to next block
		if p, ok := g.historyCache.Get(cacheKey); ok {
			processedFee, isOk := p.(*processedFees)
			if !isOk {
				return &FeeHistoryReturn{0, nil, nil, nil}, errors.New("could not convert catched processed fee")
			}

			baseFeePerGas[i-oldestBlock] = processedFee.baseFee
			gasUsedRatio[i-oldestBlock] = processedFee.gasUsedRatio
			reward[i-oldestBlock] = processedFee.reward

			continue
		}

		block, ok := g.backend.GetBlockByNumber(i, true)
		if !ok {
			return &FeeHistoryReturn{0, nil, nil, nil}, ErrBlockNotFound
		}

		baseFeePerGas[i-oldestBlock] = block.Header.BaseFee
		gasUsedRatio[i-oldestBlock] = float64(block.Header.GasUsed) / float64(block.Header.GasLimit)

		if math.IsNaN(gasUsedRatio[i-oldestBlock]) {
			//gasUsedRatio is NaN, set to 0
			gasUsedRatio[i-oldestBlock] = 0
		}

		if len(rewardPercentiles) == 0 {
			//reward percentiles not requested, skip rest of this loop
			continue
		}

		reward[i-oldestBlock] = make([]uint64, len(rewardPercentiles))
		if len(block.Transactions) == 0 {
			for j := range reward[i-oldestBlock] {
				reward[i-oldestBlock][j] = 0
			}
			//no transactions in block, set rewards to 0 and move to next block
			continue
		}

		sorter := make([]*txGasAndReward, len(block.Transactions))
		baseFee := new(big.Int).SetUint64(block.Header.BaseFee)

		// Weigh each tx by its gas, in gas units. It used to be gas*price
		// (wei) compared against a threshold in gas, so the very first tx
		// already passed every percentile and all rewards came out as the
		// lowest tip (audit RPC-L1). Receipts are not at hand here, so a tx's
		// gas limit stands in for its gas used, and percentiles are taken of
		// the block's total gas limit of txs (exact for plain transfers).
		totalTxGas := uint64(0)

		for j, tx := range block.Transactions {
			sorter[j] = &txGasAndReward{
				gasUsed: new(big.Int).SetUint64(tx.Gas),
				reward:  tx.EffectiveGasTip(baseFee),
			}
			totalTxGas += tx.Gas
		}

		sort.Slice(sorter, func(i, j int) bool {
			return sorter[i].reward.Cmp(sorter[j].reward) < 0
		})

		var txIndex int

		sumGasUsed := sorter[0].gasUsed.Uint64()

		// calculate reward for each percentile
		for c, v := range rewardPercentiles {
			thresholdGasUsed := uint64(float64(totalTxGas) * v / 100)
			for sumGasUsed < thresholdGasUsed && txIndex < len(block.Transactions)-1 {
				txIndex++
				sumGasUsed += sorter[txIndex].gasUsed.Uint64()
			}

			reward[i-oldestBlock][c] = sorter[txIndex].reward.Uint64()
		}

		blockFees := &processedFees{
			reward:       reward[i-oldestBlock],
			baseFee:      block.Header.BaseFee,
			gasUsedRatio: gasUsedRatio[i-oldestBlock],
		}
		g.historyCache.Add(cacheKey, blockFees)
	}

	baseFeePerGas[blockCount] = g.backend.Header().BaseFee

	return &FeeHistoryReturn{oldestBlock, baseFeePerGas, gasUsedRatio, reward}, nil
}
