//go:build jsoniter

package web

import jsoniter "github.com/json-iterator/go"

var (
	jsonMarshal   = jsoniter.ConfigCompatibleWithStandardLibrary.Marshal
	jsonUnmarshal = jsoniter.ConfigCompatibleWithStandardLibrary.Unmarshal
)
