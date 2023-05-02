// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package state

import (
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/golang/mock/gomock"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto/bls"
	"github.com/ava-labs/avalanchego/utils/timer/mockable"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
)

var errCustom = errors.New("custom")

func TestStakerLess(t *testing.T) {
	tests := []struct {
		name  string
		left  *Staker
		right *Staker
		less  bool
	}{
		{
			name: "left time < right time",
			left: &Staker{
				TxID:     ids.ID([32]byte{}),
				NextTime: time.Unix(0, 0),
				Priority: txs.PrimaryNetworkValidatorCurrentPriority,
			},
			right: &Staker{
				TxID:     ids.ID([32]byte{}),
				NextTime: time.Unix(1, 0),
				Priority: txs.PrimaryNetworkValidatorCurrentPriority,
			},
			less: true,
		},
		{
			name: "left time > right time",
			left: &Staker{
				TxID:     ids.ID([32]byte{}),
				NextTime: time.Unix(1, 0),
				Priority: txs.PrimaryNetworkValidatorCurrentPriority,
			},
			right: &Staker{
				TxID:     ids.ID([32]byte{}),
				NextTime: time.Unix(0, 0),
				Priority: txs.PrimaryNetworkValidatorCurrentPriority,
			},
			less: false,
		},
		{
			name: "left priority < right priority",
			left: &Staker{
				TxID:     ids.ID([32]byte{}),
				NextTime: time.Unix(0, 0),
				Priority: txs.PrimaryNetworkDelegatorApricotPendingPriority,
			},
			right: &Staker{
				TxID:     ids.ID([32]byte{}),
				NextTime: time.Unix(0, 0),
				Priority: txs.PrimaryNetworkValidatorPendingPriority,
			},
			less: true,
		},
		{
			name: "left priority > right priority",
			left: &Staker{
				TxID:     ids.ID([32]byte{}),
				NextTime: time.Unix(0, 0),
				Priority: txs.PrimaryNetworkValidatorPendingPriority,
			},
			right: &Staker{
				TxID:     ids.ID([32]byte{}),
				NextTime: time.Unix(0, 0),
				Priority: txs.PrimaryNetworkDelegatorApricotPendingPriority,
			},
			less: false,
		},
		{
			name: "left txID < right txID",
			left: &Staker{
				TxID:     ids.ID([32]byte{0}),
				NextTime: time.Unix(0, 0),
				Priority: txs.PrimaryNetworkValidatorPendingPriority,
			},
			right: &Staker{
				TxID:     ids.ID([32]byte{1}),
				NextTime: time.Unix(0, 0),
				Priority: txs.PrimaryNetworkValidatorPendingPriority,
			},
			less: true,
		},
		{
			name: "left txID > right txID",
			left: &Staker{
				TxID:     ids.ID([32]byte{1}),
				NextTime: time.Unix(0, 0),
				Priority: txs.PrimaryNetworkValidatorPendingPriority,
			},
			right: &Staker{
				TxID:     ids.ID([32]byte{0}),
				NextTime: time.Unix(0, 0),
				Priority: txs.PrimaryNetworkValidatorPendingPriority,
			},
			less: false,
		},
		{
			name: "equal",
			left: &Staker{
				TxID:     ids.ID([32]byte{}),
				NextTime: time.Unix(0, 0),
				Priority: txs.PrimaryNetworkValidatorCurrentPriority,
			},
			right: &Staker{
				TxID:     ids.ID([32]byte{}),
				NextTime: time.Unix(0, 0),
				Priority: txs.PrimaryNetworkValidatorCurrentPriority,
			},
			less: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.less, test.left.Less(test.right))
		})
	}
}

