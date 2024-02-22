// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package avm

import (
	"context"
	"fmt"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/utils/set"
	"github.com/ava-labs/avalanchego/vms/avm/config"
	"github.com/ava-labs/avalanchego/vms/avm/state"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/wallet/chain/x/backends"
)

var _ backends.Backend = (*Backend)(nil)

func NewBackend(
	feeAssetID ids.ID,
	ctx *snow.Context,
	cfg *config.Config,
	state state.State,
	atomicUTXOsMan avax.AtomicUTXOManager,
) *Backend {
	backendCtx := backends.NewContext(
		ctx.NetworkID,
		ctx.XChainID,
		feeAssetID,
		cfg.TxFee,
		cfg.CreateAssetTxFee,
	)
	return &Backend{
		Context:        backendCtx,
		xchainID:       ctx.XChainID,
		cfg:            cfg,
		state:          state,
		atomicUTXOsMan: atomicUTXOsMan,
	}
}

type Backend struct {
	backends.Context

	xchainID       ids.ID
	cfg            *config.Config
	addrs          set.Set[ids.ShortID]
	state          state.State
	atomicUTXOsMan avax.AtomicUTXOManager
}

func (b *Backend) ResetAddresses(addrs set.Set[ids.ShortID]) {
	b.addrs = addrs
}

func (b *Backend) UTXOs(_ context.Context, sourceChainID ids.ID) ([]*avax.UTXO, error) {
	if sourceChainID == b.xchainID {
		return avax.GetAllUTXOs(b.state, b.addrs)
	}

	atomicUTXOs, _, _, err := b.atomicUTXOsMan.GetAtomicUTXOs(sourceChainID, b.addrs, ids.ShortEmpty, ids.Empty, int(maxPageSize))
	return atomicUTXOs, err
}

func (b *Backend) GetUTXO(_ context.Context, chainID, utxoID ids.ID) (*avax.UTXO, error) {
	if chainID == b.xchainID {
		return b.state.GetUTXO(utxoID)
	}

	atomicUTXOs, _, _, err := b.atomicUTXOsMan.GetAtomicUTXOs(chainID, b.addrs, ids.ShortEmpty, ids.Empty, int(maxPageSize))
	if err != nil {
		return nil, fmt.Errorf("problem retrieving atomic UTXOs: %w", err)
	}
	for _, utxo := range atomicUTXOs {
		if utxo.InputID() == utxoID {
			return utxo, nil
		}
	}
	return nil, database.ErrNotFound
}
