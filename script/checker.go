package script

import (
	"sort"
	"strconv"
	"strings"
)

var builtinArity = map[string][]int{"len": {1}, "str": {1}, "range": {1, 2, 3}, "map": {2}, "mapcat": {2}, "fold": {3}, "fail": {1}, "concat": {2}, "allcat": {1}, "to_chars": {1}, "from_chars": {1}, "to_codepoint": {1}, "from_codepoint": {1}, "slice": {3, 4}, "keys": {1}, "has": {2}, "put": {3}, "split": {2}, "parse_number": {1}, "print": {1}, "log": {2}, "telemetry": {2}}

type bindingInfo struct {
	info    analysis
	fn      *expression
	builtin string
	static  bool
}
type scope struct {
	parent   *scope
	bindings map[string]bindingInfo
}

func (s *scope) child() *scope { return &scope{parent: s, bindings: map[string]bindingInfo{}} }
func (s *scope) find(name string) (bindingInfo, bool) {
	for s != nil {
		if x, ok := s.bindings[name]; ok {
			return x, true
		}
		s = s.parent
	}
	return bindingInfo{}, false
}

type checker struct {
	trustedNative bool
	syntax        *syntax
	contracts     map[string]Contract
	aliases       map[string]importDecl
	stores        map[string]*statement
	writers       map[string]int
	err           error
	top           int
	local         int
	active        map[*expression]bool
	voidOK        *expression
	returns       map[*expression]*operationSummary
}

func plainDesc(typ string) *staticDesc { return &staticDesc{kind: descPlain, typ: normalizeType(typ)} }
func unknownDesc() *staticDesc         { return nil }
func requestDesc(ops *operationSummary) *staticDesc {
	if ops == nil {
		return nil
	}
	return &staticDesc{kind: descRequest, typ: "effect", ops: ops}
}
func paramDesc(i int, typ string) *staticDesc {
	d := &staticDesc{kind: descParam, typ: normalizeType(typ), param: i}
	if d.typ == "effect" {
		d.ops = paramOperations(i)
	}
	return d
}
func paramOperations(i int) *operationSummary {
	return &operationSummary{parameter: true, param: i}
}
func callableDesc(fn *expression, ret *staticDesc) *staticDesc {
	return &staticDesc{kind: descCallable, typ: "function", fn: fn, ret: ret}
}
func listDesc(slots []*staticDesc, element *staticDesc) *staticDesc {
	return &staticDesc{kind: descList, typ: "list", slots: slots, element: element}
}
func listPrefixDesc(slots []*staticDesc, element *staticDesc) *staticDesc {
	return &staticDesc{kind: descList, typ: "list", slots: slots, tail: true, element: element}
}
func exactList(d *staticDesc) bool {
	return d != nil && d.kind == descList && d.slots != nil && !d.tail
}

// listShape joins only candidates that all expose a finite prefix.  Different
// exact lengths become a common prefix with an unknown tail; any untracked
// candidate makes the shape unavailable.
func listShape(d *staticDesc) (slots []*staticDesc, tail bool, ok bool) {
	if d == nil {
		return nil, false, false
	}
	if d.kind == descList {
		if d.slots == nil {
			return nil, false, false
		}
		return append([]*staticDesc(nil), d.slots...), d.tail, true
	}
	if d.kind != descUnion || len(d.parts) == 0 {
		return nil, false, false
	}
	first := true
	for _, part := range d.parts {
		partSlots, partTail, partOK := listShape(part)
		if !partOK {
			return nil, false, false
		}
		if first {
			slots, tail, first = partSlots, partTail, false
			continue
		}
		oldLen := len(slots)
		if len(partSlots) < oldLen {
			slots = slots[:len(partSlots)]
		}
		for i := range slots {
			slots[i] = unionDesc(slots[i], partSlots[i])
		}
		tail = tail || partTail || len(partSlots) != oldLen
	}
	return slots, tail, true
}

const maxStaticRangeSlots = 4096

func rangeDesc(args []argument) *staticDesc {
	numbers := make([]int64, len(args))
	for i, arg := range args {
		n, ok := staticIndex(arg.expr)
		if !ok {
			return listDesc(nil, plainDesc("number"))
		}
		numbers[i] = n
	}
	start, end, step := int64(0), int64(0), int64(1)
	switch len(numbers) {
	case 1:
		end = numbers[0]
	case 2:
		start, end = numbers[0], numbers[1]
	case 3:
		start, end, step = numbers[0], numbers[1], numbers[2]
	default:
		return nil
	}
	if step == 0 {
		return listDesc(nil, plainDesc("number"))
	}
	count := int64(0)
	if step > 0 && start < end {
		count = (end-start-1)/step + 1
	} else if step < 0 && start > end {
		count = (start-end-1)/(-step) + 1
	}
	if count > maxStaticRangeSlots {
		return listDesc(nil, plainDesc("number"))
	}
	slots := make([]*staticDesc, count)
	for i := range slots {
		slots[i] = plainDesc("number")
	}
	return listDesc(slots, plainDesc("number"))
}
func objectDesc(fields map[string]*staticDesc, rest *staticDesc, closed bool) *staticDesc {
	return &staticDesc{kind: descObject, typ: "object", fields: fields, rest: rest, closed: closed}
}
func noResultDesc() *staticDesc { return &staticDesc{kind: descNoResult} }

func descType(d *staticDesc) string {
	if d == nil {
		return "any"
	}
	if d.typ != "" {
		return d.typ
	}
	switch d.kind {
	case descRequest:
		return "effect"
	case descCallable:
		return "function"
	case descObject:
		return "object"
	case descList:
		return "list"
	case descNoResult:
		return "any"
	}
	return "any"
}

func descParts(d *staticDesc) []*staticDesc {
	if d == nil {
		return nil
	}
	if d.kind != descUnion {
		return []*staticDesc{d}
	}
	return d.parts
}

// sameDesc compares the complete finite description.  Descriptors can contain
// recursive callable returns, so the pair map is part of the comparison rather
// than relying on pointer identity or the first union insertion order.
func sameDesc(a, b *staticDesc) bool {
	type pair struct{ a, b *staticDesc }
	seen := map[pair]bool{}
	var eq func(*staticDesc, *staticDesc) bool
	var eqSummary func(*operationSummary, *operationSummary) bool
	summarySeen := map[struct{ a, b *operationSummary }]bool{}
	eqSummary = func(x, y *operationSummary) bool {
		if x == nil || y == nil {
			return x == y
		}
		p := struct{ a, b *operationSummary }{x, y}
		if summarySeen[p] {
			return true
		}
		summarySeen[p] = true
		if x.operation != y.operation || x.parameter != y.parameter || x.param != y.param || (x.mapped == nil) != (y.mapped == nil) || len(x.sources) != len(y.sources) || len(x.args) != len(y.args) {
			return false
		}
		if !eqSummary(x.mapped, y.mapped) {
			return false
		}
		for i := range x.args {
			if !eq(x.args[i], y.args[i]) {
				return false
			}
		}
		// Sources form a union, so their construction order is not part of the
		// description.  Match them as an unordered finite multiset while the
		// pair memo above handles recursive cells.
		used := make([]bool, len(y.sources))
		for _, xs := range x.sources {
			matched := false
			for j, ys := range y.sources {
				if !used[j] && eqSummary(xs, ys) {
					used[j] = true
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
		return true
	}
	eq = func(x, y *staticDesc) bool {
		if x == nil || y == nil {
			return x == y
		}
		if x.kind == descNoResult || y.kind == descNoResult {
			return x.kind == y.kind
		}
		p := pair{x, y}
		if seen[p] {
			return true
		}
		seen[p] = true
		if x.kind != y.kind || x.typ != y.typ || x.param != y.param || x.fn != y.fn || x.builtin != y.builtin || x.closed != y.closed {
			return false
		}
		if !eqSummary(x.ops, y.ops) || !eq(x.ret, y.ret) || !eq(x.rest, y.rest) || !eq(x.element, y.element) {
			return false
		}
		if (x.slots == nil) != (y.slots == nil) || x.tail != y.tail || (x.parts == nil) != (y.parts == nil) || len(x.slots) != len(y.slots) || len(x.parts) != len(y.parts) || len(x.fields) != len(y.fields) {
			return false
		}
		for i := range x.slots {
			if !eq(x.slots[i], y.slots[i]) {
				return false
			}
		}
		for i := range x.parts {
			if !eq(x.parts[i], y.parts[i]) {
				return false
			}
		}
		for k, xv := range x.fields {
			yv, ok := y.fields[k]
			if !ok || !eq(xv, yv) {
				return false
			}
		}
		return true
	}
	return eq(a, b)
}

// descOrderKey is intentionally structural and stable.  It is used only to
// canonicalize Union order; it never exposes a runtime pointer or value.
func descOrderKey(d *staticDesc) string {
	if d == nil {
		return "0"
	}
	if d.kind == descNoResult {
		return "n"
	}
	var b strings.Builder
	var write func(*staticDesc, map[*staticDesc]bool, map[*operationSummary]bool)
	var writeSummary func(*operationSummary, map[*staticDesc]bool, map[*operationSummary]bool)
	writeSummary = func(s *operationSummary, active map[*staticDesc]bool, summaries map[*operationSummary]bool) {
		if s == nil {
			b.WriteString("?")
			return
		}
		if summaries[s] {
			b.WriteString("sr")
			return
		}
		summaries[s] = true
		b.WriteString("s[")
		if s.parameter {
			b.WriteString("param:")
			b.WriteString(strconv.Itoa(s.param))
		} else if s.operation != "" {
			b.WriteString("op:")
			b.WriteString(s.operation)
		} else {
			b.WriteString("cell")
		}
		if s.mapped != nil {
			b.WriteString("|mapped:")
			writeSummary(s.mapped, active, summaries)
		}
		b.WriteString("|args:")
		for _, arg := range s.args {
			write(arg, active, summaries)
		}
		b.WriteString("|sources:")
		for _, source := range s.sources {
			writeSummary(source, active, summaries)
		}
		b.WriteByte(']')
		delete(summaries, s)
	}
	write = func(x *staticDesc, active map[*staticDesc]bool, summaries map[*operationSummary]bool) {
		if x == nil {
			b.WriteString("?")
			return
		}
		if active[x] {
			b.WriteString("r")
			return
		}
		active[x] = true
		b.WriteByte(byte('a' + x.kind))
		b.WriteString("[")
		b.WriteString(x.typ)
		if x.kind == descList {
			if x.slots == nil {
				b.WriteString("|slots:unknown")
			} else if x.tail {
				b.WriteString("|slots:prefix")
			} else {
				b.WriteString("|slots:exact")
			}
		}
		if x.kind == descObject {
			if x.closed {
				b.WriteString("|closed")
			} else {
				b.WriteString("|open")
			}
		}
		b.WriteByte('|')
		if x.fn != nil {
			b.WriteString("fn:")
			b.WriteString(strconv.Itoa(len(x.fn.tok.source)))
			b.WriteByte(':')
			b.WriteString(x.fn.tok.source)
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(x.fn.tok.site))
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(x.fn.tok.line))
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(x.fn.tok.column))
		}
		if x.param >= 0 {
			b.WriteString(strconv.Itoa(x.param))
		}
		if x.kind == descRequest {
			writeSummary(x.ops, active, summaries)
		}
		write(x.ret, active, summaries)
		write(x.rest, active, summaries)
		write(x.element, active, summaries)
		for _, slot := range x.slots {
			write(slot, active, summaries)
		}
		// Union parts are canonicalized when the union is built.  Re-sorting
		// here would restart the recursive key builder and could loop on a
		// recursive callable.
		for _, part := range x.parts {
			write(part, active, summaries)
		}
		keys := make([]string, 0, len(x.fields))
		for k := range x.fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(k)
			write(x.fields[k], active, summaries)
		}
		b.WriteByte(']')
		delete(active, x)
	}
	write(d, map[*staticDesc]bool{}, map[*operationSummary]bool{})
	return b.String()
}