func TestNewCurrentStakerPreContinuousStakingFork(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	txID := ids.GenerateTestID()
	nodeID := ids.GenerateTestNodeID()
	sk, err := bls.NewSecretKey()
	require.NoError(err)
	publicKey := bls.PublicFromSecretKey(sk)
	subnetID := ids.GenerateTestID()
	weight := uint64(12345)
	startTime := time.Now().Truncate(time.Second)
	endTime := time.Now().Truncate(time.Second)
	duration := endTime.Sub(startTime)
	potentialReward := uint64(54321)
	currentPriority := txs.SubnetPermissionedValidatorCurrentPriority

	stakerTx := txs.NewMockStaker(ctrl)
	stakerTx.EXPECT().EndTime().Return(endTime)
	stakerTx.EXPECT().StakingPeriod().Return(duration)
	stakerTx.EXPECT().NodeID().Return(nodeID)
	stakerTx.EXPECT().PublicKey().Return(publicKey, true, nil)
	stakerTx.EXPECT().SubnetID().Return(subnetID)
	stakerTx.EXPECT().Weight().Return(weight)
	stakerTx.EXPECT().CurrentPriority().Return(currentPriority)

	staker, err := NewCurrentStaker(txID, stakerTx, startTime, potentialReward)
	require.NotNil(staker)
	require.NoError(err)
	require.Equal(txID, staker.TxID)
	require.Equal(nodeID, staker.NodeID)
	require.Equal(publicKey, staker.PublicKey)
	require.Equal(subnetID, staker.SubnetID)
	require.Equal(weight, staker.Weight)
	require.Equal(startTime, staker.StartTime)
	require.Equal(duration, staker.StakingPeriod)
	require.Equal(endTime, staker.EndTime)
	require.Equal(potentialReward, staker.PotentialReward)
	require.Equal(endTime, staker.NextTime)
	require.Equal(currentPriority, staker.Priority)

	stakerTx.EXPECT().PublicKey().Return(nil, false, errCustom)

	_, err = NewCurrentStaker(txID, stakerTx, startTime, potentialReward)
	require.ErrorIs(err, errCustom)
}

func TestNewCurrentStaker(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	txID := ids.GenerateTestID()
	nodeID := ids.GenerateTestNodeID()
	sk, err := bls.NewSecretKey()
	require.NoError(err)
	publicKey := bls.PublicFromSecretKey(sk)
	subnetID := ids.GenerateTestID()
	weight := uint64(12345)
	startTime := time.Unix(rand.Int63(), 0)                                 // #nosec G404
	duration := time.Duration(rand.Int63n(365 * 24 * 60 * 60 * 1000000000)) // #nosec G404
	endTime := mockable.MaxTime
	potentialReward := uint64(54321)
	currentPriority := txs.SubnetPermissionedValidatorCurrentPriority

	stakerTx := txs.NewMockStaker(ctrl)
	stakerTx.EXPECT().EndTime().Return(endTime)
	stakerTx.EXPECT().StakingPeriod().Return(duration)
	stakerTx.EXPECT().NodeID().Return(nodeID)
	stakerTx.EXPECT().PublicKey().Return(publicKey, true, nil)
	stakerTx.EXPECT().SubnetID().Return(subnetID)
	stakerTx.EXPECT().Weight().Return(weight)
	stakerTx.EXPECT().CurrentPriority().Return(currentPriority)

	staker, err := NewCurrentStaker(txID, stakerTx, startTime, potentialReward)
	require.NotNil(staker)
	require.NoError(err)
	require.Equal(txID, staker.TxID)
	require.Equal(nodeID, staker.NodeID)
	require.Equal(publicKey, staker.PublicKey)
	require.Equal(subnetID, staker.SubnetID)
	require.Equal(weight, staker.Weight)
	require.Equal(startTime, staker.StartTime)
	require.Equal(duration, staker.StakingPeriod)
	require.Equal(endTime, staker.EndTime)
	require.Equal(potentialReward, staker.PotentialReward)
	require.Equal(startTime.Add(duration), staker.NextTime)
	require.Equal(currentPriority, staker.Priority)

	stakerTx.EXPECT().PublicKey().Return(nil, false, errCustom)

	_, err = NewCurrentStaker(txID, stakerTx, startTime, potentialReward)
	require.ErrorIs(err, errCustom)
}

