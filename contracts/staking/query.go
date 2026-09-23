package staking

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/umbracle/ethgo"

	"github.com/umbracle/ethgo/abi"
	"github.com/w-chain-team/node/contracts/abis"
	"github.com/w-chain-team/node/state/runtime"
	"github.com/w-chain-team/node/types"
)

const (
	methodValidators             = "validators"
	methodValidatorBLSPublicKeys = "validatorBLSPublicKeys"
	methodGetRewardPool          = "getRewardPool"
	methodGetRewardPerEpoch      = "getRewardPerEpoch"
)

var (
	// staking contract address
	AddrStakingContract = types.StringToAddress("d941a16af07cAc64F61C92258250C018F6e929E0")

	// Gas limit used when querying the validator set
	queryGasLimit uint64 = 1000000

	ErrMethodNotFoundInABI = errors.New("method not found in ABI")
	ErrFailedTypeAssertion = errors.New("failed type assertion")
)

// TxQueryHandler is a interface to call view method in the contract
type TxQueryHandler interface {
	Apply(*types.Transaction) (*runtime.ExecutionResult, error)
	GetNonce(types.Address) uint64
	SetNonPayable(nonPayable bool)
}

// ValidatorRewardComponents holds all the components needed for validator reward distribution
type ValidatorRewardComponents struct {
	// Addresses of all validators
	ValidatorAddresses []types.Address
	// Address of the reward pool
	RewardPoolAddress types.Address
	// Reward amount per epoch for each validator
	RewardPerEpoch *big.Int
}

// decodeWeb3ArrayOfBytes is a helper function to parse the data
// representing array of bytes in contract result
func decodeWeb3ArrayOfBytes(
	result interface{},
) ([][]byte, error) {
	mapResult, ok := result.(map[string]interface{})
	if !ok {
		return nil, ErrFailedTypeAssertion
	}

	bytesArray, ok := mapResult["0"].([][]byte)
	if !ok {
		return nil, ErrFailedTypeAssertion
	}

	return bytesArray, nil
}

// createCallViewTx is a helper function to create a transaction to call view method
func createCallViewTx(
	from types.Address,
	contractAddress types.Address,
	methodID []byte,
	nonce uint64,
) *types.Transaction {
	return &types.Transaction{
		From:     from,
		To:       &contractAddress,
		Input:    methodID,
		Nonce:    nonce,
		Gas:      queryGasLimit,
		Value:    big.NewInt(0),
		GasPrice: big.NewInt(0),
	}
}

// DecodeValidators parses contract call result and returns array of address
func DecodeValidators(method *abi.Method, returnValue []byte) ([]types.Address, error) {
	decodedResults, err := method.Outputs.Decode(returnValue)
	if err != nil {
		return nil, err
	}

	results, ok := decodedResults.(map[string]interface{})
	if !ok {
		return nil, errors.New("failed type assertion from decodedResults to map")
	}

	web3Addresses, ok := results["0"].([]ethgo.Address)

	if !ok {
		return nil, errors.New("failed type assertion from results[0] to []ethgo.Address")
	}

	addresses := make([]types.Address, len(web3Addresses))
	for idx, waddr := range web3Addresses {
		addresses[idx] = types.Address(waddr)
	}

	return addresses, nil
}

// QueryValidators is a helper function to get validator addresses from contract
func QueryValidators(t TxQueryHandler, from types.Address) ([]types.Address, error) {
	method, ok := abis.StakingABI.Methods[methodValidators]
	if !ok {
		return nil, ErrMethodNotFoundInABI
	}

	t.SetNonPayable(true)
	res, err := t.Apply(createCallViewTx(
		from,
		AddrStakingContract,
		method.ID(),
		t.GetNonce(from),
	))

	if err != nil {
		return nil, err
	}

	if res.Failed() {
		return nil, res.Err
	}

	return DecodeValidators(method, res.ReturnValue)
}

// decodeBLSPublicKeys parses contract call result and returns array of bytes
func decodeBLSPublicKeys(
	method *abi.Method,
	returnValue []byte,
) ([][]byte, error) {
	decodedResults, err := method.Outputs.Decode(returnValue)
	if err != nil {
		return nil, err
	}

	blsPublicKeys, err := decodeWeb3ArrayOfBytes(decodedResults)
	if err != nil {
		return nil, err
	}

	return blsPublicKeys, nil
}

