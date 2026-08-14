//go:build sonic && avx && (linux || windows || darwin) && amd64

package web

import "github.com/bytedance/sonic"

var (
	jsonMarshal   = sonic.ConfigStd.Marshal
	jsonUnmarshal = sonic.ConfigStd.Unmarshal
)
