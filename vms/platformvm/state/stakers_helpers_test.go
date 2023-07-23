// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package state

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ava-labs/avalanchego/chains"
	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/uptime"
	"github.com/ava-labs/avalanchego/snow/validators"
	"github.com/ava-labs/avalanchego/utils"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/formatting"
	"github.com/ava-labs/avalanchego/utils/json"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/vms/platformvm/api"
	"github.com/ava-labs/avalanchego/vms/platformvm/config"
	"github.com/ava-labs/avalanchego/vms/platformvm/metrics"
	"github.com/ava-labs/avalanchego/vms/platformvm/reward"
)

var (
	_ Versions = (*versionsHolder)(nil)

	xChainID    = ids.Empty.Prefix(0)
	cChainID    = ids.Empty.Prefix(1)
	avaxAssetID = ids.ID{'y', 'e', 'e', 't'}

	defaultMinStakingDuration = 24 * time.Hour
	defaultMaxStakingDuration = 365 * 24 * time.Hour
	defaultGenesisTime        = time.Date(1997, 1, 1, 0, 0, 0, 0, time.UTC)
	defaultValidateStartTime  = defaultGenesisTime
	defaultValidateEndTime    = defaultValidateStartTime.Add(10 * defaultMinStakingDuration)
	defaultTxFee              = uint64(100)
)

type stakerStatus int

type versionsHolder struct {
	baseState State
}

func (h *versionsHolder) GetState(blkID ids.ID) (Chain, bool) {
	return h.baseState, blkID == h.baseState.GetLastAccepted()
}

func buildStateCtx() *snow.Context {
	ctx := snow.DefaultContextTest()
	ctx.NetworkID = constants.UnitTestID
	ctx.XChainID = xChainID
	ctx.CChainID = cChainID
	ctx.AVAXAssetID = avaxAssetID

	return ctx
}

func buildChainState(baseDB database.Database, trackedSubnets []ids.ID) (State, error) {
	cfg := defaultConfig()
	cfg.TrackedSubnets.Add(trackedSubnets...)

	ctx := buildStateCtx()

	genesisBytes, err := buildGenesisTest(ctx)
	if err != nil {
		return nil, err
	}

	rewardsCalc := reward.NewCalculator(cfg.RewardConfig)
	return NewMerkleState(
		baseDB,
		metrics.Noop,
		genesisBytes,
		cfg,
		ctx,
		prometheus.NewRegistry(),
		rewardsCalc,
		&utils.Atomic[bool]{},
	)
}

func defaultConfig() *config.Config {
	vdrs := validators.NewManager()
	primaryVdrs := validators.NewSet()
	_ = vdrs.Add(constants.PrimaryNetworkID, primaryVdrs)
	return &config.Config{
		Chains:                 chains.TestManager,
		UptimeLockedCalculator: uptime.NewLockedCalculator(),
		Validators:             vdrs,
		TxFee:                  defaultTxFee,
		CreateSubnetTxFee:      100 * defaultTxFee,
		CreateBlockchainTxFee:  100 * defaultTxFee,
		MinValidatorStake:      5 * units.MilliAvax,
		MaxValidatorStake:      500 * units.MilliAvax,
		MinDelegatorStake:      1 * units.MilliAvax,
		MinStakeDuration:       defaultMinStakingDuration,
		MaxStakeDuration:       defaultMaxStakingDuration,
		RewardConfig: reward.Config{
			MaxConsumptionRate: .12 * reward.PercentDenominator,
			MinConsumptionRate: .10 * reward.PercentDenominator,
			MintingPeriod:      defaultMaxStakingDuration,
			SupplyCap:          720 * units.MegaAvax,
		},
		ApricotPhase3Time: defaultValidateEndTime,
		ApricotPhase5Time: defaultValidateEndTime,
		BanffTime:         defaultValidateEndTime,
		CortinaTime:       defaultValidateEndTime,
	}
}

func buildGenesisTest(ctx *snow.Context) ([]byte, error) {
	buildGenesisArgs := api.BuildGenesisArgs{
		NetworkID:     json.Uint32(constants.UnitTestID),
		AvaxAssetID:   ctx.AVAXAssetID,
		UTXOs:         nil, // no UTXOs in this genesis. Not relevant to package tests.
		Validators:    nil, // no validators in this genesis. Tests will handle them.
		Chains:        nil,
		Time:          json.Uint64(defaultGenesisTime.Unix()),
		InitialSupply: json.Uint64(360 * units.MegaAvax),
		Encoding:      formatting.Hex,
	}

	buildGenesisResponse := api.BuildGenesisReply{}
	platformvmSS := api.StaticService{}
	if err := platformvmSS.BuildGenesis(nil, &buildGenesisArgs, &buildGenesisResponse); err != nil {
		return nil, fmt.Errorf("problem while building platform chain's genesis state: %w", err)
	}

	genesisBytes, err := formatting.Decode(buildGenesisResponse.Encoding, buildGenesisResponse.Bytes)
	if err != nil {
		return nil, err
	}

	return genesisBytes, nil
}
