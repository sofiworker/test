package web

import (
	"net/http"
	"reflect"

	"example.com/web/httperr"
)

// Handler is the internal lowest common denominator every route is compiled
// down to. Middleware, the router and the response pipeline all speak this
// single signature. Users rarely write it directly — see Raw.
type Handler func(*Ctx) error

// This file implements the endpoint-as-data design (Tapir lineage, see
// PROPOSAL.md v3): routes are first-class values. The description (method,
// path, input contract, output contract) is separated from the logic (a
// plain function). Everything is compiled with direct calls — there is no
// reflection anywhere in this package.
//
// Go lacks generic methods, so the type-parameter transitions (introducing
// I and O) are free functions; methods appear only where the type parameters
// are already fixed. The flat, fully inferred entry point is Handle.

// Builder carries method and path; it declares no types yet.
type Builder struct {
	method string
	path   string
}

// Get starts a GET endpoint description.
func Get(path string) *Builder { return &Builder{method: "GET", path: path} }

// Post starts a POST endpoint description.
func Post(path string) *Builder { return &Builder{method: "POST", path: path} }

// Put starts a PUT endpoint description.
func Put(path string) *Builder { return &Builder{method: "PUT", path: path} }

// Patch starts a PATCH endpoint description.
func Patch(path string) *Builder { return &Builder{method: "PATCH", path: path} }

// Delete starts a DELETE endpoint description.
func Delete(path string) *Builder { return &Builder{method: "DELETE", path: path} }

// Head starts a HEAD endpoint description.
func Head(path string) *Builder { return &Builder{method: "HEAD", path: path} }

// Options starts an OPTIONS endpoint description.
func Options(path string) *Builder { return &Builder{method: "OPTIONS", path: path} }

// In describes how the input value I is born from a request. It is the INPUT
// half of an endpoint's contract.
type In[I any] interface {
	build(*Ctx) (I, error)
}

// InFunc adapts an explicit constructor into an input descriptor. This is
// the universal escape hatch: any combination of path/query/header/body and
// any arity — the 100-parameter route is one struct returned by one
// function.
//
// Errors returned by the constructor are handler errors: wrap them in
// httperr.BadRequest (or any typed status) yourself, nothing is inferred.
func InFunc[I any](fn func(Req) (I, error)) In[I] { return inFunc[I]{fn: fn} }

type inFunc[I any] struct {
	fn   func(Req) (I, error)
	meta OpMeta
}

func (f inFunc[I]) build(c *Ctx) (I, error) { return f.fn(Req{c: c}) }

// Describe returns the OpenAPI metadata of this descriptor. User-defined
// InFunc descriptors carry none.
func (f inFunc[I]) Describe() OpMeta { return f.meta }

// opMetaCarrier lets an already-merged OpMeta flow through describeOf.
type opMetaCarrier struct{ m OpMeta }

func (c opMetaCarrier) Describe() OpMeta { return c.m }

// None is the input type of endpoints that take no input.
type None struct{}

// NoIn declares an empty input contract.
func NoIn() In[None] {
	return InFunc(func(Req) (None, error) { return None{}, nil })
}

// PathInt64 declares one int64 path parameter as the input contract. Parse
// failures are automatically mapped to 400.
func PathInt64(name string) In[int64] {
	return inFunc[int64]{
		fn: func(r Req) (int64, error) {
			v, err := r.Path().Int64(name)
			if err != nil {
				return 0, httperr.BadRequest(err)
			}
			return v, nil
		},
		meta: OpMeta{Parameters: []*Parameter{{
			Name: name, In: "path", Required: true,
			Schema: &Schema{Type: "integer", Format: "int64"},
		}}},
	}
}

// PathString declares one string path parameter as the input contract.
func PathString(name string) In[string] {
	return inFunc[string]{
		fn: func(r Req) (string, error) {
			v, err := r.Path().String(name)
			if err != nil {
				return "", httperr.BadRequest(err)
			}
			return v, nil
		},
		meta: OpMeta{Parameters: []*Parameter{{
			Name: name, In: "path", Required: true,
			Schema: &Schema{Type: "string"},
		}}},
	}
}

// PathBool declares one bool path parameter as the input contract.
func PathBool(name string) In[bool] {
	return inFunc[bool]{
		fn: func(r Req) (bool, error) {
			v, err := r.Path().Bool(name)
			if err != nil {
				return false, httperr.BadRequest(err)
			}
			return v, nil
		},
		meta: OpMeta{Parameters: []*Parameter{{
			Name: name, In: "path", Required: true,
			Schema: &Schema{Type: "boolean"},
		}}},
	}
}

// QueryInt declares one int query parameter as the input contract.
func QueryInt(name string) In[int] {
	return inFunc[int]{
		fn: func(r Req) (int, error) {
			v, err := r.Query().Int(name)
			if err != nil {
				return 0, httperr.BadRequest(err)
			}
			return v, nil
		},
		meta: OpMeta{Parameters: []*Parameter{{
			Name: name, In: "query",
			Schema: &Schema{Type: "integer"},
		}}},
	}
}

