package gocommon

import (
	"fmt"

	"github.com/klauspost/compress/zstd"
)

var ZSTDEncoder *zstd.Encoder

func init() {
	var err error

	// Create the encoder
	ZSTDEncoder, err = zstd.NewWriter(nil)
	if err != nil {
		panic(fmt.Sprintf("Failed to create ZSTD encoder: %s", err))
	}
}

func Compress(src []byte) []byte {
	return ZSTDEncoder.EncodeAll(src, make([]byte, 0, len(src)))
}
