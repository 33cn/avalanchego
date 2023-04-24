// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package block

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/crypto/bls"
	"github.com/ava-labs/avalanchego/utils/units"
)

func equal(require *require.Assertions, chainID ids.ID, pk *bls.PublicKey, want, have SignedBlock) {
	require.Equal(want.ID(), have.ID())
	require.Equal(want.ParentID(), have.ParentID())
	require.Equal(want.PChainHeight(), have.PChainHeight())
	require.Equal(want.Timestamp(), have.Timestamp())
	require.Equal(want.Block(), have.Block())
	require.Equal(want.Proposer(), have.Proposer())
	require.Equal(want.Bytes(), have.Bytes())
	require.Equal(want.Verify(false, chainID, pk), have.Verify(false, chainID, pk))
	require.Equal(want.Verify(true, chainID, pk), have.Verify(true, chainID, pk))
}

func TestVerifyNoCertWithSignature(t *testing.T) {
	parentID := ids.ID{1}
	timestamp := time.Unix(123, 0)
	pChainHeight := uint64(2)
	innerBlockBytes := []byte{3}

	require := require.New(t)

	builtBlockIntf, err := BuildUnsigned(parentID, timestamp, pChainHeight, innerBlockBytes)
	require.NoError(err)

	builtBlock := builtBlockIntf.(*statelessCertSignedBlock)
	builtBlock.Signature = []byte{0}

	err = builtBlock.Verify(false, ids.Empty, nil)
	require.ErrorIs(err, errUnexpectedProposer)

	err = builtBlock.Verify(true, ids.Empty, nil)
	require.ErrorIs(err, errMissingProposer)
}

func TestBlockSizeLimit(t *testing.T) {
	require := require.New(t)

	parentID := ids.ID{1}
	timestamp := time.Unix(123, 0)
	pChainHeight := uint64(2)
	innerBlockBytes := bytes.Repeat([]byte{0}, 270*units.KiB)

	// with the large limit, it should be able to build large blocks
	_, err := BuildUnsigned(parentID, timestamp, pChainHeight, innerBlockBytes)
	require.NoError(err)
}