// QueryIntDefault declares one int query parameter with a default value.
func QueryIntDefault(name string, def int) In[int] {
	return inFunc[int]{
		fn: func(r Req) (int, error) {
			return r.Query().IntDefault(name, def), nil
		},
		meta: OpMeta{Parameters: []*Parameter{{
			Name: name, In: "query",
			Schema: &Schema{Type: "integer"},
		}}},
	}
}

// QueryString declares one string query parameter as the input contract.
func QueryString(name string) In[string] {
	return inFunc[string]{
		fn: func(r Req) (string, error) { return r.Query().String(name), nil },
		meta: OpMeta{Parameters: []*Parameter{{
			Name: name, In: "query",
			Schema: &Schema{Type: "string"},
		}}},
	}
}

// BodyJSON declares the request body, decoded as T, as the input contract.
// Decode failures are automatically mapped to 400.
func BodyJSON[T any]() In[T] {
	return inFunc[T]{
		fn: func(r Req) (T, error) {
			v, err := DecodeBody[T](r)
			if err != nil {
				return v, httperr.BadRequest(err)
			}
			return v, nil
		},
		meta: OpMeta{RequestBody: &RequestBody{
			Required: true,
			Content: map[string]*MediaType{
				"application/json": {Schema: schemaOfType(reflect.TypeOf((*T)(nil)).Elem())},
			},
		}},
	}
}

// Endpoint is a description with an input contract attached.
type Endpoint[I any] struct {
	b  *Builder
	in In[I]
}

// WithIn attaches an input descriptor to a builder (free function: Go
// methods cannot introduce type parameters).
func WithIn[I any](b *Builder, i In[I]) *Endpoint[I] {
	return &Endpoint[I]{b: b, in: i}
}

// Bound is a description with both input and output contracts attached.
type Bound[I, O any] struct {
	e   *Endpoint[I]
	out Renderer[O]
}

// WithOut attaches an output renderer to an endpoint (free function, same
// reason as WithIn).
func WithOut[I, O any](e *Endpoint[I], r Renderer[O]) *Bound[I, O] {
	return &Bound[I, O]{e: e, out: r}
}

// Handle binds the business logic to the contract and returns a Route. The
// method is legal here because I and O are already fixed.
func (bd *Bound[I, O]) Handle(fn func(I) (O, error)) *Route {
	h := func(c *Ctx) error {
		in, err := bd.e.in.build(c)
		if err != nil {
			return err
		}
		o, err := fn(in)
		if err != nil {
			return err
		}
		return render(c, o, bd.out)
	}
	return &Route{
		method:  bd.e.b.method,
		path:    bd.e.b.path,
		h:       h,
		inMeta:  bd.e.in,
		outMeta: bd.out,
	}
}

// Handle is the flat, fully inferred entry point: method+path, input
// contract, output contract and the plain handler function.
func Handle[I, O any](b *Builder, in In[I], out Renderer[O], fn func(I) (O, error)) *Route {
	return (&Bound[I, O]{e: &Endpoint[I]{b: b, in: in}, out: out}).Handle(fn)
}

// Route is a compiled endpoint: an immutable first-class value that can be
// mounted on any App, reused across apps, and wrapped with middleware.
type Route struct {
	method string
	path   string
	h      Handler
	mws    []Middleware

	inMeta  any // retained for introspection (OpenAPI generation, M3)
	outMeta any
}

// With returns a copy of the route wrapped with extra middleware. Route
// middleware runs inside the app's global stack, in With order.
func (r *Route) With(mws ...Middleware) *Route {
	n := *r
	n.mws = append(append([]Middleware(nil), r.mws...), mws...)
	return &n
}

// Raw wraps an internal Handler as a Route: the escape hatch for hand-rolled
// hot paths, streaming and protocol upgrades. Zero adaptation.
func Raw(method, path string, h Handler) *Route {
	return &Route{method: method, path: path, h: h}
}

// ---- 注册层糖：方法 + 输出契约合一（内部只是 Handle 的一行包装，零新机制）----
//
// 这些函数让 JSON/文本这类 90% 场景一行写完，同时不隐藏任何契约：输出类型由
// handler 推断、渲染器由函数名显式声明。需要自定义渲染器或状态码时退回
// web.Handle 全契约形式。

// GetJSON declares a GET endpoint rendering its output as JSON 200.
func GetJSON[I, O any](path string, in In[I], fn func(I) (O, error)) *Route {
	return Handle(Get(path), in, JSON[O](), fn)
}

// PostJSON declares a POST endpoint rendering its output as JSON 200.
func PostJSON[I, O any](path string, in In[I], fn func(I) (O, error)) *Route {
	return Handle(Post(path), in, JSON[O](), fn)
}

// PutJSON declares a PUT endpoint rendering its output as JSON 200.
func PutJSON[I, O any](path string, in In[I], fn func(I) (O, error)) *Route {
	return Handle(Put(path), in, JSON[O](), fn)
}

