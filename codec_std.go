//go:build std_json

package web

import "encoding/json"

var (
	jsonMarshal   = json.Marshal
	jsonUnmarshal = json.Unmarshal
)
