//go:build !std_json && !jsoniter && !(sonic && avx && (linux || windows || darwin) && amd64)

package web

// 默认 JSON 引擎：goccy/go-json —— 纯 Go、stdlib 兼容、无汇编依赖，
// 任何 Go 版本可用；小对象编码明显快于 encoding/json。
// 可选构建标签（与 gin 同机制）：std_json 回退标准库、jsoniter、
// sonic（需 avx+amd64）。所有 JSON 出口都经这两个变量。

import gojson "github.com/goccy/go-json"

var (
	jsonMarshal   = gojson.Marshal
	jsonUnmarshal = gojson.Unmarshal
)
