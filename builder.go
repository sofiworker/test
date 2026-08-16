package web

import (
	"strings"
)

// 链式构建器（Go <1.27 形态，吸收 ghttp 的 Route[I,O] 起点思路）：
// 契约在起点一次声明（类型由描述器推断，无需显式写类型参数），
// 之后每个方法都是固定类型参数的普通方法，链式可用。
//
// Go 1.27 泛型方法落地后的标准形态见 builder_go127.go：
// app.Route().GET(path).To[I,O](in, out, fn)。

// OpDoc is per-operation OpenAPI metadata. All fields are optional: whatever
// the user does not write is inferred from the route itself (summary
// defaults to "METHOD path"), so documentation costs nothing by default.
type OpDoc struct {
	Summary     string
	Description string
	Tags        []string
	OperationID string
	Deprecated  bool
}

// DocOption mutates an OpDoc.
type DocOption func(*OpDoc)

// Summary sets the operation summary.
func Summary(s string) DocOption { return func(d *OpDoc) { d.Summary = s } }

// Description sets the operation description.
func Description(s string) DocOption { return func(d *OpDoc) { d.Description = s } }

// Tags attaches OpenAPI tags.
func Tags(ts ...string) DocOption { return func(d *OpDoc) { d.Tags = ts } }

// OperationID sets an explicit operation id.
func OperationID(id string) DocOption { return func(d *OpDoc) { d.OperationID = id } }

// Deprecated marks the operation deprecated.
func Deprecated() DocOption { return func(d *OpDoc) { d.Deprecated = true } }

// inferOperationID builds an operation id from method and path. Parameter
// braces are KEPT so that /users/{id} and a hypothetical /users/id never
// collide.
func inferOperationID(method, path string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	for _, r := range path {
		if r == '/' {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
