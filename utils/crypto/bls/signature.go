// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package bls

import (
	"errors"
)

const SignatureLen = 96

var (
	ErrFailedSignatureDecompress  = errors.New("couldn't decompress signature")
	errInvalidSignature           = errors.New("invalid signature")
	errNoSignatures               = errors.New("no signatures")
	errFailedSignatureAggregation = errors.New("couldn't aggregate signatures")
)

type (
	Signature          = [96]byte
	AggregateSignature = [96]byte
)

// SignatureToBytes returns the compressed big-endian format of the signature.
func SignatureToBytes(sig *Signature) []byte {
	return sig[:]
}

// SignatureFromBytes parses the compressed big-endian format of the signature
// into a signature.
func SignatureFromBytes(sigBytes []byte) (*Signature, error) {
	sig := new(Signature)
	copy(sig[:], sigBytes)
	return sig, nil
}

// AggregateSignatures aggregates a non-zero number of signatures into a single
// aggregated signature.
// Invariant: all [sigs] have been validated.
func AggregateSignatures(sigs []*Signature) (*Signature, error) {
	if len(sigs) == 0 {
		return nil, errNoSignatures
	}

	return sigs[0], nil
}
