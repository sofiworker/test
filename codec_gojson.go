//go:build go_json

package web

import gojson "github.com/goccy/go-json"

var (
	jsonMarshal   = gojson.Marshal
	jsonUnmarshal = gojson.Unmarshal
)
