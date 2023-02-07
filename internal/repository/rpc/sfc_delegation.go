/*
Package rpc implements bridge to Lachesis full node API interface.

We recommend using local IPC for fast and the most efficient inter-process communication between the API server
and an Opera/Lachesis node. Any remote RPC connection will work, but the performance may be significantly degraded
by extra networking overhead of remote RPC calls.

You should also consider security implications of opening Lachesis RPC interface for a remote access.
If you considering it as your deployment strategy, you should establish encrypted channel between the API server
and Lachesis RPC interface with connection limited to specified endpoints.

We strongly discourage opening Lachesis RPC interface for unrestricted Internet access.
*/
package rpc

import (
	"context"
	"fmt"
	"galaxy-graphql/internal/repository/rpc/contracts"
	"galaxy-graphql/internal/types"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// AmountStaked returns the current amount at stake for the given staker address and target validator
func (chain *ChainBridge) AmountStaked(addr *common.Address, valID *big.Int) (*big.Int, error) {
	// keep track of the operation
	chain.log.Debugf("verifying amount staked by %s to %d", addr.String(), valID.Uint64())
	return chain.SfcContract().GetStake(chain.DefaultCallOpts(), *addr, valID)
}

// AmountStakeLocked returns the current locked amount at stake for the given staker address and target validator.
func (chain *ChainBridge) AmountStakeLocked(addr *common.Address, valID *big.Int) (*big.Int, error) {
	return chain.SfcContract().GetLockedStake(chain.DefaultCallOpts(), *addr, valID)
}

// AmountStakeUnlocked returns the current unlocked amount at stake for the given staker address and target validator.
func (chain *ChainBridge) AmountStakeUnlocked(addr *common.Address, valID *big.Int) (*big.Int, error) {
	return chain.SfcContract().GetUnlockedStake(chain.DefaultCallOpts(), *addr, valID)
}

// StakeUnlockPenalty returns the expected penalty of a premature stake unlock.
func (chain *ChainBridge) StakeUnlockPenalty(addr *common.Address, valID *big.Int, amount *big.Int) (*big.Int, error) {
	// pack call data
	cd, err := chain.SfcAbi().Pack("unlockStake", valID, amount)
	if err != nil {
		chain.log.Errorf("penalty for unlocking %d of %s to %d not available; %s", amount.Uint64(), addr.String(), valID.Uint64(), err.Error())
		return nil, err
	}

	// make the UnlockStake call as a view call to get the penalty value
	data, err := chain.eth.CallContract(context.Background(), ethereum.CallMsg{
		From: *addr,
		To:   &chain.sfcConfig.SFCContract,
		Data: cd,
	}, nil)
	if err != nil {
		chain.log.Errorf("penalty for unlocking %d of %s to %d not available; %s", amount.Uint64(), addr.String(), valID.Uint64(), err.Error())
		return nil, err
	}

	// make response size sanity check; we expect single big integer value
	if len(data) != 32 {
		chain.log.Errorf("penalty for unlocking %d of %s to %d response not valid; expected 32 bytes, received %d bytes", amount.Uint64(), addr.String(), valID.Uint64(), len(data))
		return nil, err
	}

	// parse data in to the value and return it
	return new(big.Int).SetBytes(data), nil
}

// PendingRewards returns a detail of delegation rewards waiting to be claimed for the given delegation.
func (chain *ChainBridge) PendingRewards(addr *common.Address, valID *big.Int) (*types.PendingRewards, error) {
	// prep the empty value
	pr := types.PendingRewards{
		Address: *addr,
		Staker:  hexutil.Big(*valID),
		Amount:  hexutil.Big{},
	}

	// get the pending rewards amount
	amo, err := chain.SfcContract().PendingRewards(chain.DefaultCallOpts(), *addr, valID)
	if err != nil {
		chain.log.Criticalf("can not calculate pending rewards of %s to %d; %s", addr.String(), valID.Uint64(), err.Error())
		return &pr, nil
	}

	// update the amount
	pr.Amount = hexutil.Big(*amo)
	return &pr, nil
}

// DelegationLock returns delegation lock information using SFC contract binding.
func (chain *ChainBridge) DelegationLock(addr *common.Address, valID *hexutil.Big) (dll *types.DelegationLock, err error) {
	// recover from panic here
	defer func() {
		if r := recover(); r != nil {
			chain.log.Criticalf("can not get SFC lock status on delegation %s to %d; SFC call panic", addr.String(), valID.String())
			dll = &types.DelegationLock{}
		}
	}()

	// get staker locking detail
	lock, err := chain.SfcContract().GetLockupInfo(chain.DefaultCallOpts(), *addr, valID.ToInt())
	if err != nil {
		chain.log.Errorf("delegation lock query failed; %v", err)
		return nil, err
	}

	// are lock timers available?
	if lock.FromEpoch == nil || lock.EndTime == nil {
		chain.log.Errorf("delegation lock details not available")
		return nil, fmt.Errorf("delegation lock missing")
	}

	// make a new delegation lock
	return &types.DelegationLock{
		LockedAmount:    hexutil.Big(*lock.LockedStake),
		LockedFromEpoch: hexutil.Uint64(lock.FromEpoch.Uint64()),
		LockedUntil:     hexutil.Uint64(lock.EndTime.Uint64()),
		Duration:        hexutil.Uint64(lock.Duration.Uint64()),
	}, nil
}

// DelegationOutstandingSCoin returns the amount of tokens for the delegation
// identified by the delegator address and the stakerId.
func (chain *ChainBridge) DelegationOutstandingSCoin(addr *common.Address, valID *big.Int) (*big.Int, error) {
	// log action
	chain.log.Debugf("checking outstanding of %s to %d", addr.String(), valID.Uint64())

	// instantiate the contract and display its name
	contract, err := contracts.NewSfcTokenizer(chain.sfcConfig.TokenizerContract, chain.eth)
	if err != nil {
		chain.log.Criticalf("failed to instantiate SFC Tokenizer contract; %s", err.Error())
		return nil, err
	}

	// get the amount of outstanding
	return contract.OutstandingSCoin(chain.DefaultCallOpts(), *addr, valID)
}

// DelegationTokenizerUnlocked returns the status of SFC Tokenizer lock
// for a delegation identified by the address and staker id.
func (chain *ChainBridge) DelegationTokenizerUnlocked(addr *common.Address, valID *big.Int) (bool, error) {
	// log action
	chain.log.Debugf("checking SFC tokenizer lock of %s to %d", addr.String(), valID.Uint64())

	// instantiate the contract and display its name
	contract, err := contracts.NewSfcTokenizer(chain.sfcConfig.TokenizerContract, chain.eth)
	if err != nil {
		chain.log.Criticalf("failed to instantiate SFC Tokenizer contract: %s", err.Error())
		return false, err
	}

	// get the lock status
	lock, err := contract.AllowedToWithdrawStake(chain.DefaultCallOpts(), *addr, valID)
	if err != nil {
		chain.log.Criticalf("failed to get SFC Tokenizer lock status of %s to %d; %s", addr.String(), valID.Uint64(), err.Error())
		return false, err
	}

	return lock, nil
}
