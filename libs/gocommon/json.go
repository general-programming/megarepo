package gocommon

import (
	"bytes"

	"github.com/bytedance/sonic/decoder"
	"github.com/bytedance/sonic/encoder"
)

func Unmarshal(data string, v interface{}) error {
	dec := decoder.NewDecoder(data)
	err := dec.Decode(v)

	return err
}

func Marshal(data interface{}) ([]byte, error) {
	buf := bytes.NewBuffer(nil)
	encoder := encoder.NewStreamEncoder(buf)
	err := encoder.Encode(data)

	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
