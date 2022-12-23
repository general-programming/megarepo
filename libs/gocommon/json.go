package gocommon

import (
	"github.com/bytedance/sonic/decoder"
)

func Unmarshal(data string, v interface{}) error {
	dec := decoder.NewDecoder(data)
	err := dec.Decode(v)

	return err
}
