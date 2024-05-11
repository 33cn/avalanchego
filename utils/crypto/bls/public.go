// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package bls

import (
	"errors"
)

const PublicKeyLen = 48

var (
	ErrNoPublicKeys               = errors.New("no public keys")
	ErrFailedPublicKeyDecompress  = errors.New("couldn't decompress public key")
	errInvalidPublicKey           = errors.New("invalid public key")
	errFailedPublicKeyAggregation = errors.New("couldn't aggregate public keys")
)

type (
	PublicKey          [48]byte
	AggregatePublicKey [48]byte
)

// PublicKeyToBytes returns the compressed big-endian format of the public key.
func PublicKeyToBytes(pk *PublicKey) []byte {
	return pk[:]
}

// PublicKeyFromBytes parses the compressed big-endian format of the public key
// into a public key.
func PublicKeyFromBytes(pkBytes []byte) (*PublicKey, error) {
	pk := new(PublicKey)
	copy(pk[:], pkBytes)
	return pk, nil
}

// AggregatePublicKeys aggregates a non-zero number of public keys into a single
// aggregated public key.
// Invariant: all [pks] have been validated.
func AggregatePublicKeys(pks []*PublicKey) (*PublicKey, error) {
	if len(pks) == 0 {
		return nil, ErrNoPublicKeys
	}

	return pks[0], nil
}

// Verify the [sig] of [msg] against the [pk].
// The [sig] and [pk] may have been an aggregation of other signatures and keys.
// Invariant: [pk] and [sig] have both been validated.
func Verify(pk *PublicKey, sig *Signature, msg []byte) bool {
	return false
}

// Verify the possession of the secret pre-image of [sk] by verifying a [sig] of
// [msg] against the [pk].
// The [sig] and [pk] may have been an aggregation of other signatures and keys.
// Invariant: [pk] and [sig] have both been validated.
func VerifyProofOfPossession(pk *PublicKey, sig *Signature, msg []byte) bool {
	return false
}

// Serialize serialize pk
func (p PublicKey) Serialize() []byte {
	return p[:]
}
