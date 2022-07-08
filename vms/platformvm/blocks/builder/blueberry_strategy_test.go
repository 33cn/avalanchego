// Copyright (C) 2019-2021, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package builder

import (
	"testing"
	"time"

	"github.com/ava-labs/avalanchego/snow/consensus/snowman"
	"github.com/ava-labs/avalanchego/vms/platformvm/blocks/stateful"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs/executor"
	"github.com/stretchr/testify/assert"
)

func TestBlueberryPickingOrder(t *testing.T) {
	assert := assert.New(t)

	// mock ResetBlockTimer to control timing of block formation
	h := newTestHelpersCollection(t, true /*mockResetBlockTimer*/)
	defer func() {
		if err := internalStateShutdown(h); err != nil {
			t.Fatal(err)
		}
	}()
	h.cfg.BlueberryTime = time.Time{} // Blueberry already active

	chainTime := h.fullState.GetTimestamp()
	now := chainTime.Add(time.Second)
	h.clk.Set(now)

	nextChainTime := chainTime.Add(h.cfg.MinStakeDuration).Add(time.Hour)

	// create validator
	validatorStartTime := now.Add(time.Second)
	validatorTx, err := createTestValidatorTx(h, validatorStartTime, nextChainTime)
	assert.NoError(err)

	// accept validator as pending
	txExecutor := executor.ProposalTxExecutor{
		Backend:     &h.txExecBackend,
		ParentState: h.fullState,
		Tx:          validatorTx,
	}
	assert.NoError(validatorTx.Unsigned.Visit(&txExecutor))
	txExecutor.OnCommit.Apply(h.fullState)
	assert.NoError(h.fullState.Commit())

	// promote validator to current
	advanceTime, err := h.txBuilder.NewAdvanceTimeTx(validatorStartTime)
	assert.NoError(err)
	txExecutor.Tx = advanceTime
	assert.NoError(advanceTime.Unsigned.Visit(&txExecutor))
	txExecutor.OnCommit.Apply(h.fullState)
	assert.NoError(h.fullState.Commit())

	// move chain time to current validator's
	// end of staking time, so that it may be rewarded
	h.fullState.SetTimestamp(nextChainTime)
	now = nextChainTime
	h.clk.Set(now)

	// add decisionTx and stakerTxs to mempool
	decisionTxs, err := createTestDecisionTxes(2)
	assert.NoError(err)
	for _, dt := range decisionTxs {
		assert.NoError(h.mempool.Add(dt))
	}

	starkerTxStartTime := nextChainTime.Add(executor.SyncBound).Add(time.Second)
	stakerTx, err := createTestValidatorTx(h, starkerTxStartTime, starkerTxStartTime.Add(time.Hour))
	assert.NoError(err)
	assert.NoError(h.mempool.Add(stakerTx))

	// test: decisionTxs must be picked first
	blk, err := h.BlockBuilder.BuildBlock()
	assert.NoError(err)
	stdBlk, ok := blk.(*stateful.StandardBlock)
	assert.True(ok)
	assert.True(len(decisionTxs) == len(stdBlk.DecisionTxs()))
	for i, tx := range stdBlk.DecisionTxs() {
		assert.Equal(decisionTxs[i].ID(), tx.ID())
	}

	assert.False(h.mempool.HasDecisionTxs())

	// test: reward validator blocks must follow, one per endingValidator
	blk, err = h.BlockBuilder.BuildBlock()
	assert.NoError(err)
	rewardBlk, ok := blk.(*stateful.ProposalBlock)
	assert.True(ok)
	rewardTx, ok := rewardBlk.ProposalTx().Unsigned.(*txs.RewardValidatorTx)
	assert.True(ok)
	assert.Equal(validatorTx.ID(), rewardTx.TxID)

	// accept reward validator tx so that current validator is removed
	assert.NoError(blk.Verify())
	assert.NoError(blk.Accept())
	options, err := blk.(snowman.OracleBlock).Options()
	assert.NoError(err)
	commitBlk := options[0]
	assert.NoError(commitBlk.Verify())
	assert.NoError(commitBlk.Accept())

	// finally mempool addValidatorTx must be picked
	blk, err = h.BlockBuilder.BuildBlock()
	assert.NoError(err)
	propBlk, ok := blk.(*stateful.ProposalBlock)
	assert.True(ok)
	assert.Equal(stakerTx.ID(), propBlk.ProposalTx().ID())
}