func TestNewPendingStaker(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	txID := ids.GenerateTestID()
	nodeID := ids.GenerateTestNodeID()
	sk, err := bls.NewSecretKey()
	require.NoError(err)
	publicKey := bls.PublicFromSecretKey(sk)
	subnetID := ids.GenerateTestID()
	weight := uint64(12345)
	startTime := time.Now()
	endTime := time.Now()
	duration := endTime.Sub(startTime)
	pendingPriority := txs.SubnetPermissionedValidatorPendingPriority

	stakerTx := txs.NewMockStaker(ctrl)
	stakerTx.EXPECT().NodeID().Return(nodeID)
	stakerTx.EXPECT().PublicKey().Return(publicKey, true, nil)
	stakerTx.EXPECT().SubnetID().Return(subnetID)
	stakerTx.EXPECT().Weight().Return(weight)
	stakerTx.EXPECT().StartTime().Return(startTime)
	stakerTx.EXPECT().StakingPeriod().Return(duration)
	stakerTx.EXPECT().EndTime().Return(endTime)
	stakerTx.EXPECT().PendingPriority().Return(pendingPriority)

	staker, err := NewPendingStaker(txID, stakerTx)
	require.NotNil(staker)
	require.NoError(err)
	require.Equal(txID, staker.TxID)
	require.Equal(nodeID, staker.NodeID)
	require.Equal(publicKey, staker.PublicKey)
	require.Equal(subnetID, staker.SubnetID)
	require.Equal(weight, staker.Weight)
	require.Equal(startTime, staker.StartTime)
	require.Equal(duration, staker.StakingPeriod)
	require.Equal(endTime, staker.EndTime)
	require.Zero(staker.PotentialReward)
	require.Equal(startTime, staker.NextTime)
	require.Equal(pendingPriority, staker.Priority)

	stakerTx.EXPECT().PublicKey().Return(nil, false, errCustom)

	_, err = NewPendingStaker(txID, stakerTx)
	require.ErrorIs(err, errCustom)
}

func TestShiftStaker(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// create the staker
	var (
		start         = time.Now().Truncate(time.Second)
		stakingPeriod = 6 * 30 * 24 * time.Hour
		end           = mockable.MaxTime
	)

	// Shift with max end time
	staker := &Staker{
		StartTime:     start,
		StakingPeriod: stakingPeriod,
		NextTime:      start.Add(stakingPeriod),
		EndTime:       end,
	}
	require.True(staker.NextTime.Before(staker.EndTime))

	ShiftStakerAheadInPlace(staker)
	require.Equal(start.Add(stakingPeriod), staker.StartTime)
	require.Equal(stakingPeriod, staker.StakingPeriod)
	require.Equal(start.Add(2*stakingPeriod), staker.NextTime)
	require.Equal(end, staker.EndTime)
	require.False(staker.NextTime.After(staker.EndTime)) // invariant

	// Shift with finite end time set in the future
	periods := 5
	end = start.Add(time.Duration(periods) * stakingPeriod)
	staker = &Staker{
		StartTime:     start,
		StakingPeriod: stakingPeriod,
		NextTime:      start.Add(stakingPeriod),
		EndTime:       end,
	}
	require.False(staker.NextTime.After(staker.EndTime)) // invariant

	for i := 1; i < periods; i++ {
		ShiftStakerAheadInPlace(staker)
		require.Equal(start.Add(time.Duration(i)*stakingPeriod), staker.StartTime)
		require.Equal(stakingPeriod, staker.StakingPeriod)
		require.Equal(start.Add(time.Duration(i+1)*stakingPeriod), staker.NextTime)
		require.Equal(end, staker.EndTime)
		require.False(staker.NextTime.After(staker.EndTime)) // invariant
	}
	require.Equal(staker.EndTime, staker.NextTime)

	// staker reached end of life, shift must be ineffective
	cpy := *staker
	ShiftStakerAheadInPlace(&cpy)
	require.Equal(staker, &cpy)
}