func unionDesc(parts ...*staticDesc) *staticDesc {
	flat := make([]*staticDesc, 0, len(parts))
	var add func(*staticDesc)
	add = func(d *staticDesc) {
		if d == nil {
			// Unknown is top for request/callable qualification.  Keep it
			// explicit by returning nil to every caller.
			flat = nil
			return
		}
		if flat == nil {
			return
		}
		if d.kind == descNoResult {
			return
		}
		if d.kind == descUnion {
			for _, p := range d.parts {
				add(p)
				if flat == nil {
					return
				}
			}
			return
		}
		for _, p := range flat {
			if sameDesc(p, d) {
				return
			}
		}
		flat = append(flat, d)
	}
	for _, d := range parts {
		add(d)
		if flat == nil {
			return nil
		}
	}
	if len(flat) == 0 {
		return nil
	}
	if len(flat) == 1 {
		return flat[0]
	}
	sort.SliceStable(flat, func(i, j int) bool { return descOrderKey(flat[i]) < descOrderKey(flat[j]) })
	return &staticDesc{kind: descUnion, typ: unifyDescTypes(flat), parts: flat}
}

func unifyDescTypes(parts []*staticDesc) string {
	if len(parts) == 0 {
		return "any"
	}
	t := descType(parts[0])
	for _, p := range parts[1:] {
		t = unify(t, descType(p))
	}
	return t
}

func descOps(d *staticDesc) (*operationSummary, bool) {
	if d == nil {
		return nil, false
	}
	switch d.kind {
	case descNoResult:
		return nil, false
	case descRequest:
		return d.ops, d.ops != nil
	case descParam:
		return d.ops, d.ops != nil
	case descUnion:
		var out *operationSummary
		for _, p := range d.parts {
			op, ok := descOps(p)
			if !ok {
				return nil, false
			}
			if out == nil {
				out = op
			} else {
				out = unionOperations(out, op)
			}
		}
		return out, out != nil
	default:
		return nil, false
	}
}

func callableParts(d *staticDesc) ([]*staticDesc, bool) {
	if d == nil {
		return nil, false
	}
	if d.kind == descCallable {
		return []*staticDesc{d}, true
	}
	if d.kind != descUnion {
		return nil, false
	}
	var out []*staticDesc
	for _, p := range d.parts {
		if p.kind != descCallable {
			return nil, false
		}
		out = append(out, p)
	}
	return out, len(out) != 0
}

func instantiateDesc(d *staticDesc, args []*staticDesc, active map[*expression]bool) *staticDesc {
	if d == nil {
		return nil
	}
	if active == nil {
		active = map[*expression]bool{}
	}
	switch d.kind {
	case descNoResult:
		return d
	case descParam:
		if d.param >= 0 && d.param < len(args) {
			return args[d.param]
		}
		return nil
	case descRequest:
		// A provisional forward/recursive call points at an empty summary
		// cell.  Keep a finite mapped edge so a later function check can publish
		// its fixed point and recursive parameter permutations can be resolved at
		// the outer call site.  Copying an empty cell without its arguments would
		// freeze the call as an artificial Unknown.
		if d.ops != nil && d.ops.mapped == nil && !d.ops.parameter && d.ops.operation == "" && len(d.ops.sources) == 0 {
			copy := *d
			copy.ops = &operationSummary{mapped: d.ops, args: append([]*staticDesc(nil), args...)}
			return &copy
		}
		copy := *d
		copy.ops = instantiateOperations(d.ops, args)
		return &copy
	case descPlain:
		return d
	case descCallable:
		if d.fn != nil && active[d.fn] {
			return d
		}
		copy := *d
		if d.fn != nil {
			active[d.fn] = true
		}
		copy.ret = instantiateDesc(d.ret, args, active)
		if d.fn != nil {
			delete(active, d.fn)
		}
		return &copy
	case descObject:
		copy := *d
		copy.fields = make(map[string]*staticDesc, len(d.fields))
		for k, v := range d.fields {
			copy.fields[k] = instantiateDesc(v, args, active)
		}
		copy.rest = instantiateDesc(d.rest, args, active)
		return &copy
	case descList:
		copy := *d
		copy.slots = make([]*staticDesc, len(d.slots))
		for i, v := range d.slots {
			copy.slots[i] = instantiateDesc(v, args, active)
		}
		copy.element = instantiateDesc(d.element, args, active)
		return &copy
	case descUnion:
		parts := make([]*staticDesc, 0, len(d.parts))
		for _, p := range d.parts {
			parts = append(parts, instantiateDesc(p, args, active))
		}
		return unionDesc(parts...)
	}
	return nil
}

func staticIndex(e *expression) (int64, bool) {
	if e == nil {
		return 0, false
	}
	if e.tag == "lit" && e.value.k == Number && e.value.n == float64(int64(e.value.n)) && e.value.n >= -9007199254740991 && e.value.n <= 9007199254740991 {
		return int64(e.value.n), true
	}
	if e.tag == "unary" && e.name == "-" && e.x != nil && e.x.tag == "lit" && e.x.value.k == Number && e.x.value.n == float64(int64(e.x.value.n)) && e.x.value.n >= 0 && e.x.value.n <= 9007199254740991 {
		return -int64(e.x.value.n), true
	}
	if e.tag == "unary" && e.name == "+" && e.x != nil {
		return staticIndex(e.x)
	}
	return 0, false
}

func staticSliceValue(e *expression) (Value, bool) {
	if e == nil {
		return Value{}, false
	}
	if e.tag == "lit" {
		if e.value.k == Null {
			return e.value, true
		}
		if _, ok := staticIndex(e); ok {
			return e.value, true
		}
		return Value{}, false
	}
	if e.tag == "unary" && (e.name == "+" || e.name == "-") && e.x != nil && e.x.tag == "lit" && e.x.value.k == Number && e.x.value.n == float64(int64(e.x.value.n)) && e.x.value.n >= 0 && e.x.value.n <= 9007199254740991 {
		n := e.x.value.n
		if e.name == "-" {
			n = -n
		}
		v, err := NumberValue(n)
		return v, err == nil
	}
	return Value{}, false
}

// A slice with equal literal start/stop bounds is empty for every source
// length, after the runtime's clamping rules.  This is the only source-shape
// fact we can recover without knowing the list length; reverse slices with
// equal bounds are empty as well, while a literal zero step is an error.
func staticEmptySlice(bounds []argument) bool {
	if len(bounds) < 2 || len(bounds) > 3 {
		return false
	}
	start, okStart := staticSliceValue(bounds[0].expr)
	stop, okStop := staticSliceValue(bounds[1].expr)
	if !okStart || !okStop || start.k != Number || stop.k != Number || start.n != stop.n {
		return false
	}
	if len(bounds) == 3 && bounds[2].expr != nil {
		step, ok := staticSliceValue(bounds[2].expr)
		if !ok {
			return false
		}
		if step.k == Number && step.n == 0 {
			return false
		}
	}
	return true
}

func projectIndex(d *staticDesc, index *expression, gather bool) *staticDesc {
	if d == nil {
		return nil
	}
	if d.kind == descUnion {
		var out []*staticDesc
		for _, p := range d.parts {
			out = append(out, projectIndex(p, index, gather))
		}
		for _, p := range out {
			if p == nil {
				return nil
			}
		}
		return unionDesc(out...)
	}
	selector := "any"
	if index != nil && index.info.typ != "" {
		selector = normalizeType(index.info.typ)
	}
	if selector == "any" && (index == nil || index.tag != "list") {
		// An any selector may be either a scalar index or a gather list.  The
		// two normal result shapes cannot be safely collapsed into one source.
		return nil
	}
	if d.kind == descObject {
		if !typeOverlap(selector, "string") {
			return noResultDesc()
		}
		// A dynamic object key can select any known field and, for an open
		// object, the unknown rest.  A closed object with no fields has no
		// statically known normal candidate, so keep it Unknown.
		var out *staticDesc
		seen := false
		for _, field := range d.fields {
			if !seen {
				out, seen = field, true
			} else {
				out = unionDesc(out, field)
			}
		}
		if d.rest != nil {
			if !seen {
				out, seen = d.rest, true
			} else {
				out = unionDesc(out, d.rest)
			}
		} else if !d.closed {
			return nil
		}
		return out
	}
	if d.kind == descList {
		if !gather && !typeOverlap(selector, "number") {
			return noResultDesc()
		}
		if gather && !typeOverlap(selector, "list") {
			return noResultDesc()
		}
	} else if d.kind == descPlain && d.typ == "string" {
		if gather || !typeOverlap(selector, "number") {
			return noResultDesc()
		}
		return plainDesc("string")
	} else if d.kind == descPlain || d.kind == descParam {
		// Plain list/object values carry no finite shape.  Other known scalar
		// types have no normal indexing candidate.
		if d.typ == "any" || d.kind == descParam {
			return nil
		}
		return noResultDesc()
	} else {
		return noResultDesc()
	}
	if gather {
		if index == nil || index.tag != "list" {
			return listDesc(nil, descElement(d))
		}
		out := make([]*staticDesc, len(index.elems))
		if d.slots == nil {
			if len(index.elems) == 0 {
				return listDesc([]*staticDesc{}, nil)
			}
			return listDesc(nil, descElement(d))
		}
		for i, raw := range index.elems {
			n, ok := staticIndex(raw)
			if !ok {
				return listDesc(nil, d.element)
			}
			if d.tail && n < 0 {
				return listDesc(nil, d.element)
			}
			if n < 0 {
				n += int64(len(d.slots))
			}
			if n < 0 || n >= int64(len(d.slots)) {
				if d.tail && n >= int64(len(d.slots)) {
					out[i] = d.element
					continue
				}
				return noResultDesc()
			}
			out[i] = d.slots[n]
		}
		return listDesc(out, unionDesc(out...))
	}
	n, ok := staticIndex(index)
	if !ok {
		return descElement(d)
	}
	if d.tail && n < 0 {
		return descElement(d)
	}
	if d.slots == nil {
		return descElement(d)
	}
	if n < 0 {
		n += int64(len(d.slots))
	}
	if n < 0 || n >= int64(len(d.slots)) {
		if d.tail && n >= int64(len(d.slots)) {
			return d.element
		}
		return noResultDesc()
	}
	return d.slots[n]
}

func projectField(d *staticDesc, name string) *staticDesc {
	if d == nil {
		return nil
	}
	if d.kind == descUnion {
		parts := make([]*staticDesc, 0, len(d.parts))
		for _, p := range d.parts {
			parts = append(parts, projectField(p, name))
		}
		for _, p := range parts {
			if p == nil {
				return nil
			}
		}
		return unionDesc(parts...)
	}
	if d.kind != descObject {
		if (d.kind == descPlain || d.kind == descParam) && (d.typ == "any" || d.typ == "object") {
			return nil
		}
		return noResultDesc()
	}
	if x, ok := d.fields[name]; ok {
		return x
	}
	if d.rest != nil {
		return d.rest
	}
	if d.closed {
		return noResultDesc()
	}
	return nil
}

func descElement(d *staticDesc) *staticDesc {
	if d == nil {
		return nil
	}
	if d.kind == descList {
		if d.element != nil {
			return d.element
		}
		if d.tail {
			// A known prefix with no element summary has an unknown tail; the
			// prefix slots alone must not make dynamic element access look known.
			return nil
		}
		return unionDesc(d.slots...)
	}
	if d.kind == descUnion {
		parts := make([]*staticDesc, 0, len(d.parts))
		for _, p := range d.parts {
			parts = append(parts, descElement(p))
		}
		return unionDesc(parts...)
	}
	if (d.kind == descPlain || d.kind == descParam) && (d.typ == "any" || d.typ == "list") {
		return nil
	}
	return noResultDesc()
}

