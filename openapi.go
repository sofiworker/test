package web

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"example.com/web/httperr"
)

// OpenAPI 3.0 generation from mounted routes. Because endpoints are data —
// the contract lives in descriptor and renderer values — the document is
// produced by walking the route table. No reflection on the request path;
// type schemas are introspected once per document build, at startup.

// Schema is a minimal OpenAPI 3.0 schema object.
type Schema struct {
	Type        string             `json:"type,omitempty"`
	Format      string             `json:"format,omitempty"`
	Properties  map[string]*Schema `json:"properties,omitempty"`
	Items       *Schema            `json:"items,omitempty"`
	Required    []string           `json:"required,omitempty"`
	Description string             `json:"description,omitempty"`
	Minimum     *float64           `json:"minimum,omitempty"`
	Maximum     *float64           `json:"maximum,omitempty"`
	Enum        []string           `json:"enum,omitempty"`
}

// Constraint narrows a descriptor: it validates at request time (violations
// become 400) and mirrors itself into the OpenAPI schema. Constraints are
// explicit — no struct tags, no hidden rules.
type Constraint struct {
	min  *float64
	max  *float64
	enum []string
}

// Min requires numeric inputs to be >= v.
func Min(v float64) Constraint { return Constraint{min: &v} }

// Max requires numeric inputs to be <= v.
func Max(v float64) Constraint { return Constraint{max: &v} }

// Enum restricts string inputs to the listed values.
func Enum(vals ...string) Constraint { return Constraint{enum: vals} }

// applyConstraints mirrors constraints into a schema.
func applyConstraints(s *Schema, cs []Constraint) *Schema {
	for _, c := range cs {
		if c.min != nil {
			s.Minimum = c.min
		}
		if c.max != nil {
			s.Maximum = c.max
		}
		if len(c.enum) > 0 {
			s.Enum = c.enum
		}
	}
	return s
}

// checkConstraints validates a parsed value against constraints, mapping
// violations to 400.
func checkConstraints(name string, v any, cs []Constraint) error {
	for _, c := range cs {
		if err := c.check(name, v); err != nil {
			return httperr.BadRequest(err)
		}
	}
	return nil
}

