package gocommon

import (
	"fmt"

	"github.com/klauspost/compress/zstd"
)

var (
	ZSTDEncoder *zstd.Encoder
	ZSTDDecoder *zstd.Decoder
)

func init() {
	var err error

	// Create the encoder
	ZSTDEncoder, err = zstd.NewWriter(nil)
	if err != nil {
		panic(fmt.Sprintf("Failed to create ZSTD encoder: %s", err))
	}

	// Create the decoder
	ZSTDDecoder, err = zstd.NewReader(nil)
	if err != nil {
		panic(fmt.Sprintf("Failed to create ZSTD decoder: %s", err))
	}
}

func Decompress(src []byte) ([]byte, error) {
	return ZSTDDecoder.DecodeAll(src, make([]byte, 0, len(src)))
}

func Compress(src []byte) []byte {
	return ZSTDEncoder.EncodeAll(src, make([]byte, 0, len(src)))
}