// QueryBLSPublicKeys is a helper function to get BLS Public Keys from contract
func QueryBLSPublicKeys(t TxQueryHandler, from types.Address) ([][]byte, error) {
	method, ok := abis.StakingABI.Methods[methodValidatorBLSPublicKeys]
	if !ok {
		return nil, ErrMethodNotFoundInABI
	}

	t.SetNonPayable(true)
	res, err := t.Apply(createCallViewTx(
		from,
		AddrStakingContract,
		method.ID(),
		t.GetNonce(from),
	))

	if err != nil {
		return nil, err
	}

	if res.Failed() {
		return nil, res.Err
	}

	return decodeBLSPublicKeys(method, res.ReturnValue)
}

// DecodeAddress parses contract call result and returns single address
func DecodeAddress(method *abi.Method, returnValue []byte) (types.Address, error) {
	decodedResults, err := method.Outputs.Decode(returnValue)
	if err != nil {
		return types.Address{}, err
	}

	results, ok := decodedResults.(map[string]interface{})
	if !ok {
		return types.Address{}, errors.New("failed type assertion from decodedResults to map")
	}

	address, ok := results["0"].(ethgo.Address)
	if !ok {
		return types.Address{}, errors.New("failed type assertion from results[0] to ethgo.Address")
	}

	return types.Address(address), nil
}

// DecodeBigInt parses contract call result and returns big.Int
func DecodeBigInt(method *abi.Method, returnValue []byte) (*big.Int, error) {
	decodedResults, err := method.Outputs.Decode(returnValue)
	if err != nil {
		return nil, err
	}

	results, ok := decodedResults.(map[string]interface{})
	if !ok {
		return nil, errors.New("failed type assertion from decodedResults to map")
	}

	amount, ok := results["0"].(*big.Int)
	if !ok {
		return nil, errors.New("failed type assertion from results[0] to *big.Int")
	}

	return amount, nil
}

// QueryRewardPool is a helper function to get reward pool address from contract
func QueryRewardPool(t TxQueryHandler, from types.Address) (types.Address, error) {
	method, ok := abis.StakingABI.Methods[methodGetRewardPool]
	if !ok {
		return types.Address{}, ErrMethodNotFoundInABI
	}

	t.SetNonPayable(true)
	res, err := t.Apply(createCallViewTx(
		from,
		AddrStakingContract,
		method.ID(),
		t.GetNonce(from),
	))

	if err != nil {
		return types.Address{}, err
	}

	if res.Failed() {
		return types.Address{}, res.Err
	}

	return DecodeAddress(method, res.ReturnValue)
}

// QueryRewardPerEpoch is a helper function to get reward per epoch for each validator
func QueryRewardPerEpoch(t TxQueryHandler, from types.Address) (*big.Int, error) {
	method, ok := abis.StakingABI.Methods[methodGetRewardPerEpoch]
	if !ok {
		return nil, ErrMethodNotFoundInABI
	}

	t.SetNonPayable(true)
	res, err := t.Apply(createCallViewTx(
		from,
		AddrStakingContract,
		method.ID(),
		t.GetNonce(from),
	))

	if err != nil {
		return nil, err
	}

	if res.Failed() {
		return nil, res.Err
	}

	return DecodeBigInt(method, res.ReturnValue)
}

// FetchValidatorRewardComponents fetches all components needed for validator reward distribution
func FetchValidatorRewardComponents(
	transition TxQueryHandler,
	from types.Address,
) (*ValidatorRewardComponents, error) {
	// Fetch validator addresses
	valAddrs, err := QueryValidators(transition, from)
	if err != nil {
		return nil, fmt.Errorf("failed to query validator addresses: %w", err)
	}

	// Fetch reward pool address
	rewardPoolAddr, err := QueryRewardPool(transition, from)
	if err != nil {
		return nil, fmt.Errorf("failed to query reward pool address: %w", err)
	}

	// Fetch reward per epoch
	rewardPerEpoch, err := QueryRewardPerEpoch(transition, from)
	if err != nil {
		// Log the error but return a default reward component with zero reward
		return &ValidatorRewardComponents{
			ValidatorAddresses: valAddrs,
			RewardPoolAddress:  rewardPoolAddr,
			RewardPerEpoch:     big.NewInt(0),
		}, nil
	}

	// Calculate reward per validator by dividing total reward by number of validators
	if len(valAddrs) > 0 {
		rewardPerValidator := new(big.Int).Div(rewardPerEpoch, big.NewInt(int64(len(valAddrs))))
		rewardPerEpoch = rewardPerValidator
	}

	return &ValidatorRewardComponents{
		ValidatorAddresses: valAddrs,
		RewardPoolAddress:  rewardPoolAddr,
		RewardPerEpoch:     rewardPerEpoch,
	}, nil
}
