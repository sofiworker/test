package web

import "encoding/json"

// 框架自身仅依赖标准库 encoding/json；需要更高性能或特殊行为时，
// 经 UseJSONCodec 注入外部实现（如 goccy/go-json、sonic），框架不引入
// 任何第三方 JSON 依赖。

// MarshalFunc serializes a value to JSON bytes.
type MarshalFunc func(v any) ([]byte, error)

// UnmarshalFunc deserializes JSON bytes into a value.
type UnmarshalFunc func(data []byte, v any) error

var (
	jsonMarshal   MarshalFunc   = json.Marshal
	jsonUnmarshal UnmarshalFunc = json.Unmarshal
)

// UseJSONCodec injects an external JSON implementation. All renderers, body
// decoding, SSE and error envelopes go through the injected functions.
// Not safe for concurrent mutation: call it once during startup, before
// serving.
func UseJSONCodec(m MarshalFunc, u UnmarshalFunc) {
	jsonMarshal = m
	jsonUnmarshal = u
}