func TestPrimaryNetworkValidatorStopTimes(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// create the staker
	nodeID := ids.GenerateTestNodeID()
	subnetID := constants.PrimaryNetworkID
	startTime := time.Now().Truncate(time.Second)
	duration := 365 * 24 * time.Hour
	endTime := mockable.MaxTime
	currentPriority := txs.PrimaryNetworkValidatorCurrentPriority

	stakerTx := txs.NewMockStaker(ctrl)
	stakerTx.EXPECT().NodeID().Return(nodeID)
	stakerTx.EXPECT().SubnetID().Return(subnetID)
	stakerTx.EXPECT().StakingPeriod().Return(duration)
	stakerTx.EXPECT().EndTime().Return(endTime)
	stakerTx.EXPECT().CurrentPriority().Return(currentPriority)

	stakerTx.EXPECT().PublicKey().Return(nil, true, nil)
	stakerTx.EXPECT().Weight().Return(uint64(123))

	txID := ids.GenerateTestID()
	potentialReward := uint64(54321)
	staker, err := NewCurrentStaker(txID, stakerTx, startTime, potentialReward)
	require.NoError(err)

	// stopTime should be at T+1 staking period
	require.Equal(startTime.Add(duration), staker.NextTime)
	require.Equal(mockable.MaxTime, staker.EndTime)
	stopTime := staker.EarliestStopTime()
	require.Equal(staker.NextTime.Add(staker.StakingPeriod), stopTime)

	MarkStakerForRemovalInPlaceBeforeTime(staker, stopTime)
	require.Equal(stopTime, staker.EndTime)
	require.Equal(stopTime, staker.EarliestStopTime())

	// staker shift must not change stop time
	ShiftStakerAheadInPlace(staker)
	require.Equal(stopTime, staker.EndTime)
	require.Equal(stopTime, staker.EarliestStopTime())
}

func TestNonPrimaryNetworkValidatorStopTimes(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// create the staker
	nodeID := ids.GenerateTestNodeID()
	subnetID := constants.PrimaryNetworkID
	startTime := time.Now().Truncate(time.Second)
	duration := 365 * 24 * time.Hour
	endTime := mockable.MaxTime
	currentPriority := txs.PrimaryNetworkDelegatorCurrentPriority

	stakerTx := txs.NewMockStaker(ctrl)
	stakerTx.EXPECT().NodeID().Return(nodeID)
	stakerTx.EXPECT().SubnetID().Return(subnetID)
	stakerTx.EXPECT().StakingPeriod().Return(duration)
	stakerTx.EXPECT().EndTime().Return(endTime)
	stakerTx.EXPECT().CurrentPriority().Return(currentPriority)

	stakerTx.EXPECT().PublicKey().Return(nil, true, nil)
	stakerTx.EXPECT().Weight().Return(uint64(123))

	txID := ids.GenerateTestID()
	potentialReward := uint64(54321)
	staker, err := NewCurrentStaker(txID, stakerTx, startTime, potentialReward)
	require.NoError(err)

	// stopTime should be at end of staking period
	require.Equal(startTime.Add(duration), staker.NextTime)
	require.Equal(mockable.MaxTime, staker.EndTime)
	stopTime := staker.EarliestStopTime()
	require.Equal(staker.NextTime, stopTime)

	MarkStakerForRemovalInPlaceBeforeTime(staker, stopTime)
	require.Equal(stopTime, staker.NextTime)
	require.Equal(stopTime, staker.EndTime)
	require.Equal(stopTime, staker.EarliestStopTime())
}
