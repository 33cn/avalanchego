// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package fees

import (
	"errors"
	"fmt"

	"github.com/ava-labs/avalanchego/codec"
	"github.com/ava-labs/avalanchego/utils/wrappers"
	"github.com/ava-labs/avalanchego/vms/avm/config"
	"github.com/ava-labs/avalanchego/vms/avm/fxs"
	"github.com/ava-labs/avalanchego/vms/avm/txs"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/fees"
	"github.com/ava-labs/avalanchego/vms/nftfx"
	"github.com/ava-labs/avalanchego/vms/propertyfx"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
)

var (
	_ txs.Visitor = (*Calculator)(nil)

	errFailedFeeCalculation          = errors.New("failed fee calculation")
	errFailedConsumedUnitsCumulation = errors.New("failed cumulating consumed units")
)

type Calculator struct {
	// setup, to be filled before visitor methods are called
	IsEUpgradeActive bool

	// Pre E-Upgrade inputs
	Config *config.Config

	// Post E-Upgrade inputs
	FeeManager       *fees.Manager
	ConsumedUnitsCap fees.Dimensions
	Codec            codec.Manager

	// common inputs
	Credentials []*fxs.FxCredential

	// outputs of visitor execution
	Fee uint64
}

func (fc *Calculator) BaseTx(tx *txs.BaseTx) error {
	if !fc.IsEUpgradeActive {
		fc.Fee = fc.Config.TxFee
		return nil
	}

	consumedUnits, err := fc.meterTx(tx, tx.Outs, tx.Ins)
	if err != nil {
		return err
	}

	_, err = fc.AddFeesFor(consumedUnits)
	return err
}

func (fc *Calculator) CreateAssetTx(tx *txs.CreateAssetTx) error {
	if !fc.IsEUpgradeActive {
		fc.Fee = fc.Config.CreateAssetTxFee
		return nil
	}

	consumedUnits, err := fc.meterTx(tx, tx.Outs, tx.Ins)
	if err != nil {
		return err
	}

	_, err = fc.AddFeesFor(consumedUnits)
	return err
}

func (fc *Calculator) OperationTx(tx *txs.OperationTx) error {
	if !fc.IsEUpgradeActive {
		fc.Fee = fc.Config.TxFee
		return nil
	}

	consumedUnits, err := fc.meterTx(tx, tx.Outs, tx.Ins)
	if err != nil {
		return err
	}

	_, err = fc.AddFeesFor(consumedUnits)
	return err
}

func (fc *Calculator) ImportTx(tx *txs.ImportTx) error {
	if !fc.IsEUpgradeActive {
		fc.Fee = fc.Config.TxFee
		return nil
	}

	ins := make([]*avax.TransferableInput, len(tx.Ins)+len(tx.ImportedIns))
	copy(ins, tx.Ins)
	copy(ins[len(tx.Ins):], tx.ImportedIns)

	consumedUnits, err := fc.meterTx(tx, tx.Outs, ins)
	if err != nil {
		return err
	}

	_, err = fc.AddFeesFor(consumedUnits)
	return err
}

func (fc *Calculator) ExportTx(tx *txs.ExportTx) error {
	if !fc.IsEUpgradeActive {
		fc.Fee = fc.Config.TxFee
		return nil
	}

	outs := make([]*avax.TransferableOutput, len(tx.Outs)+len(tx.ExportedOuts))
	copy(outs, tx.Outs)
	copy(outs[len(tx.Outs):], tx.ExportedOuts)

	consumedUnits, err := fc.meterTx(tx, outs, tx.Ins)
	if err != nil {
		return err
	}

	_, err = fc.AddFeesFor(consumedUnits)
	return err
}

func (fc *Calculator) meterTx(
	uTx txs.UnsignedTx,
	allOuts []*avax.TransferableOutput,
	allIns []*avax.TransferableInput,
) (fees.Dimensions, error) {
	var consumedUnits fees.Dimensions

	uTxSize, err := fc.Codec.Size(txs.CodecVersion, uTx)
	if err != nil {
		return consumedUnits, fmt.Errorf("couldn't calculate UnsignedTx marshal length: %w", err)
	}
	consumedUnits[fees.Bandwidth] = uint64(uTxSize)

	// meter credentials, one by one. Then account for the extra bytes needed to
	// serialize a slice of credentials (codec version bytes + slice size bytes)
	for i, cred := range fc.Credentials {
		var keysCount int
		switch c := cred.Credential.(type) {
		case *secp256k1fx.Credential:
			keysCount = len(c.Sigs)
		case *propertyfx.Credential:
			keysCount = len(c.Sigs)
		case *nftfx.Credential:
			keysCount = len(c.Sigs)
		default:
			return consumedUnits, fmt.Errorf("don't know how to calculate complexity of %T", cred)
		}
		credDimensions, err := fees.MeterCredential(fc.Codec, txs.CodecVersion, keysCount)
		if err != nil {
			return consumedUnits, fmt.Errorf("failed adding credential %d: %w", i, err)
		}
		consumedUnits, err = fees.Add(consumedUnits, credDimensions)
		if err != nil {
			return consumedUnits, fmt.Errorf("failed adding credentials: %w", err)
		}
	}
	consumedUnits[fees.Bandwidth] += wrappers.IntLen // length of the credentials slice
	consumedUnits[fees.Bandwidth] += codec.CodecVersionSize

	for _, in := range allIns {
		inputDimensions, err := fees.MeterInput(fc.Codec, txs.CodecVersion, in)
		if err != nil {
			return consumedUnits, fmt.Errorf("failed retrieving size of inputs: %w", err)
		}
		inputDimensions[fees.Bandwidth] = 0 // inputs bandwidth is already accounted for above, so we zero it
		consumedUnits, err = fees.Add(consumedUnits, inputDimensions)
		if err != nil {
			return consumedUnits, fmt.Errorf("failed adding inputs: %w", err)
		}
	}

	for _, out := range allOuts {
		outputDimensions, err := fees.MeterOutput(fc.Codec, txs.CodecVersion, out)
		if err != nil {
			return consumedUnits, fmt.Errorf("failed retrieving size of outputs: %w", err)
		}
		outputDimensions[fees.Bandwidth] = 0 // outputs bandwidth is already accounted for above, so we zero it
		consumedUnits, err = fees.Add(consumedUnits, outputDimensions)
		if err != nil {
			return consumedUnits, fmt.Errorf("failed adding outputs: %w", err)
		}
	}

	return consumedUnits, nil
}

func (fc *Calculator) AddFeesFor(consumedUnits fees.Dimensions) (uint64, error) {
	boundBreached, dimension := fc.FeeManager.CumulateUnits(consumedUnits, fc.ConsumedUnitsCap)
	if boundBreached {
		return 0, fmt.Errorf("%w: breached dimension %d", errFailedConsumedUnitsCumulation, dimension)
	}

	fee, err := fc.FeeManager.CalculateFee(consumedUnits)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", errFailedFeeCalculation, err)
	}

	fc.Fee += fee
	return fee, nil
}

func (fc *Calculator) RemoveFeesFor(unitsToRm fees.Dimensions) (uint64, error) {
	if err := fc.FeeManager.RemoveUnits(unitsToRm); err != nil {
		return 0, fmt.Errorf("failed removing units: %w", err)
	}

	fee, err := fc.FeeManager.CalculateFee(unitsToRm)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", errFailedFeeCalculation, err)
	}

	fc.Fee -= fee
	return fee, nil
}
