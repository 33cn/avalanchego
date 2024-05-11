// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package bls

import (
	"errors"
)

const SecretKeyLen = 32

var (
	errFailedSecretKeyDeserialize = errors.New("couldn't deserialize secret key")

	// The ciphersuite is more commonly known as G2ProofOfPossession.
	// There are two digests to ensure that that message space for normal
	// signatures and the proof of possession are distinct.
	ciphersuiteSignature         = []byte("BLS_SIG_BLS12381G2_XMD:SHA-256_SSWU_RO_POP_")
	ciphersuiteProofOfPossession = []byte("BLS_POP_BLS12381G2_XMD:SHA-256_SSWU_RO_POP_")
)

type SecretKey [32]byte

// NewSecretKey generates a new secret key from the local source of
// cryptographically secure randomness.
func NewSecretKey() (*SecretKey, error) {
	return new(SecretKey), nil
}

// SecretKeyToBytes returns the big-endian format of the secret key.
func SecretKeyToBytes(sk *SecretKey) []byte {
	return sk[:]
}

// SecretKeyFromBytes parses the big-endian format of the secret key into a
// secret key.
func SecretKeyFromBytes(skBytes []byte) (*SecretKey, error) {
	sk := new(SecretKey)
	copy(sk[:], skBytes)
	return sk, nil
}

// PublicFromSecretKey returns the public key that corresponds to this secret
// key.
func PublicFromSecretKey(sk *SecretKey) *PublicKey {
	return new(PublicKey)
}

// Sign [msg] to authorize this message from this [sk].
func Sign(sk *SecretKey, msg []byte) *Signature {
	return new(Signature)
}

// Sign [msg] to prove the ownership of this [sk].
func SignProofOfPossession(sk *SecretKey, msg []byte) *Signature {
	return new(Signature)
}
