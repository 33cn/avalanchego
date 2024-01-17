// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package fees

import (
	"errors"
	"fmt"
	"time"

	"github.com/ava-labs/avalanchego/codec"
	"github.com/ava-labs/avalanchego/vms/avm/config"
	"github.com/ava-labs/avalanchego/vms/avm/fxs"
	"github.com/ava-labs/avalanchego/vms/avm/txs"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/fees"
)

var (
	_ txs.Visitor = (*Calculator)(nil)

	errFailedFeeCalculation          = errors.New("failed fee calculation")
	errFailedConsumedUnitsCumulation = errors.New("failed cumulating consumed units")
)

type Calculator struct {
	// setup, to be filled before visitor methods are called
	FeeManager *fees.Manager
	Codec      codec.Manager
	Config     *config.Config
	ChainTime  time.Time

	// inputs, to be filled before visitor methods are called
	Credentials []*fxs.FxCredential

	// outputs of visitor execution
	Fee uint64
}

func (fc *Calculator) BaseTx(tx *txs.BaseTx) error {
	if !fc.Config.IsEForkActivated(fc.ChainTime) {
		fc.Fee = fc.Config.TxFee
		return nil
	}

	consumedUnits, err := fc.commonConsumedUnits(tx, tx.Outs, tx.Ins)
	if err != nil {
		return err
	}

	return fc.AddFeesFor(consumedUnits)
}

func (fc *Calculator) CreateAssetTx(tx *txs.CreateAssetTx) error {
	if !fc.Config.IsEForkActivated(fc.ChainTime) {
		fc.Fee = fc.Config.CreateAssetTxFee
		return nil
	}

	consumedUnits, err := fc.commonConsumedUnits(tx, tx.Outs, tx.Ins)
	if err != nil {
		return err
	}

	return fc.AddFeesFor(consumedUnits)
}

func (fc *Calculator) OperationTx(tx *txs.OperationTx) error {
	if !fc.Config.IsEForkActivated(fc.ChainTime) {
		fc.Fee = fc.Config.TxFee
		return nil
	}

	consumedUnits, err := fc.commonConsumedUnits(tx, tx.Outs, tx.Ins)
	if err != nil {
		return err
	}

	return fc.AddFeesFor(consumedUnits)
}

func (fc *Calculator) ImportTx(tx *txs.ImportTx) error {
	if !fc.Config.IsEForkActivated(fc.ChainTime) {
		fc.Fee = fc.Config.TxFee
		return nil
	}

	ins := make([]*avax.TransferableInput, len(tx.Ins)+len(tx.ImportedIns))
	copy(ins, tx.Ins)
	copy(ins[len(tx.Ins):], tx.ImportedIns)

	consumedUnits, err := fc.commonConsumedUnits(tx, tx.Outs, ins)
	if err != nil {
		return err
	}

	return fc.AddFeesFor(consumedUnits)
}

func (fc *Calculator) ExportTx(tx *txs.ExportTx) error {
	if !fc.Config.IsEForkActivated(fc.ChainTime) {
		fc.Fee = fc.Config.TxFee
		return nil
	}

	outs := make([]*avax.TransferableOutput, len(tx.Outs)+len(tx.ExportedOuts))
	copy(outs, tx.Outs)
	copy(outs[len(tx.Outs):], tx.ExportedOuts)

	consumedUnits, err := fc.commonConsumedUnits(tx, outs, tx.Ins)
	if err != nil {
		return err
	}

	return fc.AddFeesFor(consumedUnits)
}

func (fc *Calculator) commonConsumedUnits(
	uTx txs.UnsignedTx,
	allOuts []*avax.TransferableOutput,
	allIns []*avax.TransferableInput,
) (fees.Dimensions, error) {
	var consumedUnits fees.Dimensions

	uTxSize, err := fc.Codec.Size(txs.CodecVersion, uTx)
	if err != nil {
		return consumedUnits, fmt.Errorf("couldn't calculate UnsignedTx marshal length: %w", err)
	}
	credsSize, err := fc.Codec.Size(txs.CodecVersion, fc.Credentials)
	if err != nil {
		return consumedUnits, fmt.Errorf("failed retrieving size of credentials: %w", err)
	}
	consumedUnits[fees.Bandwidth] = uint64(uTxSize + credsSize)

	inputDimensions, err := fees.GetInputsDimensions(fc.Codec, txs.CodecVersion, allIns)
	if err != nil {
		return consumedUnits, fmt.Errorf("failed retrieving size of inputs: %w", err)
	}
	inputDimensions[fees.Bandwidth] = 0 // inputs bandwidth is already accounted for above, so we zero it
	consumedUnits, err = fees.Add(consumedUnits, inputDimensions)
	if err != nil {
		return consumedUnits, fmt.Errorf("failed adding inputs: %w", err)
	}

	outputDimensions, err := fees.GetOutputsDimensions(fc.Codec, txs.CodecVersion, allOuts)
	if err != nil {
		return consumedUnits, fmt.Errorf("failed retrieving size of outputs: %w", err)
	}
	outputDimensions[fees.Bandwidth] = 0 // outputs bandwidth is already accounted for above, so we zero it
	consumedUnits, err = fees.Add(consumedUnits, outputDimensions)
	if err != nil {
		return consumedUnits, fmt.Errorf("failed adding outputs: %w", err)
	}

	return consumedUnits, nil
}

func (fc *Calculator) AddFeesFor(consumedUnits fees.Dimensions) error {
	boundBreached, dimension := fc.FeeManager.CumulateUnits(consumedUnits, fc.Config.BlockMaxConsumedUnits(fc.ChainTime))
	if boundBreached {
		return fmt.Errorf("%w: breached dimension %d", errFailedConsumedUnitsCumulation, dimension)
	}

	fee, err := fc.FeeManager.CalculateFee(consumedUnits)
	if err != nil {
		return fmt.Errorf("%w: %w", errFailedFeeCalculation, err)
	}

	fc.Fee = fee
	return nil
}
