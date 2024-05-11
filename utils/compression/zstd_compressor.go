// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package compression

import (
	"fmt"
	"math"
)

var _ Compressor = (*zstdCompressor)(nil)

func NewZstdCompressor(maxSize int64) (Compressor, error) {
	if maxSize == math.MaxInt64 {
		// "Decompress" creates "io.LimitReader" with max size + 1:
		// if the max size + 1 overflows, "io.LimitReader" reads nothing
		// returning 0 byte for the decompress call
		// require max size < math.MaxInt64 to prevent int64 overflows
		return nil, ErrInvalidMaxSizeCompressor
	}

	return &zstdCompressor{
		maxSize: maxSize,
	}, nil
}

type zstdCompressor struct {
	maxSize int64
	noCompressor
}

func (z *zstdCompressor) Compress(msg []byte) ([]byte, error) {
	if int64(len(msg)) > z.maxSize {
		return nil, fmt.Errorf("%w: (%d) > (%d)", ErrMsgTooLarge, len(msg), z.maxSize)
	}
	return z.noCompressor.Compress(msg)
}

func (z *zstdCompressor) Decompress(msg []byte) ([]byte, error) {
	return z.noCompressor.Decompress(msg)
}