func unionListSlots(slots []*staticDesc) *staticDesc {
	if len(slots) == 0 {
		return nil
	}
	out := slots[0]
	for _, slot := range slots[1:] {
		out = unionDesc(out, slot)
	}
	return out
}

func listProjectionDesc(d *staticDesc, bounds []argument) *staticDesc {
	if d == nil {
		return nil
	}
	if d.kind == descUnion {
		parts := make([]*staticDesc, 0, len(d.parts))
		for _, p := range d.parts {
			parts = append(parts, listProjectionDesc(p, bounds))
		}
		for _, p := range parts {
			if p == nil {
				return nil
			}
		}
		return unionDesc(parts...)
	}
	if d.kind != descList {
		if (d.kind == descPlain || d.kind == descParam) && (d.typ == "any" || d.typ == "list") {
			return listDesc(nil, descElement(d))
		}
		return noResultDesc()
	}
	if staticEmptySlice(bounds) {
		return listDesc([]*staticDesc{}, nil)
	}
	if d.slots == nil {
		return listDesc(nil, descElement(d))
	}
	values := make([]Value, len(bounds))
	for i, b := range bounds {
		value, ok := staticSliceValue(b.expr)
		if !ok {
			return listDesc(nil, descElement(d))
		}
		values[i] = value
	}
	if d.tail {
		// A prefix can be sliced precisely when stop is a non-negative literal,
		// start is either omitted (the length-independent default zero) or a
		// non-negative literal, the step is positive, and stop stays within the
		// known prefix.  Negative bounds and an omitted stop depend on the
		// unknown total length and therefore remain conservative.
		if len(values) != 3 || values[1].k != Number || values[1].n < 0 || values[1].n > float64(len(d.slots)) || values[0].k != Null && (values[0].k != Number || values[0].n < 0 || values[0].n > float64(len(d.slots))) || values[2].k == Number && values[2].n <= 0 {
			return listDesc(nil, descElement(d))
		}
		start, step, count, err := slicePlan(len(d.slots), values)
		if err != nil {
			return noResultDesc()
		}
		slots := make([]*staticDesc, 0, count)
		for i := int64(0); i < count; i++ {
			pos := start + i*step
			if pos < 0 || pos >= int64(len(d.slots)) {
				return listDesc(nil, descElement(d))
			}
			slots = append(slots, d.slots[pos])
		}
		return listDesc(slots, unionListSlots(slots))
	}
	start, step, count, err := slicePlan(len(d.slots), values)
	if err != nil {
		return noResultDesc()
	}
	slots := make([]*staticDesc, 0, count)
	for i := int64(0); i < count; i++ {
		pos := start + i*step
		if pos < 0 || pos >= int64(len(d.slots)) {
			return noResultDesc()
		}
		slots = append(slots, d.slots[pos])
	}
	return listDesc(slots, unionListSlots(slots))
}

func unionOperations(a, b *operationSummary) *operationSummary {
	if a == nil || b == nil {
		return nil
	}
	return &operationSummary{sources: []*operationSummary{a, b}}
}

// instantiateOperations substitutes parameter summary atoms with the
// operation summary of the corresponding actual descriptor.  The memo table
// deliberately preserves recursive cells: a recursive relation such as
// R(p) = p | R(p) becomes a finite graph whose reachability is still the
// operation summary of p, rather than an artificial Unknown.
func instantiateOperations(root *operationSummary, args []*staticDesc) *operationSummary {
	if root == nil {
		return nil
	}
	type key struct {
		sig string
		n   *operationSummary
	}
	memo := map[key]*operationSummary{}
	argKey := func(xs []*staticDesc) string {
		var b strings.Builder
		for _, x := range xs {
			b.WriteString(descOrderKey(x))
			b.WriteByte(0)
		}
		return b.String()
	}
	var visit func(*operationSummary, []*staticDesc) *operationSummary
	visit = func(s *operationSummary, actual []*staticDesc) *operationSummary {
		if s == nil {
			return nil
		}
		if s.parameter {
			if s.param < 0 || s.param >= len(actual) {
				return nil
			}
			op, ok := descOps(actual[s.param])
			if !ok {
				return nil
			}
			return op
		}
		if s.mapped != nil {
			mappedArgs := make([]*staticDesc, len(s.args))
			for i, x := range s.args {
				mappedArgs[i] = instantiateDesc(x, actual, nil)
			}
			return visit(s.mapped, mappedArgs)
		}
		k := key{n: s, sig: argKey(actual)}
		if cached, ok := memo[k]; ok {
			return cached
		}
		copy := &operationSummary{operation: s.operation}
		memo[k] = copy
		for _, source := range s.sources {
			instantiated := visit(source, actual)
			if instantiated == nil {
				return nil
			}
			copy.sources = append(copy.sources, instantiated)
		}
		return copy
	}
	return visit(root, args)
}

