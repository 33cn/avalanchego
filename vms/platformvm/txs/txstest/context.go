// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package txstest

import (
	"time"

	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/vms/platformvm/config"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs/fee"
	"github.com/ava-labs/avalanchego/wallet/chain/p/builder"
)

func newContext(
	ctx *snow.Context,
	cfg *config.Config,
	timestamp time.Time,
) *builder.Context {
	var (
		isEActive = cfg.UpgradeConfig.IsEActivated(timestamp)
		feeCfg    = fee.GetDynamicConfig(isEActive)
		feeCalc   = fee.NewCalculator(cfg.StaticFeeConfig, cfg.UpgradeConfig)
	)
	feeCalc.Update(timestamp, feeCfg.FeeRate, feeCfg.BlockMaxComplexity)

	var (
		createSubnetFee, _ = feeCalc.ComputeFee(&txs.CreateSubnetTx{}, nil)
		createChainFee, _  = feeCalc.ComputeFee(&txs.CreateChainTx{}, nil)
	)

	return &builder.Context{
		NetworkID:                     ctx.NetworkID,
		AVAXAssetID:                   ctx.AVAXAssetID,
		BaseTxFee:                     cfg.StaticFeeConfig.TxFee,
		CreateSubnetTxFee:             createSubnetFee,
		TransformSubnetTxFee:          cfg.StaticFeeConfig.TransformSubnetTxFee,
		CreateBlockchainTxFee:         createChainFee,
		AddPrimaryNetworkValidatorFee: cfg.StaticFeeConfig.AddPrimaryNetworkValidatorFee,
		AddPrimaryNetworkDelegatorFee: cfg.StaticFeeConfig.AddPrimaryNetworkDelegatorFee,
		AddSubnetValidatorFee:         cfg.StaticFeeConfig.AddSubnetValidatorFee,
		AddSubnetDelegatorFee:         cfg.StaticFeeConfig.AddSubnetDelegatorFee,
	}
}
