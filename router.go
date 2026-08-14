package web

import (
	"fmt"
	"sort"
	"strings"
)

// node is a radix tree node. Static segments are prefix-merged within one
// path segment (like httprouter); wildcard children ({name} and {name...})
// sit beside static children at a segment boundary and match one whole
// segment. Static children are tried first, then a parameter child, then a
// catch-all child.
type node struct {
	prefix   string // static fragment of the current segment (never empty, never contains '/')
	children []*node

	param     *node // {name} child
	paramName string

	catch     *node // {name...} child (terminal)
	catchName string

	// leafHandlers 是切片而非 map：叶节点通常只有一两个方法，
	// 热分派是线性扫描，零哈希成本（profile 中的 HashTrieMap.Load）。
	leafHandlers []leafHandler
	methods      []string // sorted, for the Allow header
}

// leafHandler is one method at one leaf.
type leafHandler struct {
	method string
	h      Handler
}

func (n *node) addHandler(method string, h Handler) {
	for i := range n.leafHandlers {
		if n.leafHandlers[i].method == method {
			n.leafHandlers[i].h = h
			return
		}
	}
	n.leafHandlers = append(n.leafHandlers, leafHandler{method: method, h: h})
	sort.Slice(n.leafHandlers, func(i, j int) bool {
		return n.leafHandlers[i].method < n.leafHandlers[j].method
	})
	n.methods = append(n.methods, method)
	sort.Strings(n.methods)
}

func (n *node) leafInsert(method string, h Handler) error {
	for _, lh := range n.leafHandlers {
		if lh.method == method {
			return fmt.Errorf("web: duplicate route for method %s", method)
		}
	}
	n.addHandler(method, h)
	return nil
}

// handlerFor returns the handler for a method, with HEAD falling back to GET.
func (n *node) handlerFor(method string) Handler {
	for _, lh := range n.leafHandlers {
		if lh.method == method {
			return lh.h
		}
	}
	if method == "HEAD" {
		return n.handlerFor("GET")
	}
	return nil
}

// insert adds a route described by validated segments.
func (n *node) insert(segs []string, h Handler, method string) error {
	if len(segs) == 0 {
		return n.leafInsert(method, h)
	}
	seg := segs[0]
	rest := segs[1:]

	if strings.HasPrefix(seg, "{") {
		if seg[len(seg)-1] != '}' {
			return fmt.Errorf("web: malformed wildcard %q", seg)
		}
		if strings.HasSuffix(seg, "...}") {
			name := seg[1 : len(seg)-4]
			if err := validateWildcardName(name); err != nil {
				return err
			}
			if len(rest) > 0 {
				return fmt.Errorf("web: catch-all %q must be the last segment", seg)
			}
			if n.catch == nil {
				n.catch = &node{}
				n.catchName = name
			} else if n.catchName != name {
				return fmt.Errorf("web: conflicting catch-all names %q and %q at the same position", n.catchName, name)
			}
			return n.catch.leafInsert(method, h)
		}
		name := seg[1 : len(seg)-1]
		if err := validateWildcardName(name); err != nil {
			return err
		}
		if n.param == nil {
			n.param = &node{}
			n.paramName = name
		} else if n.paramName != name {
			return fmt.Errorf("web: conflicting parameter names %q and %q at the same position", n.paramName, name)
		}
		return n.param.insert(rest, h, method)
	}
	if strings.ContainsAny(seg, "{}") {
		return fmt.Errorf("web: wildcard must occupy a whole path segment in %q", seg)
	}

	// Static segment: radix merge with an existing child.
	for _, c := range n.children {
		l := commonPrefix(seg, c.prefix)
		if l == 0 {
			continue
		}
		if l == len(c.prefix) {
			rem := rest
			if l < len(seg) {
				rem = append([]string{seg[l:]}, rest...)
			}
			return c.insert(rem, h, method)
		}
		// Split c at l: the common part stays, the tail becomes a grandchild.
		grand := &node{
			prefix:       c.prefix[l:],
			children:     c.children,
			param:        c.param,
			paramName:    c.paramName,
			catch:        c.catch,
			catchName:    c.catchName,
			leafHandlers: c.leafHandlers,
			methods:      c.methods,
		}
		c.prefix = c.prefix[:l]
		c.children = []*node{grand}
		c.param, c.paramName, c.catch, c.catchName, c.leafHandlers, c.methods = nil, "", nil, "", nil, nil

		rem := rest
		if l < len(seg) {
			rem = append([]string{seg[l:]}, rest...)
		}
		return c.insert(rem, h, method)
	}

	child := &node{prefix: seg}
	n.children = append(n.children, child)
	return child.insert(rest, h, method)
}

// match walks the request path. rem is the unconsumed tail of the current
// segment ("" at a segment boundary); pos is the offset of the next segment
// in path; ps collects parameters in registration order.
//
// It returns the deepest node reached on success; whether that node has a
// handler for the method is the caller's concern (404 vs 405 vs dispatch).
func (n *node) match(rem, path string, pos int, ps []param) (*node, []param, bool) {
	var seg string
	if rem != "" {
		seg = rem
	} else {
		if pos >= len(path) {
			return n, ps, true
		}
		var next int
		seg, next = nextSeg(path, pos)
		pos = next
	}

	for _, c := range n.children {
		if strings.HasPrefix(seg, c.prefix) {
			if nd, p, ok := c.match(seg[len(c.prefix):], path, pos, ps); ok {
				return nd, p, true
			}
		}
	}

	// Wildcards match a whole segment only: skip them mid-segment.
	if rem == "" {
		if n.param != nil {
			if nd, p, ok := n.param.match("", path, pos, append(ps, param{key: n.paramName, value: seg})); ok {
				return nd, p, true
			}
		}
		if n.catch != nil {
			rest := seg
			if pos < len(path) {
				rest += path[pos-1:] // path[pos-1] is the '/' before this segment
			}
			return n.catch, append(ps, param{key: n.catchName, value: rest}), true
		}
	}
	return nil, ps, false
}

func nextSeg(path string, pos int) (string, int) {
	if end := strings.IndexByte(path[pos:], '/'); end >= 0 {
		return path[pos : pos+end], pos + end + 1
	}
	return path[pos:], len(path)
}

func commonPrefix(a, b string) int {
	max := min(len(a), len(b))
	i := 0
	for i < max && a[i] == b[i] {
		i++
	}
	return i
}

func validateWildcardName(name string) error {
	if name == "" {
		return fmt.Errorf("web: wildcard name must not be empty")
	}
	if strings.ContainsAny(name, "{}") {
		return fmt.Errorf("web: wildcard name %q must not contain braces", name)
	}
	return nil
}

// parsePath validates a registration path and splits it into segments.
func parsePath(path string) ([]string, error) {
	if path == "" || path[0] != '/' {
		return nil, fmt.Errorf("web: path %q must start with '/'", path)
	}
	if path == "/" {
		return nil, nil
	}
	if strings.HasSuffix(path, "/") {
		return nil, fmt.Errorf("web: path %q must not end with '/': use %q", path, strings.TrimRight(path, "/"))
	}
	segs := strings.Split(path[1:], "/")
	for _, s := range segs {
		if s == "" {
			return nil, fmt.Errorf("web: path %q contains an empty segment", path)
		}
	}
	return segs, nil
}