func (c Constraint) check(name string, v any) error {
	if c.min != nil || c.max != nil {
		if f, ok := toFloat(v); ok {
			if c.min != nil && f < *c.min {
				return fmt.Errorf("web: parameter %q: %v is below minimum %v", name, v, *c.min)
			}
			if c.max != nil && f > *c.max {
				return fmt.Errorf("web: parameter %q: %v is above maximum %v", name, v, *c.max)
			}
		}
	}
	if len(c.enum) > 0 {
		if s, ok := v.(string); ok {
			for _, e := range c.enum {
				if s == e {
					return nil
				}
			}
			return fmt.Errorf("web: parameter %q: %q is not one of %v", name, s, c.enum)
		}
	}
	return nil
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

// Parameter is a path/query/header parameter description.
type Parameter struct {
	Name        string  `json:"name"`
	In          string  `json:"in"` // "path" | "query" | "header"
	Required    bool    `json:"required"`
	Description string  `json:"description,omitempty"`
	Schema      *Schema `json:"schema"`
}

// MediaType describes one content type of a request or response body.
type MediaType struct {
	Schema *Schema `json:"schema"`
}

// RequestBody describes a request body.
type RequestBody struct {
	Required bool                  `json:"required"`
	Content  map[string]*MediaType `json:"content"`
}

// OpMeta is the OpenAPI metadata carried by input descriptors.
type OpMeta struct {
	Parameters  []*Parameter
	RequestBody *RequestBody
}

// Describe is implemented by every built-in input descriptor. Composed
// descriptors (All/All3) merge the metadata of their parts; user-defined
// InFunc descriptors carry none (documented limitation until M3+).
type Describe interface {
	Describe() OpMeta
}

// Response is one response description.
type Response struct {
	Description string                `json:"description"`
	Content     map[string]*MediaType `json:"content,omitempty"`
}

// Operation is one method on one path.
type Operation struct {
	Parameters  []*Parameter         `json:"parameters,omitempty"`
	RequestBody *RequestBody         `json:"requestBody,omitempty"`
	Responses   map[string]*Response `json:"responses"`
}

// Info is the OpenAPI info object.
type Info struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

// OpenAPIDoc is the generated document.
type OpenAPIDoc struct {
	OpenAPI string                           `json:"openapi"`
	Info    Info                             `json:"info"`
	Paths   map[string]map[string]*Operation `json:"paths"`
}

// Doc builds an OpenAPI 3.0 document from every mounted route.
func (a *App) Doc(info Info) OpenAPIDoc {
	doc := OpenAPIDoc{
		OpenAPI: "3.0.3",
		Info:    info,
		Paths:   map[string]map[string]*Operation{},
	}
	for _, r := range a.routes {
		method := strings.ToLower(r.method)
		if doc.Paths[r.path] == nil {
			doc.Paths[r.path] = map[string]*Operation{}
		}
		op := &Operation{Responses: map[string]*Response{}}

		if d, ok := r.inMeta.(Describe); ok {
			m := d.Describe()
			op.Parameters = m.Parameters
			op.RequestBody = m.RequestBody
		}

		code, desc := 200, "OK"
		if rd, ok := r.outMeta.(interface{ StatusCode() int }); ok {
			code = rd.StatusCode()
		}
		resp := &Response{Description: desc}
		if s, ok := r.outMeta.(responseSchemer); ok {
			if sch := s.ResponseSchema(); sch != nil {
				ct := "application/json"
				if rd, ok := r.outMeta.(interface{ ContentType() string }); ok && rd.ContentType() != "" {
					ct = rd.ContentType()
				}
				resp.Content = map[string]*MediaType{ct: {Schema: sch}}
			}
		}
		op.Responses[fmt.Sprint(code)] = resp
		op.Responses["400"] = &Response{Description: "Bad request"}
		op.Responses["404"] = &Response{Description: "Not found"}
		op.Responses["500"] = &Response{Description: "Internal server error"}

		doc.Paths[r.path][method] = op
	}
	return doc
}

// responseSchemer is implemented by built-in renderers.
type responseSchemer interface {
	ResponseSchema() *Schema
}

// schemaOfType maps a Go type to an OpenAPI schema. Called at document build
// time (startup), never on the request path.
func schemaOfType(t reflect.Type) *Schema {
	switch t.Kind() {
	case reflect.String:
		return &Schema{Type: "string"}
	case reflect.Bool:
		return &Schema{Type: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return &Schema{Type: "integer", Format: t.Kind().String()}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &Schema{Type: "integer"}
	case reflect.Float32:
		return &Schema{Type: "number", Format: "float"}
	case reflect.Float64:
		return &Schema{Type: "number", Format: "double"}
	case reflect.Pointer:
		return schemaOfType(t.Elem())
	case reflect.Slice:
		return &Schema{Type: "array", Items: schemaOfType(t.Elem())}
	case reflect.Struct:
		s := &Schema{Type: "object", Properties: map[string]*Schema{}}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name := f.Name
			omit := false
			if tag, ok := f.Tag.Lookup("json"); ok {
				parts := strings.Split(tag, ",")
				if parts[0] == "-" {
					continue
				}
				if parts[0] != "" {
					name = parts[0]
				}
				for _, p := range parts[1:] {
					if p == "omitempty" {
						omit = true
					}
				}
			} else {
				name = strings.ToLower(name)
			}
			s.Properties[name] = schemaOfType(f.Type)
			if !omit {
				s.Required = append(s.Required, name)
			}
		}
		return s
	default:
		return &Schema{}
	}
}

// ensure encoding/json is linked for users marshaling the doc.
var _ = json.Marshal