// PatchJSON declares a PATCH endpoint rendering its output as JSON 200.
func PatchJSON[I, O any](path string, in In[I], fn func(I) (O, error)) *Route {
	return Handle(Patch(path), in, JSON[O](), fn)
}

// DeleteJSON declares a DELETE endpoint rendering its output as JSON 200.
func DeleteJSON[I, O any](path string, in In[I], fn func(I) (O, error)) *Route {
	return Handle(Delete(path), in, JSON[O](), fn)
}

// CreatedJSON declares a POST endpoint rendering its output as JSON 201.
func CreatedJSON[I, O any](path string, in In[I], fn func(I) (O, error)) *Route {
	return Handle(Post(path), in, Status(http.StatusCreated, JSON[O]()), fn)
}

// GetText declares a GET endpoint rendering a string as text/plain 200.
func GetText[I any](path string, in In[I], fn func(I) (string, error)) *Route {
	return Handle(Get(path), in, Text(), fn)
}

// PostText declares a POST endpoint rendering a string as text/plain 200.
func PostText[I any](path string, in In[I], fn func(I) (string, error)) *Route {
	return Handle(Post(path), in, Text(), fn)
}

// ---- 无输入（0）变体：消除 web.None 样板。这是唯一的 0 特例，不是元数家族。----

// GetJSON0 declares a GET endpoint with no input contract.
func GetJSON0[O any](path string, fn func() (O, error)) *Route {
	return Handle(Get(path), NoIn(), JSON[O](), func(_ None) (O, error) { return fn() })
}

// PostJSON0 declares a POST endpoint with no input contract.
func PostJSON0[O any](path string, fn func() (O, error)) *Route {
	return Handle(Post(path), NoIn(), JSON[O](), func(_ None) (O, error) { return fn() })
}

// CreatedJSON0 declares a POST endpoint with no input contract, status 201.
func CreatedJSON0[O any](path string, fn func() (O, error)) *Route {
	return Handle(Post(path), NoIn(), Status(http.StatusCreated, JSON[O]()), func(_ None) (O, error) { return fn() })
}

// GetText0 declares a GET text endpoint with no input contract.
func GetText0(path string, fn func() (string, error)) *Route {
	return Handle(Get(path), NoIn(), Text(), func(_ None) (string, error) { return fn() })
}

// PostText0 declares a POST text endpoint with no input contract.
func PostText0(path string, fn func() (string, error)) *Route {
	return Handle(Post(path), NoIn(), Text(), func(_ None) (string, error) { return fn() })
}

// ---- 输入组合子：path + body 等任意来源的拼装（零反射，类型全显式）----

// Pair is the composed input of two sources. Go has no tuple type; the pair
// struct is its explicit substitute (the axum tuple idea, spelled out).
type Pair[A, B any] struct {
	First  A
	Second B
}

// Triple is the composed input of three sources.
type Triple[A, B, C any] struct {
	First  A
	Second B
	Third  C
}

// All composes two input descriptors into one: both are built in order, and
// the first error short-circuits. Nest for more sources:
// All(All(a, b), c) — or use All3.
func All[A, B any](a In[A], b In[B]) In[Pair[A, B]] {
	return inFunc[Pair[A, B]]{
		fn: func(r Req) (Pair[A, B], error) {
			va, err := a.build(r.c)
			if err != nil {
				return Pair[A, B]{}, err
			}
			vb, err := b.build(r.c)
			if err != nil {
				return Pair[A, B]{}, err
			}
			return Pair[A, B]{First: va, Second: vb}, nil
		},
		meta: mergeMeta(a, b),
	}
}

// describeOf returns the OpenAPI metadata of a descriptor, or empty when it
// carries none (user-defined InFunc descriptors).
func describeOf(in any) OpMeta {
	if d, ok := in.(Describe); ok {
		return d.Describe()
	}
	return OpMeta{}
}

// mergeMeta merges the OpenAPI metadata of two input descriptors.
func mergeMeta(a, b any) OpMeta {
	ma, mb := describeOf(a), describeOf(b)
	var m OpMeta
	m.Parameters = append(append([]*Parameter(nil), ma.Parameters...), mb.Parameters...)
	if ma.RequestBody != nil {
		m.RequestBody = ma.RequestBody
	} else if mb.RequestBody != nil {
		m.RequestBody = mb.RequestBody
	}
	return m
}

// All3 composes three input descriptors into a Triple.
func All3[A, B, C any](a In[A], b In[B], c In[C]) In[Triple[A, B, C]] {
	ab := mergeMeta(a, b)
	meta := mergeMeta(opMetaCarrier{ab}, c)
	return inFunc[Triple[A, B, C]]{
		fn: func(r Req) (Triple[A, B, C], error) {
			va, err := a.build(r.c)
			if err != nil {
				return Triple[A, B, C]{}, err
			}
			vb, err := b.build(r.c)
			if err != nil {
				return Triple[A, B, C]{}, err
			}
			vc, err := c.build(r.c)
			if err != nil {
				return Triple[A, B, C]{}, err
			}
			return Triple[A, B, C]{First: va, Second: vb, Third: vc}, nil
		},
		meta: meta,
	}
}