// Reachability computes the least union fixed point, including recursive
// components. Empty intermediate cells are bottom, never an unknown leaf.
// No function is evaluated and no operation is inferred from a runtime value.
func resolvedOperations(root *operationSummary) ([]string, bool) {
	seen := map[*operationSummary]bool{}
	ops := map[string]bool{}
	pending := []*operationSummary{root}
	for len(pending) != 0 {
		s := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if s == nil {
			return nil, false
		}
		if s.parameter {
			return nil, false
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		if s.operation != "" {
			ops[s.operation] = true
		}
		pending = append(pending, s.sources...)
	}
	keys := make([]string, 0, len(ops))
	for op := range ops {
		keys = append(keys, op)
	}
	sort.Strings(keys)
	return keys, len(keys) != 0
}

func pureInfo(typ string) analysis {
	typ = normalizeType(typ)
	return analysis{typ: typ, desc: plainDesc(typ), effect: PureEffect, concurrent: true}
}
func mergeEffect(a, b analysis) analysis {
	if b.effect > a.effect {
		a.effect = b.effect
	}
	a.concurrent = a.concurrent && b.concurrent
	a.hasFlow = a.hasFlow || b.hasFlow
	return a
}
func unify(a, b string) string {
	a, b = normalizeType(a), normalizeType(b)
	if a == b {
		return a
	}
	if a == "null" {
		if b == "any" || strings.HasSuffix(b, "?") {
			return b
		}
		return b + "?"
	}
	if b == "null" {
		return unify(b, a)
	}
	return "any"
}

// typeOverlap answers the static question needed at callable boundaries: a
// mismatch is provable only when the two type sets are disjoint.  Nullable
// values therefore remain dynamically checked when they may contain the
// expected non-null value.
func typeOverlap(actual, expected string) bool {
	actual, expected = normalizeType(actual), normalizeType(expected)
	if actual == "any" || expected == "any" {
		return true
	}
	actuals := []string{actual}
	if strings.HasSuffix(actual, "?") {
		actuals = []string{"null", strings.TrimSuffix(actual, "?")}
	}
	expecteds := []string{expected}
	if strings.HasSuffix(expected, "?") {
		expecteds = []string{"null", strings.TrimSuffix(expected, "?")}
	}
	for _, a := range actuals {
		for _, e := range expecteds {
			if a == e {
				return true
			}
		}
	}
	return false
}

func descTypeOverlap(d *staticDesc, expected string) bool {
	if d == nil {
		return true
	}
	if d.kind == descNoResult {
		// NoResult is an internal missing-position witness.  At a call
		// boundary it becomes Unknown, so the eventual selector error keeps
		// its runtime phase instead of turning into a static E-TYPE.
		return true
	}
	if d.kind == descUnion {
		for _, p := range d.parts {
			if descTypeOverlap(p, expected) {
				return true
			}
		}
		return false
	}
	return typeOverlap(descType(d), expected)
}
func (c *checker) error(t token, code, msg string) {
	if c.err == nil {
		c.err = at(diagnostic(code, msg), t)
	}
}
func reserved(name string) bool {
	_, b := builtinArity[name]
	return b || name == "claim" || strings.Contains(" import as flow fn return if else match for in parallel break continue with next effect perform export true false null nan inf void ", " "+name+" ")
}
func (c *checker) declare(s *scope, name string, info bindingInfo, t token) {
	if reserved(name) {
		c.error(t, "E-RESERVED", "reserved name "+name)
	}
	if _, ok := c.stores[name]; ok {
		c.error(t, "E-SHADOW-CLAIM", "Claim name cannot be shadowed")
	}
	if _, ok := c.aliases[name]; ok {
		c.error(t, "E-RESERVED", "import alias cannot be rebound")
	}
	if _, ok := s.bindings[name]; ok {
		c.error(t, "E-DUPLICATE", "duplicate binding "+name)
	}
	s.bindings[name] = info
}
func (c *checker) require(t token, a analysis, typ string) {
	if a.typ != "any" && a.typ != typ {
		c.error(t, "E-TYPE", "expected "+typ+", got "+a.typ)
	}
}
func analyze(s *syntax, host Host) (*checker, error) {
	if len(s.flows) == 0 {
		return analyzeBindings(s, host, nil)
	}
	for _, name := range sortedFlowNames(s.flows) {
		if _, err := checkFlow(s, host, name); err != nil {
			return nil, err
		}
	}
	return s.flows[s.flowName].check, nil
}
func checkFlow(s *syntax, host Host, name string) (*checker, error) {
	f := s.flows[name]
	if f.checking {
		return nil, at(diagnostic("E-FLOW-CYCLE", "recursive Flow call "+name), f.tok)
	}
	if f.check != nil {
		return f.check, nil
	}
	f.checking = true
	defer func() { f.checking = false }()
	view := *s
	view.flowName, view.result, view.params, view.body = f.name, f.result, f.params, f.body
	view.cell = false
	c, err := analyzeBindings(&view, host, s.initial)
	if err != nil {
		return nil, err
	}
	summary := pureInfo(f.result)
	walkBlockExpressions(f.body, func(e *expression) { summary = mergeEffect(summary, e.info) })
	if f.result != "void" && len(f.body.items) != 0 {
		summary.ops = f.body.items[len(f.body.items)-1].expr.info.ops
		summary.desc = f.body.items[len(f.body.items)-1].expr.info.desc
	}
	f.summary, f.check = summary, c
	return c, nil
}

// Saved interactive values enter the same checker as ordinary source values.
// This is a tooling entry point; Compile always starts with an empty scope.
func analyzeBindings(s *syntax, host Host, initial map[string]bindingInfo) (*checker, error) {
	c := &checker{syntax: s, contracts: map[string]Contract{}, aliases: map[string]importDecl{}, stores: map[string]*statement{}, writers: map[string]int{}, returns: map[*expression]*operationSummary{}}
	for _, contract := range host.Contracts {
		if contract.Version == "" || contract.Result.Type == "" || contract.Invoke == nil || contract.Effect == PureEffect {
			return nil, diagnostic("E-OPERATION", "invalid operation contract")
		}
		key := identityKey(contract.Operation.Wire())
		if _, dup := c.contracts[key]; dup {
			return nil, diagnostic("E-OPERATION", "duplicate operation contract")
		}
		c.contracts[key] = contract
	}
	for _, d := range s.imports {
		if reserved(d.alias) {
			c.error(d.tok, "E-RESERVED", "reserved alias")
		}
		if _, dup := c.aliases[d.alias]; dup {
			c.error(d.tok, "E-DUPLICATE", "duplicate alias")
		}
		c.aliases[d.alias] = d
	}
	env := &scope{bindings: map[string]bindingInfo{}}
	for name := range builtinArity {
		env.bindings[name] = bindingInfo{info: pureInfo("function"), builtin: name, static: true}
	}
	dataNames := make([]string, 0, len(s.data))
	for name := range s.data {
		dataNames = append(dataNames, name)
	}
	sort.Slice(dataNames, func(i, j int) bool { return s.data[dataNames[i]].tok.site < s.data[dataNames[j]].tok.site })
	for _, name := range dataNames {
		d := s.data[name]
		if err := checkType(d.value, d.typ); err != nil {
			c.error(d.tok, "E-TYPE", "static data "+name+": "+err.Error())
		}
		c.declare(env, name, bindingInfo{info: pureInfo(d.typ)}, d.tok)
	}
	for _, name := range sortedFlowNames(s.flows) {
		f := s.flows[name]
		if reserved(name) {
			c.error(f.tok, "E-RESERVED", "reserved Flow name "+name)
		}
		if _, conflict := c.aliases[name]; conflict {
			c.error(f.tok, "E-RESERVED", "Flow/import name conflict")
		}
	}
	functions := make([]string, 0, len(s.functions))
	for name := range s.functions {
		functions = append(functions, name)
	}
	sort.Slice(functions, func(i, j int) bool { return s.functions[functions[i]].tok.site < s.functions[functions[j]].tok.site })
	for _, name := range functions {
		f := s.functions[name]
		cell := &operationSummary{}
		c.returns[f] = cell
		info := pureInfo("function")
		info.returns = cell
		c.declare(env, name, bindingInfo{info: info, fn: f, static: true}, f.tok)
	}
	for name, info := range initial {
		if s.functions[name] != nil {
			continue
		}
		if _, declared := s.data[name]; declared {
			continue
		}
		c.declare(env, name, info, token{site: -1})
	}
	for _, name := range functions {
		f := s.functions[name]
		c.function(f, env)
	}
	// Module-level functions capture declarations, never a caller Flow's stores.
	for i, stmt := range s.body.items {
		if stmt.tag == "claim" {
			if reserved(stmt.name) {
				c.error(stmt.tok, "E-RESERVED", "reserved store name")
			}
			if _, dup := c.stores[stmt.name]; dup {
				c.error(stmt.tok, "E-DUPLICATE", "duplicate Claim")
			}
			if _, dup := c.aliases[stmt.name]; dup {
				c.error(stmt.tok, "E-RESERVED", "store/import conflict")
			}
			c.stores[stmt.name] = stmt
		}
		c.collectWriters(stmt, i)
	}
	flow := env.child()
	if s.cell {
		for name, info := range initial {
			flow.bindings[name] = info
		}
	}
	for i, p := range s.params {
		info := pureInfo(p.typ)
		info.desc = paramDesc(i, p.typ)
		if normalizeType(p.typ) == "effect" {
			info.ops = paramOperations(i)
		}
		c.declare(flow, p.name, bindingInfo{info: info}, p.tok)
	}
	returns := 0
	for i, stmt := range s.body.items {
		c.top = i
		if stmt.tag == "return" {
			returns++
			if i != len(s.body.items)-1 {
				c.error(stmt.tok, "E-RETURN", "Flow return must be last")
			}
			if s.result != "void" && stmt.expr == nil {
				c.error(stmt.tok, "E-RETURN", "missing return value")
			}
			if s.result == "void" && stmt.expr != nil {
				c.error(stmt.tok, "E-RETURN", "void Flow cannot return a value")
			}
		}
		c.statement(stmt, flow, false, false, 0, true)
		if stmt.tag == "return" && stmt.expr != nil {
			c.assignType(stmt.expr.tok, stmt.expr.info.typ, s.result)
		}
	}
	if s.result != "void" && returns != 1 {
		c.error(s.body.tok, "E-RETURN", "Flow requires exactly one final return")
	}
	if returns > 1 {
		c.error(s.body.tok, "E-RETURN", "multiple returns")
	}
	return c, c.err
}
func (c *checker) collectWriters(s *statement, top int) {
	if s.tag == "join" {
		c.writers[s.name] = top
	}
	if s.body != nil {
		for _, x := range s.body.items {
			c.collectWriters(x, top)
		}
	}
	if s.otherwise != nil {
		for _, x := range s.otherwise.items {
			c.collectWriters(x, top)
		}
	}
	if s.other != nil {
		c.collectWriters(s.other, top)
	}
	for _, a := range s.arms {
		if a.body != nil {
			for _, x := range a.body.items {
				c.collectWriters(x, top)
			}
		}
	}
}
func (c *checker) assignType(t token, actual, expected string) {
	actual, expected = normalizeType(actual), normalizeType(expected)
	if typeOverlap(actual, expected) {
		return
	}
	c.error(t, "E-TYPE", "expected "+expected+", got "+actual)
}
func (c *checker) function(e *expression, env *scope) analysis {
	previous := c.trustedNative
	c.trustedNative = e.trustedNative
	defer func() { c.trustedNative = previous }()
	c.local++
	defer func() { c.local-- }()
	local := env.child()
	// Publish a provisional callable before checking the body so forward and
	// recursive calls can retain the operation fixed-point cell.
	if e.info.desc == nil {
		e.info.desc = callableDesc(e, nil)
	}
	if e.info.returns == nil {
		e.info.returns = c.returns[e]
	}
	if c.active == nil {
		c.active = map[*expression]bool{}
	}
	c.active[e] = true
	defer delete(c.active, e)
	for i, p := range e.params {
		info := pureInfo(p.typ)
		info.desc = paramDesc(i, p.typ)
		if normalizeType(p.typ) == "effect" {
			info.ops = paramOperations(i)
		}
		c.declare(local, p.name, bindingInfo{info: info}, p.tok)
	}
	a := c.localBlock(e.body, local, true, false, 0)
	c.assignType(e.tok, a.typ, e.typ)
	cell := c.returns[e]
	if cell == nil {
		cell = &operationSummary{}
	}
	cell.sources = []*operationSummary{a.ops}
	e.info = pureInfo("function")
	e.info.returns = cell
	retDesc := a.desc
	if retDesc == nil {
		retDesc = plainDesc(a.typ)
	}
	e.info.desc = callableDesc(e, retDesc)
	return e.info
}
func (c *checker) localBlock(b *block, env *scope, pure, parallel bool, loops int) analysis {
	a := pureInfo("any")
	for _, s := range b.items {
		a = mergeEffect(a, c.statement(s, env, pure, parallel, loops, false))
	}
	if b.tail != nil {
		tail := c.expression(b.tail, env, pure, parallel)
		a = mergeEffect(a, tail)
		a.typ = tail.typ
		a.ops = tail.ops
		a.desc = tail.desc
	}
	if b.exit != nil {
		a = mergeEffect(a, c.carryExit(b.exit, env, pure, parallel, loops))
	}
	return a
}
func (c *checker) statement(s *statement, env *scope, pure, parallel bool, loops int, top bool) analysis {
	a := pureInfo("any")
	if s.expr != nil {
		saved := c.voidOK
		if s.tag == "eval" {
			c.voidOK = s.expr
		}
		a = c.expression(s.expr, env, pure, parallel)
		c.voidOK = saved
	}
	switch s.tag {
	case "claim":
		if !top || pure {
			c.error(s.tok, "E-PURITY", "Claim declarations require Flow top level")
		}
		if _, dup := env.bindings[s.name]; dup {
			c.error(s.tok, "E-DUPLICATE", "duplicate Claim binding")
		}
		env.bindings[s.name] = bindingInfo{info: pureInfo("list")}
	case "bind":
		info := bindingInfo{info: a}
		if s.expr.tag == "fn" {
			info.fn = s.expr
		} else if s.expr.tag == "id" {
			if prior, ok := env.find(s.expr.name); ok {
				info.fn, info.builtin = prior.fn, prior.builtin
			}
		}
		c.declare(env, s.name, info, s.tok)
	case "join":
		if pure || c.local > 0 {
			c.error(s.tok, "E-PURITY", "join requires a Flow graph statement")
		}
		if _, ok := c.stores[s.name]; !ok {
			c.error(s.tok, "E-NAME", "join target is not a store")
		}
		if a.effect != PureEffect || a.hasFlow {
			c.error(s.tok, "E-PURITY", "join RHS must be pure")
		}
	case "return":
		if !top {
			c.error(s.tok, "E-RETURN", "return only at Flow tail")
		}
	case "break", "continue":
		if loops == 0 {
			c.error(s.tok, "E-CONTROL", "loop control outside a sequential loop")
		}
	case "if":
		c.require(s.tok, a, "bool")
		a = mergeEffect(a, c.localBlock(s.body, env.child(), pure, parallel, loops))
		if s.otherwise != nil {
			a = mergeEffect(a, c.localBlock(s.otherwise, env.child(), pure, parallel, loops))
		}
		if s.other != nil {
			a = mergeEffect(a, c.statement(s.other, env.child(), pure, parallel, loops, false))
		}
	case "match":
		for _, arm := range s.arms {
			c.patterns(arm)
			if arm.guard != nil {
				g := c.expression(arm.guard, env, pure, parallel)
				c.require(arm.guard.tok, g, "bool")
				if g.effect != PureEffect || g.hasFlow {
					c.error(arm.guard.tok, "E-PURITY", "guard must be pure")
				}
			}
			a = mergeEffect(a, c.localBlock(arm.body, env.child(), pure, parallel, loops))
		}
	case "for":
		if s.expr != nil {
			c.require(s.tok, a, "list")
		}
		if s.parallel && (pure || c.local > 0) {
			c.error(s.tok, "E-PARALLEL", "fn loop cannot be parallel")
		}
		child := env.child()
		if s.name != "" {
			item := pureInfo("any")
			item.desc = descElement(a.desc)
			c.declare(child, s.name, bindingInfo{info: item}, s.tok)
		}
		nextLoops := loops + 1
		if s.parallel {
			nextLoops = 0
		}
		a = mergeEffect(a, c.localBlock(s.body, child, pure, parallel || s.parallel, nextLoops))
	}
	if parallel && a.effect != PureEffect && (a.effect != ReadOnlyEffect || !a.concurrent) {
		c.error(s.tok, "E-PARALLEL", "parallel requires Concurrent ReadOnly")
	}
	return a
}
func (c *checker) patterns(a arm) {
	for _, p := range a.patterns {
		if p.wildcard && len(a.patterns) != 1 {
			c.error(p.tok, "E-PARSE", "wildcard must be the only pattern")
		}
	}
}
func (c *checker) carryExit(s *statement, env *scope, pure, parallel bool, loops int) analysis {
	a := c.expression(s.expr, env, pure, parallel)
	if s.tag == "next" || s.tag == "break" {
		if a.effect != PureEffect || a.hasFlow {
			c.error(s.tok, "E-PURITY", "carry exit must be pure")
		}
		return a
	}
	if s.tag == "if" {
		c.require(s.tok, a, "bool")
		a = mergeEffect(a, c.localBlock(s.body, env.child(), pure, parallel, loops))
		if s.other != nil {
			a = mergeEffect(a, c.carryExit(s.other, env.child(), pure, parallel, loops))
		}
		if s.otherwise != nil {
			a = mergeEffect(a, c.localBlock(s.otherwise, env.child(), pure, parallel, loops))
		}
	}
	if s.tag == "match" {
		for _, arm := range s.arms {
			c.patterns(arm)
			if arm.guard != nil {
				g := c.expression(arm.guard, env, pure, parallel)
				c.require(arm.guard.tok, g, "bool")
				if g.effect != PureEffect || g.hasFlow {
					c.error(arm.guard.tok, "E-PURITY", "guard must be pure")
				}
			}
			a = mergeEffect(a, c.localBlock(arm.body, env.child(), pure, parallel, loops))
		}
	}
	return a
}

func carryBlockPreservesState(b *block, state *staticDesc) bool {
	return b != nil && b.exit != nil && carryExitPreservesState(b.exit, state)
}

func carryExitPreservesState(s *statement, state *staticDesc) bool {
	if s == nil || state == nil {
		return false
	}
	switch s.tag {
	case "next", "break":
		return s.expr != nil && s.expr.tag == "id" && s.expr.info.desc == state
	case "if":
		if !carryBlockPreservesState(s.body, state) {
			return false
		}
		if s.other != nil && !carryExitPreservesState(s.other, state) {
			return false
		}
		return s.otherwise != nil && carryBlockPreservesState(s.otherwise, state)
	case "match":
		if len(s.arms) == 0 {
			return false
		}
		for _, arm := range s.arms {
			if !carryBlockPreservesState(arm.body, state) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (c *checker) resolve(e *expression) (Contract, bool) {
	if e.name == "claim.snapshot" {
		return Contract{Operation: Operation{"lip", "claim", "snapshot"}, Version: "0.9", Params: []Parameter{{Name: "store", Schema: Schema{Type: "string"}}}, Result: Schema{Type: "list"}, Effect: ReadOnlyEffect, Concurrent: true}, true
	}
	parts := strings.SplitN(e.name, ".", 2)
	d, ok := c.aliases[parts[0]]
	if !ok || len(parts) != 2 {
		c.error(e.tok, "E-OPERATION", "operation requires imported alias and member")
		return Contract{}, false
	}
	key := identityKey(Operation{d.language, d.path, parts[1]}.Wire())
	contract, ok := c.contracts[key]
	if !ok {
		c.error(e.tok, "E-OPERATION", "unregistered operation "+e.name)
	}
	return contract, ok
}
func (c *checker) expression(e *expression, env *scope, pure, parallel bool) analysis {
	if e == nil {
		return pureInfo("any")
	}
	a := pureInfo("any")
	sub := func(x *expression) analysis {
		v := c.expression(x, env, pure, parallel)
		a = mergeEffect(a, v)
		return v
	}
	switch e.tag {
	case "lit":
		a.typ = e.value.k.String()
		a.desc = plainDesc(a.typ)
	case "id":
		if store, ok := c.stores[e.name]; ok {
			if pure {
				c.error(e.tok, "E-PURITY", "fn cannot capture/read active Claim")
			}
			if _, declared := env.find(e.name); !declared || store.tok.site > e.tok.site {
				c.error(e.tok, "E-NAME", "store not yet declared")
			}
			if top, writer := c.writers[e.name]; writer && c.top <= top {
				c.error(e.tok, "E-CLAIM-ORDER", "complete read must follow every writer region")
			}
			a.typ = "list"
		} else if x, ok := env.find(e.name); ok {
			a = x.info
			// A binding that carries a statically known function can be copied
			// through a list/object/conditional without losing its callable
			// return description.  The binding's ordinary type remains
			// "function", but its finite descriptor belongs to the declaration
			// that created the value.
			if x.fn != nil && (a.desc == nil || (a.desc.kind == descPlain && a.desc.typ == "function")) {
				if x.fn.info.desc != nil {
					a.desc = x.fn.info.desc
				} else {
					a.desc = callableDesc(x.fn, nil)
				}
			}
			a.effect, a.concurrent, a.hasFlow = PureEffect, true, false
		} else if _, ok := c.syntax.flows[e.name]; ok {
			c.error(e.tok, "E-TYPE", "Flow declarations are not values")
		} else {
			c.error(e.tok, "E-NAME", "unbound name "+e.name)
		}
	case "unary":
		x := sub(e.x)
		want := "number"
		if e.name == "!" {
			want = "bool"
		}
		c.require(e.tok, x, want)
		a.typ = want
		a.desc = plainDesc(want)
	case "binary":
		x, y := sub(e.x), sub(e.y)
		switch e.name {
		case "&&", "||":
			c.require(e.tok, x, "bool")
			c.require(e.tok, y, "bool")
			a.typ = "bool"
		case "==", "!=":
			if x.typ == "function" || x.typ == "effect" || y.typ == "function" || y.typ == "effect" {
				c.error(e.tok, "E-TYPE", "function/effect equality is undefined")
			}
			a.typ = "bool"
		case "<", "<=", ">", ">=":
			if x.typ != "any" && y.typ != "any" && (x.typ != y.typ || x.typ != "number" && x.typ != "string" && x.typ != "char") {
				c.error(e.tok, "E-TYPE", "comparison requires two numbers, strings, or chars")
			}
			a.typ = "bool"
		default:
			if e.name == "+" && (x.typ == "string" && (y.typ == "string" || y.typ == "any") || y.typ == "string" && x.typ == "any") {
				a.typ = "string"
			} else if e.name == "+" && x.typ == "any" && y.typ == "any" {
				a.typ = "any"
			} else {
				c.require(e.tok, x, "number")
				c.require(e.tok, y, "number")
				a.typ = "number"
			}
		}
		a.desc = plainDesc(a.typ)
	case "list":
		slots := make([]*staticDesc, 0, len(e.elems))
		for _, x := range e.elems {
			sub(x)
			slots = append(slots, x.info.desc)
		}
		a.typ = "list"
		a.desc = listDesc(slots, unionListSlots(slots))
	case "object":
		names := map[string]bool{}
		fields := map[string]*staticDesc{}
		for _, f := range e.fields {
			if names[f.name] {
				c.error(f.tok, "E-DUPLICATE", "duplicate object field")
			}
			names[f.name] = true
			sub(f.expr)
			fields[f.name] = f.expr.info.desc
		}
		a.typ = "object"
		a.desc = objectDesc(fields, nil, true)
	case "field", "index":
		x := sub(e.x)
		if e.tag == "field" {
			c.require(e.tok, x, "object")
		} else if x.typ != "any" && x.typ != "list" && x.typ != "string" && x.typ != "object" {
			c.error(e.tok, "E-TYPE", "value cannot be indexed")
		}
		if e.tag == "field" {
			a.desc = projectField(x.desc, e.name)
		}
		if e.y != nil {
			y := sub(e.y)
			if x.typ == "object" {
				c.require(e.tok, y, "string")
			} else if x.typ == "list" {
				if y.typ != "any" && y.typ != "number" && y.typ != "list" {
					c.error(e.tok, "E-TYPE", "list index requires number or list")
				}
				if y.typ == "list" {
					a.typ = "list"
					if e.y.tag == "list" {
						for _, item := range e.y.elems {
							c.require(item.tok, item.info, "number")
						}
					}
				}
			} else if x.typ == "string" {
				c.require(e.tok, y, "number")
				a.typ = "string"
			}
		}
		if e.tag == "index" {
			if x.typ == "string" {
				// Indexing a string always produces a one-scalar string.  A
				// dynamic/any receiver must stay unknown rather than looking like
				// a list projection to callable argument checking.
				a.desc = plainDesc("string")
			} else if x.desc != nil && e.y != nil && e.y.tag == "lit" && e.y.value.k == String {
				a.desc = projectField(x.desc, e.y.value.s)
			} else if e.y != nil && x.desc != nil {
				gather := e.y.tag == "list" || e.y.info.typ == "list"
				a.desc = projectIndex(x.desc, e.y, gather)
			}
		}
		if a.desc != nil {
			if ops, ok := descOps(a.desc); ok {
				a.ops = ops
			}
		}
	case "native_receiver":
		a = sub(e.x)
		a.ops = nil
	case "slice":
		x := sub(e.x)
		if x.typ != "any" && x.typ != "list" && x.typ != "string" {
			c.error(e.tok, "E-TYPE", "slice requires list or string")
		}
		for _, arg := range e.args {
			y := sub(arg.expr)
			c.assignType(arg.expr.tok, y.typ, "number?")
		}
		a.typ, a.ops = x.typ, nil
		if x.typ == "string" {
			a.desc = plainDesc("string")
		} else if x.typ == "list" {
			a.desc = listProjectionDesc(x.desc, e.args)
		} else {
			// A slice of an any value may be a list or a string at runtime;
			// do not manufacture a list descriptor and reject a dynamic call.
			a.desc = nil
		}
	case "fn":
		return c.function(e, env)
	case "block":
		c.local++
		a = c.localBlock(e.body, env.child(), pure, parallel, 0)
		c.local--
	case "if":
		g := sub(e.x)
		c.require(e.tok, g, "bool")
		c.local++
		x := c.localBlock(e.body, env.child(), pure, parallel, 0)
		c.local--
		y := sub(e.y)
		a = mergeEffect(a, x)
		a.typ = unify(x.typ, y.typ)
		a.ops = unionOperations(x.ops, y.ops)
		a.desc = unionDesc(x.desc, y.desc)
	case "match":
		sub(e.x)
		first := true
		for _, arm := range e.arms {
			c.patterns(arm)
			if arm.guard != nil {
				g := sub(arm.guard)
				c.require(arm.guard.tok, g, "bool")
				if g.effect != PureEffect || g.hasFlow {
					c.error(arm.guard.tok, "E-PURITY", "guard must be pure")
				}
			}
			x := sub(arm.result)
			if first {
				a.typ, a.ops = x.typ, x.ops
				a.desc = x.desc
				first = false
			} else {
				a.typ = unify(a.typ, x.typ)
				a.ops = unionOperations(a.ops, x.ops)
				a.desc = unionDesc(a.desc, x.desc)
			}
		}
	case "proposal":
		contract, ok := c.resolve(e)
		a.typ = "effect"
		if ok {
			a.ops = &operationSummary{operation: identityKey(contract.Operation.Wire())}
			a.desc = requestDesc(a.ops)
		}
		for _, arg := range e.args {
			x := sub(arg.expr)
			if x.effect != PureEffect || x.hasFlow {
				c.error(e.tok, "E-PURITY", "proposal arguments must be pure")
			}
			if x.typ == "function" || x.typ == "effect" {
				c.error(e.tok, "E-SCHEMA", "proposal argument must be Data")
			}
		}
		if ok {
			c.contractArguments(e, contract)
		}
		if e.name == "claim.snapshot" {
			if pure {
				c.error(e.tok, "E-PURITY", "fn cannot construct store snapshot")
			}
			if len(e.args) != 1 || e.args[0].expr.tag != "lit" || e.args[0].expr.value.k != String {
				c.error(e.tok, "E-SCHEMA", "snapshot requires a literal store name")
			} else {
				name := e.args[0].expr.value.s
				store, ok := c.stores[name]
				if !ok || store.tok.site >= e.tok.site {
					c.error(e.tok, "E-NAME", "unknown or undeclared snapshot store")
				}
			}
		}
	case "perform":
		x := sub(e.x)
		c.require(e.tok, x, "effect")
		if pure && !c.trustedNative {
			c.error(e.tok, "E-PURITY", "native calls in fn require an @{...} purity promise")
		}
		a.typ = "any"
		a.ops = nil
		ops, known := resolvedOperations(x.ops)
		if dops, dok := descOps(x.desc); dok {
			x.ops = dops
			ops, known = resolvedOperations(dops)
		}
		if !known {
			a.effect = UnknownEffect
			a.concurrent = false
		} else {
			a.typ = ""
			for _, key := range ops {
				contract, ok := c.contracts[key]
				if !ok {
					contract = Contract{Result: Schema{Type: "list"}, Effect: ReadOnlyEffect, Concurrent: true}
				}
				if a.typ == "" {
					a.typ = normalizeType(contract.Result.Type)
				} else {
					a.typ = unify(a.typ, contract.Result.Type)
				}
				a = mergeEffect(a, analysis{effect: contract.Effect, concurrent: contract.Concurrent})
			}
		}
		if c.trustedNative {
			a.effect, a.concurrent = PureEffect, true
		}
		a.desc = plainDesc(a.typ)
	case "pipe":
		first := sub(e.x)
		local := env.child()
		local.bindings["$pipe"] = bindingInfo{info: first}
		target := e.y.name
		if e.y.tag != "flow" && e.y.x.tag != "id" {
			c.error(e.tok, "E-TYPE", "pipeline target must be a static declaration")
		} else if e.y.tag != "flow" {
			target = e.y.x.name
		}
		if binding, ok := env.find(target); ok && !binding.static {
			c.error(e.tok, "E-TYPE", "pipeline target must be a static declaration")
		}
		saved := c.voidOK
		if saved == e {
			c.voidOK = e.y
		}
		stage := c.expression(e.y, local, pure, parallel)
		c.voidOK = saved
		a = mergeEffect(first, stage)
		a.typ, a.ops = stage.typ, stage.ops
		a.desc = stage.desc
	case "flow":
		a = c.flowCall(e, env, pure, parallel)
	case "call":
		if e.x.tag == "id" {
			if _, shadow := env.find(e.x.name); !shadow {
				if _, ok := c.syntax.flows[e.x.name]; ok {
					e.tag, e.name, e.tok = "flow", e.x.name, e.x.tok
					e.x = nil
					a = c.flowCall(e, env, pure, parallel)
					break
				}
			}
		}
		callee := sub(e.x)
		c.require(e.tok, callee, "function")
		argDescs := make([]*staticDesc, len(e.args))
		for _, arg := range e.args {
			sub(arg.expr)
		}
		for i := range e.args {
			argDescs[i] = e.args[i].expr.info.desc
		}
		a.ops = callee.returns
		a.typ = "any"
		candidateDesc := callee.desc
		if e.x.tag == "id" {
			if binding, ok := env.find(e.x.name); ok && binding.fn != nil {
				if binding.fn.info.desc != nil {
					candidateDesc = binding.fn.info.desc
				}
				if candidateDesc == nil || candidateDesc.kind == descPlain {
					candidateDesc = callableDesc(binding.fn, nil)
				}
			}
		}
		if candidates, ok := callableParts(candidateDesc); ok {
			var returns []*staticDesc
			failed := make([]int, 0, len(candidates))
			for _, candidate := range candidates {
				if candidate.fn != nil {
					if phase := candidatePhase(candidate.fn, e.args); phase >= 0 {
						failed = append(failed, phase)
						continue
					}
					ret := candidate.ret
					if ret == nil {
						cell := candidate.fn.info.returns
						if cell == nil {
							cell = c.returns[candidate.fn]
						}
						ret = requestDesc(cell)
					}
					returns = append(returns, instantiateDesc(ret, argDescs, nil))
				}
			}
			if len(failed) == 0 && len(returns) == len(candidates) && len(returns) > 0 {
				a.desc = unionDesc(returns...)
				if typ := descType(a.desc); typ != "any" {
					declared := ""
					if len(candidates) == 1 && candidates[0].fn != nil {
						declared = normalizeType(candidates[0].fn.typ)
					}
					if declared != "any" && !strings.HasPrefix(declared, "any?") && declared != "" {
						a.typ = typ
					}
				}
				if ops, ok := descOps(a.desc); ok {
					a.ops = ops
				}
			} else if len(failed) == len(candidates) && len(failed) > 0 {
				phase := failed[0]
				for _, p := range failed[1:] {
					if p < phase {
						phase = p
					}
				}
				if phase == 1 {
					c.error(e.tok, "E-ARITY", "callable candidates reject arguments")
				} else {
					c.error(e.tok, "E-TYPE", "callable candidates reject argument types")
				}
			}
		}
		if e.x.tag == "fn" {
			c.checkArity(e, "", e.x, env)
			a.typ = e.x.typ
			if a.desc == nil {
				a.desc = e.x.info.desc
			}
		}
		if e.x.tag == "id" {
			if binding, ok := env.find(e.x.name); ok {
				if binding.builtin != "" {
					c.checkArity(e, binding.builtin, nil, env)
					switch binding.builtin {
					case "len", "parse_number", "to_codepoint":
						a.typ = "number"
					case "str":
						a.typ = "string"
					case "from_codepoint":
						a.typ = "char"
					case "from_chars":
						a.typ = "string"
					case "to_chars":
						a.typ = "list"
					case "print", "log", "telemetry":
						a.typ = "null"
					case "has":
						a.typ = "bool"
					case "range", "map", "mapcat", "keys", "split":
						a.typ = "list"
					case "allcat":
						a.typ = "any"
						if len(e.args) == 1 && e.args[0].expr.tag == "list" && len(e.args[0].expr.elems) > 0 {
							kind := ""
							known := true
							for _, part := range e.args[0].expr.elems {
								typ := normalizeType(part.info.typ)
								if typ != "list" && typ != "string" {
									known = false
									break
								}
								if kind == "" {
									kind = typ
								} else if kind != typ {
									known = false
									break
								}
							}
							if known && kind != "" {
								a.typ = kind
							}
						}
					case "concat":
						if len(e.args) == 2 && normalizeType(e.args[0].expr.info.typ) == normalizeType(e.args[1].expr.info.typ) {
							a.typ = e.args[0].expr.info.typ
						} else {
							a.typ = "any"
						}
					case "put":
						a.typ = "object"
					case "slice":
						if len(e.args) > 0 {
							a.typ = e.args[0].expr.info.typ
						}
					}
					a.desc = builtinDesc(binding.builtin, e, env)
				} else if binding.fn != nil {
					c.checkArity(e, "", binding.fn, env)
					a.typ = binding.fn.typ
					if a.desc == nil {
						a.desc = binding.fn.info.desc
					}
				}
			}
		}
		if a.desc != nil {
			if ops, ok := descOps(a.desc); ok {
				a.ops = ops
			}
		}
	case "comprehension":
		source := sub(e.y)
		c.require(e.tok, source, "list")
		local := env.child()
		item := pureInfo("any")
		item.desc = descElement(source.desc)
		c.declare(local, e.name, bindingInfo{info: item}, e.tok)
		if e.z != nil {
			g := c.expression(e.z, local, pure, parallel)
			c.require(e.z.tok, g, "bool")
			if g.effect != PureEffect || g.hasFlow {
				c.error(e.z.tok, "E-PURITY", "comprehension guard must be pure")
			}
			a = mergeEffect(a, g)
		}
		a = mergeEffect(a, c.expression(e.x, local, pure, parallel))
		a.typ = "list"
		bodyDesc := a.desc
		if e.x != nil {
			bodyDesc = e.x.info.desc
		}
		shapeSlots, shapeTail, shapeOK := listShape(source.desc)
		if shapeOK && (e.z == nil || len(shapeSlots) == 0 && !shapeTail) {
			slots := make([]*staticDesc, len(shapeSlots))
			if e.z == nil && e.x != nil && e.x.tag == "id" && e.x.name == e.name {
				// Direct identity comprehensions preserve the source's individual
				// slot descriptions.  Comparing only bodyDesc with source.element
				// would be unsound for a constant body whose union happens to match
				// the source element union.
				copy(slots, shapeSlots)
			} else {
				for i := range slots {
					slots[i] = bodyDesc
				}
			}
			if shapeTail {
				a.desc = listPrefixDesc(slots, bodyDesc)
			} else {
				a.desc = listDesc(slots, bodyDesc)
			}
		} else {
			a.desc = listDesc(nil, bodyDesc)
		}
	case "carry":
		var source analysis
		if e.x != nil {
			source = sub(e.x)
			c.require(e.tok, source, "list")
		}
		init := sub(e.y)
		a = mergeEffect(a, init)
		local := env.child()
		if e.name != "" {
			c.declare(local, e.name, bindingInfo{info: pureInfo("any")}, e.tok)
		}
		stateInfo := pureInfo("any")
		stateDesc := stateInfo.desc
		c.declare(local, e.state, bindingInfo{info: stateInfo}, e.tok)
		c.local++
		a = mergeEffect(a, c.localBlock(e.body, local, pure, parallel, 0))
		c.local--
		a.typ = "any"
		a.ops = nil
		// A statically empty finite source never enters the body.  A body whose
		// every normal exit returns the original state has the same result for
		// every finite source.  Both cases still checked the complete body above;
		// this only preserves the returned value descriptor.
		identity := e.x != nil && carryBlockPreservesState(e.body, stateDesc)
		empty := e.x != nil && exactList(source.desc) && len(source.desc.slots) == 0
		if empty || identity {
			a.typ = init.typ
			a.desc = init.desc
		} else {
			a.desc = nil
		}
	}
	if parallel && a.effect != PureEffect && (a.effect != ReadOnlyEffect || !a.concurrent) {
		c.error(e.tok, "E-PARALLEL", "perform is not provably Concurrent ReadOnly")
	}
	e.info = a
	return a
}
func (c *checker) checkArity(e *expression, builtin string, f *expression, env *scope) {
	if builtin != "" {
		for _, arg := range e.args {
			if arg.name != "" {
				c.error(e.tok, "E-ARITY", "core functions reject named arguments")
			}
		}
		valid := false
		for _, n := range builtinArity[builtin] {
			valid = valid || n == len(e.args)
		}
		if !valid {
			c.error(e.tok, "E-ARITY", "wrong core arity")
		}
		if valid {
			c.builtinTypes(e, builtin, env)
			if builtin == "map" || builtin == "fold" {
				index, arity := 1, 1
				if builtin == "fold" {
					index, arity = 2, 2
				}
				callback := e.args[index].expr
				info := bindingInfo{}
				if callback.tag == "fn" {
					info.fn = callback
				} else if callback.tag == "id" {
					info, _ = env.find(callback.name)
				}
				if info.fn != nil && len(info.fn.params) != arity {
					c.error(callback.tok, "E-ARITY", "wrong callback arity")
				}
				if info.builtin != "" {
					valid := false
					for _, n := range builtinArity[info.builtin] {
						valid = valid || n == arity
					}
					if !valid {
						c.error(callback.tok, "E-ARITY", "wrong core callback arity")
					}
				}
				if builtin == "fold" && info.fn != nil && len(info.fn.params) == 2 {
					c.assignType(e.args[1].expr.tok, e.args[1].expr.info.typ, info.fn.params[0].typ)
				}
				if source := e.args[0].expr; source.tag == "list" && info.fn != nil && len(info.fn.params) == arity {
					for _, element := range source.elems {
						c.assignType(element.tok, element.info.typ, info.fn.params[arity-1].typ)
					}
				}
				// A finite source descriptor supplies one abstract callback
				// activation even when the runtime list is produced by a core
				// operation rather than a literal.  Reject only when every known
				// callback candidate is disjoint; partial unions stay dynamic.
				callbackDesc := callback.info.desc
				if callback.tag == "id" {
					if binding, ok := env.find(callback.name); ok && binding.fn != nil {
						callbackDesc = binding.fn.info.desc
						if callbackDesc == nil {
							callbackDesc = callableDesc(binding.fn, nil)
						}
					}
				}
				if candidates, ok := callableParts(callbackDesc); ok {
					item := descElement(e.args[0].expr.info.desc)
					if item != nil && item.kind != descNoResult {
						abstract := make([]argument, arity)
						if builtin == "fold" {
							abstract[0] = argument{expr: &expression{info: analysis{typ: descType(e.args[1].expr.info.desc), desc: e.args[1].expr.info.desc}}}
							abstract[1] = argument{expr: &expression{info: analysis{typ: descType(item), desc: item}}}
						} else {
							abstract[0] = argument{expr: &expression{info: analysis{typ: descType(item), desc: item}}}
						}
						failed := 0
						best := 3
						for _, candidate := range candidates {
							phase := candidatePhase(candidate.fn, abstract)
							if phase >= 0 {
								failed++
								if phase < best {
									best = phase
								}
							}
						}
						if failed == len(candidates) {
							if best == 1 {
								c.error(callback.tok, "E-ARITY", "callback candidates reject arguments")
							} else {
								c.error(callback.tok, "E-TYPE", "callback candidates reject argument types")
							}
						}
					}
				}
			}
		}
		return
	}
	seen := map[string]bool{}
	next := 0
	for _, arg := range e.args {
		name := arg.name
		if name == "" {
			if next >= len(f.params) {
				c.error(e.tok, "E-ARITY", "too many arguments")
				continue
			}
			name = f.params[next].name
			next++
		}
		if seen[name] {
			c.error(e.tok, "E-ARITY", "unknown/duplicate parameter")
			continue
		}
		found := false
		for _, p := range f.params {
			if p.name == name {
				found = true
				c.assignType(arg.expr.tok, arg.expr.info.typ, p.typ)
			}
		}
		if !found {
			c.error(e.tok, "E-ARITY", "unknown/duplicate parameter")
		}
		seen[name] = true
	}
	if len(seen) != len(f.params) {
		c.error(e.tok, "E-ARITY", "missing arguments")
	}
}

// candidatePhase performs only proofs available from finite descriptions.  A
// partially compatible callable union remains dynamic/Unknown; all-candidate
// failure is rejected at the earliest callable-boundary phase.
func candidatePhase(f *expression, args []argument) int {
	if f == nil {
		return 0
	}
	seen := map[string]bool{}
	next := 0
	for _, arg := range args {
		name := arg.name
		if name == "" {
			if next >= len(f.params) {
				return 1
			}
			name = f.params[next].name
			next++
		}
		if seen[name] {
			return 1
		}
		found := false
		for _, p := range f.params {
			if p.name == name {
				found = true
				seen[name] = true
				expected := normalizeType(p.typ)
				if !descTypeOverlap(arg.expr.info.desc, expected) {
					return 2
				}
				break
			}
		}
		if !found {
			return 1
		}
	}
	if len(seen) != len(f.params) {
		return 1
	}
	return -1
}

func concatDesc(left, right *staticDesc) *staticDesc {
	if left == nil || right == nil {
		return nil
	}
	if left.kind == descUnion {
		var out *staticDesc
		seen := false
		for _, l := range left.parts {
			r := concatDesc(l, right)
			if !seen {
				out, seen = r, true
			} else {
				out = unionDesc(out, r)
			}
		}
		return out
	}
	if right.kind == descUnion {
		var out *staticDesc
		seen := false
		for _, r := range right.parts {
			v := concatDesc(left, r)
			if !seen {
				out, seen = v, true
			} else {
				out = unionDesc(out, v)
			}
		}
		return out
	}
	// Empty exact lists are identities even when the other operand has no
	// tracked shape.  Apply this before requiring both operands to be concrete
	// list descriptors; otherwise concat([], plain(list)) needlessly loses the
	// descriptor of its right operand (and vice versa).
	if exactList(left) && len(left.slots) == 0 {
		return right
	}
	if exactList(right) && len(right.slots) == 0 {
		return left
	}
	leftUnknownList := (left.kind == descPlain || left.kind == descParam) && left.typ == "list"
	rightUnknownList := (right.kind == descPlain || right.kind == descParam) && right.typ == "list"
	if left.kind == descList && rightUnknownList {
		// A known left prefix remains at the same positions after appending an
		// unknown finite list.  Only the tail element becomes unknown.
		if left.slots != nil {
			return listPrefixDesc(append([]*staticDesc(nil), left.slots...), unionDesc(left.element, nil))
		}
		return listDesc(nil, nil)
	}
	if left.kind != descList || right.kind != descList {
		if leftUnknownList || rightUnknownList {
			return listDesc(nil, nil)
		}
		return noResultDesc()
	}
	element := unionDesc(left.element, right.element)
	if left.slots != nil {
		slots := append([]*staticDesc(nil), left.slots...)
		if right.slots != nil && !left.tail {
			slots = append(slots, right.slots...)
			if right.tail {
				return listPrefixDesc(slots, element)
			}
			return listDesc(slots, element)
		}
		return listPrefixDesc(slots, element)
	}
	return listDesc(nil, element)
}

func concatAllDesc(parts *staticDesc) *staticDesc {
	slots, tail, ok := listShape(parts)
	if !ok || tail || len(slots) == 0 {
		return nil
	}
	var result *staticDesc
	for _, part := range slots {
		if part == nil {
			return nil
		}
		if part.kind == descPlain && part.typ == "string" {
			if result != nil && !(result.kind == descPlain && result.typ == "string") {
				return nil
			}
			result = plainDesc("string")
			continue
		}
		if part.kind != descList {
			return nil
		}
		if result == nil {
			result = part
		} else if result.kind == descList {
			result = concatDesc(result, part)
		} else {
			return nil
		}
	}
	return result
}

func mapDesc(source, callback *staticDesc) *staticDesc {
	if source == nil {
		return nil
	}
	if source.kind == descUnion {
		var out *staticDesc
		seen := false
		for _, part := range source.parts {
			v := mapDesc(part, callback)
			if !seen {
				out, seen = v, true
			} else {
				out = unionDesc(out, v)
			}
		}
		return out
	}
	if source.kind != descList {
		if (source.kind == descPlain || source.kind == descParam) && (source.typ == "any" || source.typ == "list") {
			return nil
		}
		return noResultDesc()
	}
	if exactList(source) && len(source.slots) == 0 {
		return listDesc([]*staticDesc{}, nil)
	}
	item := descElement(source)
	candidates, ok := callableParts(callback)
	if !ok {
		return nil
	}
	// A map callback is invoked with one abstract source element.  Preserve a
	// result description only when every finite callback candidate accepts that
	// activation; a partial mismatch is a dynamic target and must not be used
	// to admit a request hidden in one candidate.  The item may itself be
	// unknown: a callback that does not return it can still have a known result.
	mapOne := func(item *staticDesc) *staticDesc {
		abstract := argument{expr: &expression{info: analysis{typ: descType(item), desc: item}}}
		var ret *staticDesc
		for _, candidate := range candidates {
			if candidate.fn == nil || candidatePhase(candidate.fn, []argument{abstract}) >= 0 || candidate.ret == nil {
				return nil
			}
			one := instantiateDesc(candidate.ret, []*staticDesc{item}, nil)
			if one == nil {
				return nil
			}
			if ret == nil {
				ret = one
			} else {
				ret = unionDesc(ret, one)
			}
		}
		return ret
	}
	if source.slots != nil {
		slots := make([]*staticDesc, len(source.slots))
		for i := range slots {
			slots[i] = mapOne(source.slots[i])
		}
		if source.tail {
			return listPrefixDesc(slots, mapOne(source.element))
		}
		return listDesc(slots, unionDesc(slots...))
	}
	return listDesc(nil, mapOne(item))
}

func descIsParameter(d *staticDesc, index int) bool {
	if d == nil {
		return false
	}
	if d.kind == descParam {
		return d.param == index
	}
	if d.kind == descUnion {
		if len(d.parts) == 0 {
			return false
		}
		for _, part := range d.parts {
			if !descIsParameter(part, index) {
				return false
			}
		}
		return true
	}
	return false
}

func accumulatorIdentity(source, seed, callback *staticDesc) bool {
	candidates, ok := callableParts(callback)
	if !ok {
		return false
	}
	for _, candidate := range candidates {
		if candidate.fn == nil || !descIsParameter(candidate.ret, 0) {
			return false
		}
	}
	var checkSource func(*staticDesc) bool
	checkSource = func(source *staticDesc) bool {
		if source == nil {
			return true
		}
		if source.kind == descUnion {
			for _, part := range source.parts {
				if !checkSource(part) {
					return false
				}
			}
			return true
		}
		if source.kind != descList {
			if (source.kind == descPlain || source.kind == descParam) && (source.typ == "any" || source.typ == "list") {
				return true
			}
			return false
		}
		checkItem := func(item *staticDesc) bool {
			args := []argument{
				{expr: &expression{info: analysis{typ: descType(seed), desc: seed}}},
				{expr: &expression{info: analysis{typ: descType(item), desc: item}}},
			}
			for _, candidate := range candidates {
				if candidatePhase(candidate.fn, args) >= 0 {
					return false
				}
			}
			return true
		}
		for _, item := range source.slots {
			if !checkItem(item) {
				return false
			}
		}
		if source.tail && !checkItem(source.element) {
			return false
		}
		return true
	}
	return checkSource(source)
}

// foldDesc performs a bounded abstract execution when the source has an exact
// finite shape.  The empty case is unconditional: fold([], seed, f) returns
// seed after the ordinary outer/callback checks have still run.  For a finite
// non-empty source, every callable candidate must accept every activation and
// provide a finite return description; otherwise the fold remains Unknown.
func foldDesc(source, seed, callback *staticDesc) *staticDesc {
	if seed == nil {
		return nil
	}
	// Returning the accumulator is independent of the source length.  This is
	// safe even for an open list, while the ordinary callback checks still run
	// in the caller before this descriptor is used.
	if accumulatorIdentity(source, seed, callback) {
		return seed
	}
	if source == nil {
		return nil
	}
	if source.kind == descUnion {
		var out *staticDesc
		seen := false
		for _, part := range source.parts {
			v := foldDesc(part, seed, callback)
			if v == nil {
				return nil
			}
			if !seen {
				out, seen = v, true
			} else {
				out = unionDesc(out, v)
			}
		}
		return out
	}
	if source.kind != descList {
		if (source.kind == descPlain || source.kind == descParam) && (source.typ == "any" || source.typ == "list") {
			return nil
		}
		return noResultDesc()
	}
	if source.slots == nil || source.tail {
		return nil
	}
	if len(source.slots) == 0 {
		return seed
	}
	if len(source.slots) > 64 {
		return nil
	}
	candidates, ok := callableParts(callback)
	if !ok {
		return nil
	}
	var out *staticDesc
	seen := false
	for _, candidate := range candidates {
		if candidate.fn == nil || candidate.ret == nil {
			return nil
		}
		acc := seed
		for _, item := range source.slots {
			args := []argument{
				{expr: &expression{info: analysis{typ: descType(acc), desc: acc}}},
				{expr: &expression{info: analysis{typ: descType(item), desc: item}}},
			}
			if candidatePhase(candidate.fn, args) >= 0 {
				return nil
			}
			acc = instantiateDesc(candidate.ret, []*staticDesc{acc, item}, nil)
			if acc == nil {
				return nil
			}
		}
		if !seen {
			out, seen = acc, true
		} else {
			out = unionDesc(out, acc)
		}
	}
	return out
}

func putDesc(base *staticDesc, key *expression, value *staticDesc) *staticDesc {
	if base == nil {
		return nil
	}
	if base.kind == descUnion {
		var out *staticDesc
		seen := false
		for _, part := range base.parts {
			v := putDesc(part, key, value)
			if !seen {
				out, seen = v, true
			} else {
				out = unionDesc(out, v)
			}
		}
		return out
	}
	if base.kind != descObject {
		if (base.kind == descPlain || base.kind == descParam) && (base.typ == "any" || base.typ == "object") {
			if key != nil && key.tag == "lit" && key.value.k == String {
				return objectDesc(map[string]*staticDesc{key.value.s: value}, nil, false)
			}
			return objectDesc(nil, nil, false)
		}
		return noResultDesc()
	}
	fields := make(map[string]*staticDesc, len(base.fields)+1)
	for k, v := range base.fields {
		fields[k] = v
	}
	if key != nil && key.tag == "lit" && key.value.k == String {
		fields[key.value.s] = value
		return objectDesc(fields, base.rest, base.closed && base.rest == nil)
	}
	for k, old := range fields {
		fields[k] = unionDesc(old, value)
	}
	rest := unionDesc(base.rest, value)
	if base.closed && base.rest == nil {
		rest = value
	}
	return objectDesc(fields, rest, false)
}

func keysDesc(d *staticDesc) *staticDesc {
	if d == nil {
		return listDesc(nil, plainDesc("string"))
	}
	if d.kind == descUnion {
		var out *staticDesc
		seen := false
		for _, part := range d.parts {
			v := keysDesc(part)
			if !seen {
				out, seen = v, true
			} else {
				out = unionDesc(out, v)
			}
		}
		return out
	}
	if d.kind != descObject {
		if (d.kind == descPlain || d.kind == descParam) && (d.typ == "any" || d.typ == "object") {
			return listDesc(nil, plainDesc("string"))
		}
		return noResultDesc()
	}
	if !d.closed || d.rest != nil {
		return listDesc(nil, plainDesc("string"))
	}
	keys := make([]string, 0, len(d.fields))
	for key := range d.fields {
		keys = append(keys, key)
	}
	if len(keys) > maxStaticRangeSlots {
		return listDesc(nil, plainDesc("string"))
	}
	sort.Strings(keys)
	slots := make([]*staticDesc, len(keys))
	for i := range slots {
		slots[i] = plainDesc("string")
	}
	return listDesc(slots, plainDesc("string"))
}

func builtinDesc(name string, e *expression, env *scope) *staticDesc {
	switch name {
	case "map":
		if len(e.args) != 2 {
			return nil
		}
		callback := e.args[1].expr.info.desc
		if e.args[1].expr.tag == "id" && env != nil {
			if binding, ok := env.find(e.args[1].expr.name); ok && binding.fn != nil {
				callback = binding.fn.info.desc
				if callback == nil {
					callback = callableDesc(binding.fn, nil)
				}
			}
		}
		return mapDesc(e.args[0].expr.info.desc, callback)
	case "mapcat":
		if len(e.args) != 2 {
			return nil
		}
		callback := e.args[1].expr.info.desc
		if e.args[1].expr.tag == "id" && env != nil {
			if binding, ok := env.find(e.args[1].expr.name); ok && binding.fn != nil {
				callback = binding.fn.info.desc
				if callback == nil {
					callback = callableDesc(binding.fn, nil)
				}
			}
		}
		mapped := mapDesc(e.args[0].expr.info.desc, callback)
		if slots, tail, ok := listShape(mapped); ok && !tail && len(slots) == 0 {
			return listDesc([]*staticDesc{}, nil)
		}
		return concatAllDesc(mapped)
	case "fold":
		if len(e.args) != 3 {
			return nil
		}
		callback := e.args[2].expr.info.desc
		if e.args[2].expr.tag == "id" && env != nil {
			if binding, ok := env.find(e.args[2].expr.name); ok && binding.fn != nil {
				callback = binding.fn.info.desc
				if callback == nil {
					callback = callableDesc(binding.fn, nil)
				}
			}
		}
		return foldDesc(e.args[0].expr.info.desc, e.args[1].expr.info.desc, callback)
	case "concat":
		if len(e.args) != 2 {
			return nil
		}
		if e.args[0].expr.info.typ == "string" && e.args[1].expr.info.typ == "string" {
			return plainDesc("string")
		}
		return concatDesc(e.args[0].expr.info.desc, e.args[1].expr.info.desc)
	case "allcat":
		if len(e.args) == 1 {
			return concatAllDesc(e.args[0].expr.info.desc)
		}
		return nil
	case "to_chars":
		return listDesc(nil, plainDesc("char"))
	case "from_chars":
		return plainDesc("string")
	case "to_codepoint":
		return plainDesc("number")
	case "from_codepoint":
		return plainDesc("char")
	case "slice":
		if len(e.args) != 3 && len(e.args) != 4 {
			return nil
		}
		bounds := e.args[1:]
		if len(bounds) == 2 {
			// The three-argument core form omits step; represent that omission as
			// the same null bound used by bracket slicing.
			bounds = append(append([]argument(nil), bounds...), argument{expr: &expression{tag: "lit", value: NullValue()}})
		}
		return listProjectionDesc(e.args[0].expr.info.desc, bounds)
	case "put":
		if len(e.args) != 3 {
			return nil
		}
		return putDesc(e.args[0].expr.info.desc, e.args[1].expr, e.args[2].expr.info.desc)
	case "range":
		return rangeDesc(e.args)
	case "keys":
		if len(e.args) == 1 {
			return keysDesc(e.args[0].expr.info.desc)
		}
		return listDesc(nil, plainDesc("string"))
	case "split":
		if len(e.args) == 2 && e.args[0].expr.tag == "lit" && e.args[0].expr.value.k == String && e.args[1].expr.tag == "lit" && e.args[1].expr.value.k == String {
			parts := strings.Split(e.args[0].expr.value.s, e.args[1].expr.value.s)
			if len(parts) > maxStaticRangeSlots {
				return listDesc(nil, plainDesc("string"))
			}
			slots := make([]*staticDesc, len(parts))
			for i := range slots {
				slots[i] = plainDesc("string")
			}
			return listDesc(slots, plainDesc("string"))
		}
		return listDesc(nil, plainDesc("string"))
	case "len", "parse_number":
		return plainDesc("number")
	case "str":
		return plainDesc("string")
	case "has":
		return plainDesc("bool")
	case "print", "log", "telemetry":
		return plainDesc("null")
	case "fail":
		// fail has no normal return; keep its static result unconstrained so a
		// branch that is never selected does not reject the surrounding type.
		return plainDesc("any")
	}
	return nil
}

func (c *checker) builtinTypes(e *expression, name string, env *scope) {
	types := map[string][]string{"range": {"number"}, "map": {"list", "function"}, "mapcat": {"list", "function"}, "fold": {"list", "any", "function"}, "fail": {"string"}, "allcat": {"list"}, "to_chars": {"string"}, "from_chars": {"list"}, "to_codepoint": {"char"}, "from_codepoint": {"number"}, "keys": {"object"}, "has": {"object", "string"}, "put": {"object", "string", "any"}, "split": {"string", "string"}, "parse_number": {"string"}, "log": {"string", "any"}, "telemetry": {"string", "object"}}
	if name == "mapcat" && len(e.args) == 2 {
		result := ""
		if e.args[1].expr.tag == "fn" {
			result = e.args[1].expr.typ
		} else if e.args[1].expr.tag == "id" && env != nil {
			if binding, ok := env.find(e.args[1].expr.name); ok && binding.fn != nil {
				result = binding.fn.typ
			}
		}
		if result != "" && normalizeType(result) != "list" && normalizeType(result) != "any" {
			c.error(e.args[1].expr.tok, "E-TYPE", "mapcat callback must return list")
		}
	}
	if name == "concat" {
		for _, arg := range e.args {
			typ := normalizeType(arg.expr.info.typ)
			if typ != "any" && typ != "list" && typ != "string" {
				c.error(arg.expr.tok, "E-TYPE", "concat requires two lists or two strings")
			}
		}
		if len(e.args) == 2 {
			a, b := normalizeType(e.args[0].expr.info.typ), normalizeType(e.args[1].expr.info.typ)
			if a != "any" && b != "any" && a != b {
				c.error(e.tok, "E-TYPE", "concat requires matching list or string arguments")
			}
		}
		return
	}
	if name == "allcat" && len(e.args) == 1 && e.args[0].expr.tag == "list" {
		parts := e.args[0].expr.elems
		if len(parts) == 0 {
			c.error(e.tok, "E-TYPE", "allcat requires a nonempty parts list")
		} else {
			kind := ""
			for _, part := range parts {
				typ := normalizeType(part.info.typ)
				if typ != "list" && typ != "string" && typ != "any" {
					c.error(part.tok, "E-TYPE", "allcat parts must be lists or strings")
					continue
				}
				if typ != "any" {
					if kind == "" {
						kind = typ
					} else if kind != typ {
						c.error(part.tok, "E-TYPE", "allcat parts must have matching kinds")
					}
				}
			}
		}
	}
	if name == "from_chars" && len(e.args) == 1 && e.args[0].expr.tag == "list" {
		for _, part := range e.args[0].expr.elems {
			c.require(part.tok, part.info, "char")
		}
	}
	if name == "len" || name == "slice" {
		typ := e.args[0].expr.info.typ
		if typ != "any" && typ != "string" && typ != "list" && (name == "slice" || typ != "object") {
			c.error(e.tok, "E-TYPE", "invalid "+name+" argument")
		}
	}
	if name == "str" && (e.args[0].expr.info.typ == "function" || e.args[0].expr.info.typ == "effect") {
		c.error(e.tok, "E-TYPE", "str requires Data")
	}
	if name == "slice" {
		for _, arg := range e.args[1:] {
			c.assignType(arg.expr.tok, arg.expr.info.typ, "number?")
		}
	}
	for i, typ := range types[name] {
		if name == "range" {
			for _, arg := range e.args {
				c.require(arg.expr.tok, arg.expr.info, "number")
			}
			break
		}
		if typ != "any" {
			c.require(e.args[i].expr.tok, e.args[i].expr.info, typ)
		}
	}
}
func (c *checker) contractArguments(e *expression, contract Contract) {
	assigned := make([]bool, len(contract.Params))
	next := 0
	for _, arg := range e.args {
		index := next
		if arg.name == "" {
			next++
		} else {
			index = -1
			for i, p := range contract.Params {
				if p.Name == arg.name {
					index = i
					break
				}
			}
		}
		if index < 0 || index >= len(contract.Params) || assigned[index] {
			c.error(e.tok, "E-ARITY", "unknown/duplicate operation argument")
			continue
		}
		assigned[index] = true
		actual, expected := arg.expr.info.typ, normalizeType(contract.Params[index].Schema.Type)
		if !typeOverlap(actual, expected) {
			c.error(arg.expr.tok, "E-SCHEMA", "operation argument schema mismatch")
		}
	}
	for i, p := range contract.Params {
		if !assigned[i] && !p.Optional {
			c.error(e.tok, "E-ARITY", "missing operation argument "+p.Name)
		}
	}
}

func (c *checker) flowCall(e *expression, env *scope, pure, parallel bool) analysis {
	f := c.syntax.flows[e.name]
	if pure {
		c.error(e.tok, "E-PURITY", "fn cannot invoke Flow")
		return pureInfo(f.result)
	}
	// Arguments run in the caller's cursor before the child graph starts.
	// Their effects must remain visible even when the callee itself is Pure.
	arguments := pureInfo("any")
	for _, arg := range e.args {
		x := c.expression(arg.expr, env, false, parallel)
		arguments = mergeEffect(arguments, x)
	}
	c.checkArity(e, "", &expression{params: f.params}, env)
	if f.result == "void" && c.voidOK != e {
		c.error(e.tok, "E-TYPE", "void Flow cannot be used as a value")
	}
	_, err := checkFlow(c.syntax, Host{Contracts: contractValues(c.contracts)}, e.name)
	if err != nil && c.err == nil {
		if d, ok := err.(*Diagnostic); ok {
			c.err = d
		} else {
			c.error(e.tok, "E-TYPE", err.Error())
		}
	}
	a := mergeEffect(f.summary, arguments)
	a.typ = f.result
	a.hasFlow = true
	args := make([]*staticDesc, len(e.args))
	for i := range e.args {
		args[i] = e.args[i].expr.info.desc
	}
	a.desc = instantiateDesc(f.summary.desc, args, nil)
	if a.desc != nil {
		if typ := descType(a.desc); typ != "any" && !strings.HasPrefix(normalizeType(f.result), "any?") {
			a.typ = typ
		}
		a.ops, _ = descOps(a.desc)
	}
	return a
}
func contractValues(contracts map[string]Contract) []Contract {
	out := make([]Contract, 0, len(contracts))
	for _, contract := range contracts {
		out = append(out, contract)
	}
	return out
}
