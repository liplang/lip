package script

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Kind uint8

const (
	Null Kind = iota
	Bool
	Number
	String
	List
	Object
	Function
	Effect
	Char
)

func (k Kind) String() string {
	return [...]string{"null", "bool", "number", "string", "list", "object", "function", "effect", "char"}[k]
}

// Value is immutable. Constructors and accessors never expose mutable storage.
type Value struct {
	k Kind
	b bool
	n float64
	s string
	r rune
	l []Value
	o map[string]Value
	f *function
	p *proposal
}

func (v Value) Kind() Kind   { return v.k }
func NullValue() Value       { return Value{} }
func BoolValue(b bool) Value { return Value{k: Bool, b: b} }
func NumberValue(n float64) (Value, error) {
	if math.IsNaN(n) {
		n = math.Float64frombits(0x7ff8000000000000)
	}
	if n == 0 {
		n = 0
	}
	return Value{k: Number, n: n}, nil
}
func nanValue() Value        { return Value{k: Number, n: math.Float64frombits(0x7ff8000000000000)} }
func infValue() Value        { return Value{k: Number, n: math.Inf(1)} }
func number(n float64) Value { v, _ := NumberValue(n); return v }
func StringValue(s string) (Value, error) {
	if !utf8.ValidString(s) {
		return Value{}, diagnostic("E-VALUE", "string is not valid UTF-8")
	}
	return Value{k: String, s: s}, nil
}
func textValue(s string) Value { return Value{k: String, s: s} }
func CharValue(r rune) (Value, error) {
	if r < 0 || r > utf8.MaxRune || r >= 0xd800 && r <= 0xdfff {
		return Value{}, diagnostic("E-VALUE", "char is not a Unicode scalar")
	}
	return Value{k: Char, r: r}, nil
}
func charValue(r rune) Value      { v, _ := CharValue(r); return v }
func ListValue(xs ...Value) Value { return Value{k: List, l: append([]Value(nil), xs...)} }
func ObjectValue(fields map[string]Value) (Value, error) {
	o := make(map[string]Value, len(fields))
	for k, v := range fields {
		if !utf8.ValidString(k) {
			return Value{}, diagnostic("E-VALUE", "object key is not valid UTF-8")
		}
		o[k] = v
	}
	return Value{k: Object, o: o}, nil
}
func object(fields map[string]Value) Value { v, _ := ObjectValue(fields); return v }
func (v Value) Float() (float64, bool)     { return v.n, v.k == Number }
func (v Value) Text() (string, bool)       { return v.s, v.k == String }
func (v Value) Boolean() (bool, bool)      { return v.b, v.k == Bool }
func (v Value) Elements() ([]Value, bool)  { return append([]Value(nil), v.l...), v.k == List }
func (v Value) Fields() (map[string]Value, bool) {
	o := make(map[string]Value, len(v.o))
	for k, x := range v.o {
		o[k] = x
	}
	return o, v.k == Object
}
func (v Value) IsData() bool {
	if v.k == Function || v.k == Effect {
		return false
	}
	for _, x := range v.l {
		if !x.IsData() {
			return false
		}
	}
	for _, x := range v.o {
		if !x.IsData() {
			return false
		}
	}
	return true
}

// Index arithmetic stays in int64 until bounds have been checked. LIP safe
// integers fit exactly, including on platforms whose Go int is only 32 bits.
func indexInteger(x Value) (int64, error) {
	if x.k != Number {
		return 0, diagnostic("E-TYPE", "index requires number")
	}
	if math.IsNaN(x.n) || math.IsInf(x.n, 0) || math.Abs(x.n) > 9007199254740991 || math.Trunc(x.n) != x.n {
		return 0, diagnostic("E-INDEX", "index must be a safe integer")
	}
	return int64(x.n), nil
}
func relativeIndex(x Value, length int) (int, error) {
	i, err := indexInteger(x)
	if err != nil {
		return 0, err
	}
	if i < 0 {
		i += int64(length)
	}
	if i < 0 || i >= int64(length) {
		return 0, diagnostic("E-INDEX", "index out of bounds")
	}
	return int(i), nil
}

// Null bounds denote omissions. In particular, omitted reverse stop is -1,
// whereas explicit -1 is first made relative to length.
func slicePlan(length int, bounds []Value) (start, step, count int64, err error) {
	var parts [3]int64
	for i, x := range bounds {
		if x.k != Null {
			parts[i], err = indexInteger(x)
			if err != nil {
				return
			}
		}
	}
	step = 1
	if len(bounds) == 3 && bounds[2].k != Null {
		step = parts[2]
	}
	if step == 0 {
		err = diagnostic("E-VALUE", "slice step cannot be zero")
		return
	}
	n := int64(length)
	low, high := int64(0), n
	start, stop := int64(0), n
	if step < 0 {
		low, high = -1, n-1
		start, stop = n-1, -1
	}
	for i, target := range []*int64{&start, &stop} {
		if bounds[i].k == Null {
			continue
		}
		b := parts[i]
		if b < 0 {
			b += n
		}
		if b < low {
			b = low
		}
		if b > high {
			b = high
		}
		*target = b
	}
	if step > 0 && start < stop {
		count = (stop-start-1)/step + 1
	} else if step < 0 && start > stop {
		count = (start-stop-1)/(-step) + 1
	}
	return start, step, count, nil
}

// Shared by core evaluation and native Data projection. Optional callbacks
// enforce the evaluator's allocation/step budget or the Host's cancellation.
func indexValue(x, index Value, reserve func(int64) error, tick func() error) (Value, error) {
	if x.k == Object {
		if index.k != String {
			return Value{}, diagnostic("E-TYPE", "object index requires string")
		}
		out, ok := x.o[index.s]
		if !ok {
			return Value{}, diagnostic("E-INDEX", "missing key "+index.s)
		}
		return out, nil
	}
	if x.k != List && x.k != String {
		return Value{}, diagnostic("E-TYPE", "value cannot be indexed")
	}
	if x.k == List && index.k == List {
		if reserve != nil {
			if err := reserve(int64(len(index.l))); err != nil {
				return Value{}, err
			}
		}
		out := make([]Value, len(index.l))
		for j, element := range index.l {
			if tick != nil {
				if err := tick(); err != nil {
					return Value{}, err
				}
			}
			i, err := relativeIndex(element, len(x.l))
			if err != nil {
				return Value{}, err
			}
			out[j] = x.l[i]
		}
		return Value{k: List, l: out}, nil
	}
	length := len(x.l)
	var rs []rune
	if x.k == String {
		rs = []rune(x.s)
		length = len(rs)
	}
	i, err := relativeIndex(index, length)
	if err != nil {
		return Value{}, err
	}
	if x.k == String {
		return textValue(string(rs[i])), nil
	}
	return x.l[i], nil
}
func sliceValue(x Value, bounds []Value, reserve func(int64) error, tick func() error) (Value, error) {
	if x.k != List && x.k != String {
		return Value{}, diagnostic("E-TYPE", "slice requires list or string")
	}
	length := len(x.l)
	var rs []rune
	if x.k == String {
		rs = []rune(x.s)
		length = len(rs)
	}
	start, step, count, err := slicePlan(length, bounds)
	if err != nil {
		return Value{}, err
	}
	var out []Value
	var text []rune
	if x.k == List {
		if reserve != nil {
			if err := reserve(count); err != nil {
				return Value{}, err
			}
		}
		out = make([]Value, int(count))
	} else {
		text = make([]rune, int(count))
	}
	for j, i := int64(0), start; j < count; j, i = j+1, i+step {
		if tick != nil {
			if err := tick(); err != nil {
				return Value{}, err
			}
		}
		if x.k == List {
			out[j] = x.l[i]
		} else {
			text[j] = rs[i]
		}
	}
	if x.k == String {
		return textValue(string(text)), nil
	}
	return Value{k: List, l: out}, nil
}
func (v Value) Native() (any, error) {
	if !v.IsData() {
		return nil, diagnostic("E-VALUE", "value is not Data")
	}
	switch v.k {
	case Null:
		return nil, nil
	case Bool:
		return v.b, nil
	case Number:
		return v.n, nil
	case String:
		return v.s, nil
	case Char:
		return string(v.r), nil
	case List:
		xs := make([]any, len(v.l))
		for i, x := range v.l {
			xs[i], _ = x.Native()
		}
		return xs, nil
	case Object:
		o := make(map[string]any, len(v.o))
		for k, x := range v.o {
			o[k], _ = x.Native()
		}
		return o, nil
	}
	return nil, diagnostic("E-VALUE", "invalid value")
}

// DecodeData validates JSON without silently dropping duplicate object keys.
func DecodeData(data []byte) (Value, error) {
	if !utf8.Valid(data) {
		return Value{}, diagnostic("E-VALUE", "JSON is not UTF-8")
	}
	if err := validateJSONScalars(data); err != nil {
		return Value{}, err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, err := decodeJSON(d)
	if err != nil {
		return Value{}, err
	}
	if _, err = d.Token(); err != io.EOF {
		return Value{}, diagnostic("E-VALUE", "trailing JSON input")
	}
	return v, nil
}

// encoding/json replaces unpaired UTF-16 surrogates. Reject them before that
// lossy conversion can erase invalid boundary data.
func validateJSONScalars(data []byte) error {
	inString := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			break
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			break
		}
		code, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			break
		}
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return diagnostic("E-VALUE", "unpaired JSON surrogate")
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return diagnostic("E-VALUE", "unpaired JSON surrogate")
			}
			low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return diagnostic("E-VALUE", "unpaired JSON surrogate")
			}
			i += 6
		}
	}
	return nil
}
func decodeJSON(d *json.Decoder) (Value, error) {
	t, err := d.Token()
	if err != nil {
		return Value{}, diagnostic("E-VALUE", err.Error())
	}
	switch x := t.(type) {
	case nil:
		return Value{}, nil
	case bool:
		return BoolValue(x), nil
	case string:
		return StringValue(x)
	case json.Number:
		n, err := strconv.ParseFloat(string(x), 64)
		if err != nil {
			return Value{}, diagnostic("E-VALUE", "non-finite JSON number")
		}
		return NumberValue(n)
	case json.Delim:
		if x == '[' {
			xs := []Value{}
			for d.More() {
				v, e := decodeJSON(d)
				if e != nil {
					return Value{}, e
				}
				xs = append(xs, v)
			}
			if _, e := d.Token(); e != nil {
				return Value{}, diagnostic("E-VALUE", e.Error())
			}
			return ListValue(xs...), nil
		}
		if x == '{' {
			o := map[string]Value{}
			for d.More() {
				t, e := d.Token()
				if e != nil {
					return Value{}, diagnostic("E-VALUE", e.Error())
				}
				k, ok := t.(string)
				if !ok {
					return Value{}, diagnostic("E-VALUE", "object key must be string")
				}
				if _, dup := o[k]; dup {
					return Value{}, diagnostic("E-VALUE", "duplicate JSON key: "+k)
				}
				v, e := decodeJSON(d)
				if e != nil {
					return Value{}, e
				}
				o[k] = v
			}
			if _, e := d.Token(); e != nil {
				return Value{}, diagnostic("E-VALUE", e.Error())
			}
			return ObjectValue(o)
		}
	}
	return Value{}, diagnostic("E-VALUE", "invalid JSON")
}

func Equal(a, b Value) (bool, error) {
	// Forbidden nested values are rejected even when a shallow mismatch exists.
	if !a.IsData() || !b.IsData() {
		return false, diagnostic("E-TYPE", "function/effect equality is undefined")
	}
	if a.k != b.k {
		return false, nil
	}
	switch a.k {
	case Null:
		return true, nil
	case Bool:
		return a.b == b.b, nil
	case Number:
		return a.n == b.n, nil
	case String:
		return a.s == b.s, nil
	case Char:
		return a.r == b.r, nil
	case List:
		if len(a.l) != len(b.l) {
			return false, nil
		}
		for i := range a.l {
			ok, _ := Equal(a.l[i], b.l[i])
			if !ok {
				return false, nil
			}
		}
		return true, nil
	case Object:
		if len(a.o) != len(b.o) {
			return false, nil
		}
		for k, x := range a.o {
			y, found := b.o[k]
			if !found {
				return false, nil
			}
			ok, _ := Equal(x, y)
			if !ok {
				return false, nil
			}
		}
		return true, nil
	}
	return false, diagnostic("E-TYPE", "unsupported equality")
}
func sortedKeys(o map[string]Value) []string {
	keys := make([]string, 0, len(o))
	for k := range o {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func Canonical(v Value) ([]byte, error) {
	if !v.IsData() {
		return nil, diagnostic("E-VALUE", "canonical encoding requires Data")
	}
	var b bytes.Buffer
	encodeCanonical(&b, v)
	return b.Bytes(), nil
}
func encodeCanonical(b *bytes.Buffer, v Value) {
	put := func(n uint64) { var xs [8]byte; binary.BigEndian.PutUint64(xs[:], n); b.Write(xs[:]) }
	switch v.k {
	case Null:
		b.WriteByte(0)
	case Bool:
		if v.b {
			b.WriteByte(2)
		} else {
			b.WriteByte(1)
		}
	case Number:
		b.WriteByte(3)
		put(math.Float64bits(v.n))
	case String:
		b.WriteByte(4)
		put(uint64(len(v.s)))
		b.WriteString(v.s)
	case Char:
		b.WriteByte(7)
		put(uint64(v.r))
	case List:
		b.WriteByte(5)
		put(uint64(len(v.l)))
		for _, x := range v.l {
			encodeCanonical(b, x)
		}
	case Object:
		b.WriteByte(6)
		put(uint64(len(v.o)))
		for _, k := range sortedKeys(v.o) {
			encodeCanonical(b, textValue(k))
			encodeCanonical(b, v.o[k])
		}
	}
}

// DecodeCanonical accepts only C(v), including canonical zero and NaN. Lengths
// are checked against the remaining input before allocating container storage.
func DecodeCanonical(data []byte) (Value, error) {
	d := canonicalDecoder{data: data}
	v, err := d.value(0)
	if err == nil && d.offset != len(data) {
		err = diagnostic("E-VALUE", "trailing canonical input")
	}
	return v, err
}

type canonicalDecoder struct {
	data   []byte
	offset int
}

func (d *canonicalDecoder) take(n uint64) ([]byte, error) {
	if n > uint64(len(d.data)-d.offset) {
		return nil, diagnostic("E-VALUE", "truncated canonical input")
	}
	b := d.data[d.offset : d.offset+int(n)]
	d.offset += int(n)
	return b, nil
}
func (d *canonicalDecoder) size() (uint64, error) {
	b, err := d.take(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b), nil
}
func (d *canonicalDecoder) value(depth int) (Value, error) {
	if depth > 1024 {
		return Value{}, diagnostic("E-VALUE", "canonical nesting budget exceeded")
	}
	tag, err := d.take(1)
	if err != nil {
		return Value{}, err
	}
	switch tag[0] {
	case 0:
		return NullValue(), nil
	case 1, 2:
		return BoolValue(tag[0] == 2), nil
	case 3:
		bits, err := d.size()
		if err != nil {
			return Value{}, err
		}
		n := math.Float64frombits(bits)
		if bits == 0x8000000000000000 || math.IsNaN(n) && bits != 0x7ff8000000000000 {
			return Value{}, diagnostic("E-VALUE", "noncanonical number")
		}
		return NumberValue(n)
	case 4:
		n, err := d.size()
		if err != nil {
			return Value{}, err
		}
		b, err := d.take(n)
		if err != nil {
			return Value{}, err
		}
		return StringValue(string(b))
	case 7:
		n, err := d.size()
		if err != nil {
			return Value{}, err
		}
		if n > utf8.MaxRune || n >= 0xd800 && n <= 0xdfff {
			return Value{}, diagnostic("E-VALUE", "invalid canonical char")
		}
		return CharValue(rune(n))
	case 5, 6:
		n, err := d.size()
		if err != nil {
			return Value{}, err
		}
		remaining := uint64(len(d.data) - d.offset)
		if tag[0] == 6 {
			remaining /= 10
		} // shortest string key + null value
		if n > remaining {
			return Value{}, diagnostic("E-VALUE", "invalid canonical container length")
		}
		if tag[0] == 5 {
			xs := make([]Value, int(n))
			for i := range xs {
				xs[i], err = d.value(depth + 1)
				if err != nil {
					return Value{}, err
				}
			}
			return Value{k: List, l: xs}, nil
		}
		o := make(map[string]Value, int(n))
		previous := ""
		for i := uint64(0); i < n; i++ {
			key, err := d.value(depth + 1)
			if err != nil {
				return Value{}, err
			}
			if key.k != String || i > 0 && key.s <= previous {
				return Value{}, diagnostic("E-VALUE", "canonical object keys must be sorted unique strings")
			}
			previous = key.s
			x, err := d.value(depth + 1)
			if err != nil {
				return Value{}, err
			}
			o[key.s] = x
		}
		return Value{k: Object, o: o}, nil
	}
	return Value{}, diagnostic("E-VALUE", "unknown canonical tag")
}

func checkDataEncoding(encoding string) error {
	if encoding != "json" && encoding != "c-hex" {
		return diagnostic("E-VALUE", "unknown Data encoding "+encoding)
	}
	return nil
}

func canonicalHex(v Value) (Value, error) {
	b, err := Canonical(v)
	if err != nil {
		return Value{}, err
	}
	return textValue(hex.EncodeToString(b)), nil
}
func decodeCanonicalHex(v Value) (Value, error) {
	if v.k != String || len(v.s)%2 != 0 || strings.IndexFunc(v.s, func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') }) >= 0 {
		return Value{}, diagnostic("E-VALUE", "c-hex requires a lowercase even-length hexadecimal JSON string")
	}
	b, err := hex.DecodeString(v.s)
	if err != nil {
		return Value{}, diagnostic("E-VALUE", "invalid c-hex")
	}
	return DecodeCanonical(b)
}

// EncodeData and DecodeDataWithEncoding select an explicit transport profile;
// they never infer tags from the shape of a business value.
func EncodeData(v Value, encoding string) ([]byte, error) {
	if err := checkDataEncoding(encoding); err != nil {
		return nil, err
	}
	if encoding == "c-hex" {
		var err error
		v, err = canonicalHex(v)
		if err != nil {
			return nil, err
		}
	}
	return EncodeJSON(v)
}
func DecodeDataWithEncoding(data []byte, encoding string) (Value, error) {
	if err := checkDataEncoding(encoding); err != nil {
		return Value{}, err
	}
	v, err := DecodeData(data)
	if err == nil && encoding == "c-hex" {
		return decodeCanonicalHex(v)
	}
	return v, err
}

func Display(v Value) (string, error) {
	if v.k == Effect {
		if v.p == nil {
			return "", diagnostic("E-TYPE", "str requires Data or a valid effect proposal")
		}
		return effectSummary(v.p), nil
	}
	if !v.IsData() {
		return "", diagnostic("E-TYPE", "str requires Data or an effect proposal")
	}
	if v.k == String {
		return v.s, nil
	}
	if v.k == Char {
		return string(v.r), nil
	}
	var b strings.Builder
	displayJSON(&b, v)
	return b.String(), nil
}

// effectSummaryValue renders only the immutable proposal identity, contract
// and Data arguments. It deliberately omits CallID, run state and the provider
// function: str(effect) is a pure review value and must never execute or
// inspect the external operation.
func effectSummaryValue(p *proposal) Value {
	if p == nil {
		return NullValue()
	}
	contract := p.contract
	params := make([]Value, len(contract.Params))
	for i, param := range contract.Params {
		params[i] = object(map[string]Value{
			"name":     textValue(param.Name),
			"optional": BoolValue(param.Optional),
			"schema":   schemaSummary(param.Schema),
		})
	}
	capabilities := append([]string(nil), contract.Capabilities...)
	sort.Strings(capabilities)
	capabilityValues := make([]Value, len(capabilities))
	for i, capability := range capabilities {
		capabilityValues[i] = textValue(capability)
	}
	resources := make(map[string]Value, len(contract.Resources))
	for name, units := range contract.Resources {
		if units <= 9007199254740991 {
			resources[name] = number(float64(units))
		} else {
			// Host resource units are uint64 while LIP numbers are safe
			// integers. Keep unusually large contract values exact rather than
			// rounding them in a review summary.
			resources[name] = textValue(strconv.FormatUint(units, 10))
		}
	}
	positional := make([]Value, len(p.positional))
	for i, value := range p.positional {
		positional[i] = reviewData(value)
	}
	named := make(map[string]Value, len(p.named))
	for name, value := range p.named {
		named[name] = reviewData(value)
	}
	arguments := object(map[string]Value{
		"named":      object(named),
		"positional": ListValue(positional...),
	})
	summary := object(map[string]Value{
		"arguments": arguments,
		"contract": object(map[string]Value{
			"capabilities": ListValue(capabilityValues...),
			"concurrent":   BoolValue(contract.Concurrent),
			"effect":       textValue(effectClassName(contract.Effect)),
			"parameters":   ListValue(params...),
			"resources":    object(resources),
			"result":       schemaSummary(contract.Result),
			"version":      textValue(contract.Version),
		}),
		"kind": textValue("effect"),
		"operation": object(map[string]Value{
			"language": textValue(contract.Operation.Language),
			"member":   textValue(contract.Operation.Member),
			"path":     textValue(contract.Operation.Path),
		}),
	})
	return summary
}

func effectSummary(p *proposal) string {
	var b strings.Builder
	displayJSON(&b, effectSummaryValue(p))
	return b.String()
}

// effectReviewText is the human-facing diagnostic/help form. Keep operation
// first and put each review field after an easy-to-search label; str(effect)
// remains the compact canonical form used for exact comparisons.
func effectReviewText(p *proposal) string {
	if p == nil {
		return "<effect>"
	}
	summary := effectSummaryValue(p)
	var b strings.Builder
	line := func(name, value string) {
		if b.Len() != 0 {
			b.WriteByte('\n')
		}
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(value)
	}
	// Every leaf gets its own path: value line. This keeps arbitrary nested
	// argument and schema fields searchable without requiring a JSON parser.
	var appendPath func(string, Value)
	appendPath = func(path string, value Value) {
		switch value.k {
		case Object:
			keys := sortedKeys(value.o)
			if len(keys) == 0 {
				line(path, "{}")
				return
			}
			for _, key := range keys {
				child := key
				if path != "" {
					child = path + reviewPathKey(key)
				}
				appendPath(child, value.o[key])
			}
		case List:
			if len(value.l) == 0 {
				line(path, "[]")
				return
			}
			for i, item := range value.l {
				appendPath(fmt.Sprintf("%s[%d]", path, i), item)
			}
		default:
			var encoded strings.Builder
			displayJSON(&encoded, value)
			line(path, encoded.String())
		}
	}
	// The dotted operation spelling is deliberately a human locator, not a
	// new identity or transport encoding. The complete summary remains the
	// lossless canonical str(effect) form.
	line("operation", effectOperationText(p.contract.Operation))
	if operation, ok := summary.o["operation"]; ok {
		if operation.k == Object {
			seen := map[string]bool{}
			for _, key := range []string{"language", "path", "member"} {
				if value, present := operation.o[key]; present {
					appendPath("operation."+key, value)
					seen[key] = true
				}
			}
			for _, key := range sortedKeys(operation.o) {
				if !seen[key] {
					appendPath("operation."+key, operation.o[key])
				}
			}
		} else {
			appendPath("operation", operation)
		}
	}
	if kind, ok := summary.o["kind"]; ok {
		appendPath("kind", kind)
	}
	for _, name := range []string{"contract", "arguments"} {
		if value, ok := summary.o[name]; ok {
			appendPath(name, value)
		}
	}
	return b.String()
}

func reviewPathKey(key string) string {
	if validID(key) {
		return "." + key
	}
	return "[" + strconv.Quote(key) + "]"
}

func effectOperationText(operation Operation) string {
	if operation.Path == "" {
		return operation.Language + "." + operation.Member
	}
	return operation.Language + "." + operation.Path + "." + operation.Member
}

func reviewData(v Value) Value {
	if text, ok := NativeDescription(v); ok {
		return textValue(text)
	}
	switch v.k {
	case List:
		values := make([]Value, len(v.l))
		for i, value := range v.l {
			values[i] = reviewData(value)
		}
		return ListValue(values...)
	case Object:
		fields := make(map[string]Value, len(v.o))
		for name, value := range v.o {
			fields[name] = reviewData(value)
		}
		return object(fields)
	default:
		return v
	}
}

func effectClassName(effect EffectClass) string {
	switch effect {
	case ReadOnlyEffect:
		return "ReadOnly"
	case WriteEffect:
		return "Write"
	case UnknownEffect:
		return "Unknown"
	case PureEffect:
		return "Pure"
	default:
		return "Unknown"
	}
}

func schemaSummary(schema Schema) Value {
	fields := make(map[string]Value, len(schema.Fields))
	for name, field := range schema.Fields {
		fields[name] = schemaSummary(field)
	}
	required := append([]string(nil), schema.Required...)
	sort.Strings(required)
	requiredValues := make([]Value, len(required))
	for i, name := range required {
		requiredValues[i] = textValue(name)
	}
	result := map[string]Value{"type": textValue(schema.Type)}
	if schema.Items != nil {
		result["items"] = schemaSummary(*schema.Items)
	}
	if len(fields) > 0 {
		result["fields"] = object(fields)
	}
	if len(requiredValues) > 0 {
		result["required"] = ListValue(requiredValues...)
	}
	return object(result)
}
func EncodeJSON(v Value) ([]byte, error) {
	if !v.IsData() {
		return nil, diagnostic("E-VALUE", "JSON requires Data")
	}
	if containsNonFinite(v) {
		return nil, diagnostic("E-VALUE", "JSON cannot encode NaN or infinity; use c-hex")
	}
	var b strings.Builder
	displayJSON(&b, v)
	return []byte(b.String()), nil
}
func displayJSON(b *strings.Builder, v Value) {
	switch v.k {
	case Null:
		b.WriteString("null")
	case Bool:
		b.WriteString(strconv.FormatBool(v.b))
	case Number:
		b.WriteString(formatNumber(v.n))
	case String:
		b.WriteByte('"')
		for _, r := range v.s {
			if r == '"' || r == '\\' {
				b.WriteByte('\\')
				b.WriteRune(r)
			} else if r < 32 {
				fmt.Fprintf(b, "\\u%04x", r)
			} else {
				b.WriteRune(r)
			}
		}
		b.WriteByte('"')
	case Char:
		b.WriteByte('"')
		for _, r := range string(v.r) {
			if r == '"' || r == '\\' {
				b.WriteByte('\\')
				b.WriteRune(r)
			} else if r < 32 {
				fmt.Fprintf(b, "\\u%04x", r)
			} else {
				b.WriteRune(r)
			}
		}
		b.WriteByte('"')
	case List:
		b.WriteByte('[')
		for i, x := range v.l {
			if i > 0 {
				b.WriteByte(',')
			}
			displayJSON(b, x)
		}
		b.WriteByte(']')
	case Object:
		b.WriteByte('{')
		for i, k := range sortedKeys(v.o) {
			if i > 0 {
				b.WriteByte(',')
			}
			displayJSON(b, textValue(k))
			b.WriteByte(':')
			displayJSON(b, v.o[k])
		}
		b.WriteByte('}')
	}
}

// Select the shortest round-trip decimal by exact distance, then magnitude.
// Ryu is used only to find the decimal exponent, not to choose a tie breaker.
func formatNumber(n float64) string {
	if math.IsNaN(n) {
		return "nan"
	}
	if math.IsInf(n, 1) {
		return "inf"
	}
	if math.IsInf(n, -1) {
		return "-inf"
	}
	if n == 0 {
		return "0"
	}
	negative := n < 0
	n = math.Abs(n)
	r := new(big.Rat).SetFloat64(n)
	es := strconv.FormatFloat(n, 'e', -1, 64)
	k, _ := strconv.Atoi(es[strings.IndexByte(es, 'e')+1:])
	var digits string
	var scale int
	for count := 1; count <= 17; count++ {
		scale = k - count + 1
		step := pow10Rat(scale)
		qrat := new(big.Rat).Quo(r, step)
		q := new(big.Int).Quo(qrat.Num(), qrat.Denom())
		var best *big.Rat
		var bestQ *big.Int
		for _, candidate := range []*big.Int{q, new(big.Int).Add(q, big.NewInt(1))} {
			if candidate.Sign() == 0 {
				continue
			}
			s := candidate.String() + "e" + strconv.Itoa(scale)
			roundtrip, err := strconv.ParseFloat(s, 64)
			if err != nil || roundtrip != n {
				continue
			}
			distance := new(big.Rat).Sub(new(big.Rat).Mul(new(big.Rat).SetInt(candidate), step), r)
			distance.Abs(distance)
			if best == nil || distance.Cmp(best) < 0 || distance.Cmp(best) == 0 && candidate.Cmp(bestQ) < 0 {
				best, bestQ = distance, candidate
			}
		}
		if bestQ != nil {
			digits = bestQ.String()
			break
		}
	}
	for len(digits) > 1 && digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
		scale++
	}
	k = len(digits) - 1 + scale
	var result string
	if k >= -6 && k < 21 {
		point := len(digits) + scale
		if point <= 0 {
			result = "0." + strings.Repeat("0", -point) + digits
		} else if point >= len(digits) {
			result = digits + strings.Repeat("0", point-len(digits))
		} else {
			result = digits[:point] + "." + digits[point:]
		}
	} else {
		result = digits[:1]
		if len(digits) > 1 {
			result += "." + digits[1:]
		}
		result += "e" + strconv.Itoa(k)
	}
	if negative {
		result = "-" + result
	}
	return result
}
func pow10Rat(e int) *big.Rat {
	n := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(absInt(e))), nil)
	if e < 0 {
		return new(big.Rat).SetFrac(big.NewInt(1), n)
	}
	return new(big.Rat).SetInt(n)
}
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// arithmeticNumber preserves the complete binary64 result domain.  LIP
// normalizes NaN payloads and signed zero in NumberValue, while retaining
// infinities produced by IEEE-754 division, overflow, and exponentiation.
func arithmeticNumber(n float64) (Value, error) { return NumberValue(n) }
func containsNonFinite(v Value) bool {
	if v.k == Number && (math.IsNaN(v.n) || math.IsInf(v.n, 0)) {
		return true
	}
	for _, x := range v.l {
		if containsNonFinite(x) {
			return true
		}
	}
	for _, x := range v.o {
		if containsNonFinite(x) {
			return true
		}
	}
	return false
}

func arithmetic(op string, a, b Value) (Value, error) {
	if op == "+" && a.k == String && b.k == String {
		return textValue(a.s + b.s), nil
	}
	if a.k != Number || b.k != Number {
		return Value{}, diagnostic("E-TYPE", "arithmetic requires numbers")
	}
	if op == "**" && (b.n == 0 || a.n == 1) {
		return number(1), nil
	}
	if math.IsNaN(a.n) || math.IsNaN(b.n) {
		return nanValue(), nil
	}
	// IEEE-754 division by zero is a value operation: non-zero finite values
	// produce a signed infinity and 0/0 produces NaN.  Floor division and
	// remainder have no IEEE quotient at a zero divisor, so their invalid
	// result is the canonical NaN as well.  This keeps the operation total over
	// the language number domain instead of turning a numeric result into a
	// control-flow error.
	if (op == "//" || op == "%") && b.n == 0 {
		return nanValue(), nil
	}
	if math.IsInf(a.n, 0) || math.IsInf(b.n, 0) {
		return infiniteArithmetic(op, a.n, b.n)
	}
	switch op {
	case "+":
		return arithmeticNumber(a.n + b.n)
	case "-":
		return arithmeticNumber(a.n - b.n)
	case "*":
		return arithmeticNumber(a.n * b.n)
	case "/":
		return arithmeticNumber(a.n / b.n)
	case "**":
		if a.n == 0 && b.n < 0 {
			return infValue(), nil
		}
		if a.n < 0 && math.Trunc(b.n) != b.n {
			return nanValue(), nil
		}
		return arithmeticNumber(corePower(a.n, b.n))
	case "*/":
		if a.n < 0 || b.n <= 0 || b.n == 1 {
			return nanValue(), nil
		}
		if a.n == 0 {
			if b.n > 1 {
				return number(math.Inf(-1)), nil
			}
			return infValue(), nil
		}
		if a.n == 1 {
			return number(0), nil
		}
		if a.n == b.n {
			return number(1), nil
		}
		return arithmeticNumber(numericDiv(coreLog(a.n), coreLog(b.n)))
	case "//", "%":
		x, y := new(big.Rat).SetFloat64(a.n), new(big.Rat).SetFloat64(b.n)
		q := new(big.Rat).Quo(x, y)
		whole, rest := new(big.Int), new(big.Int)
		whole.QuoRem(q.Num(), q.Denom(), rest)
		if q.Sign() < 0 && rest.Sign() != 0 {
			whole.Sub(whole, big.NewInt(1))
		}
		if op == "//" {
			n, _ := new(big.Rat).SetInt(whole).Float64()
			return arithmeticNumber(n)
		}
		r := new(big.Rat).Sub(x, new(big.Rat).Mul(new(big.Rat).SetInt(whole), y))
		n, _ := r.Float64()
		if n == b.n {
			n = math.Nextafter(b.n, 0)
		}
		return arithmeticNumber(n)
	}
	return Value{}, diagnostic("E-TYPE", "unknown arithmetic operator")
}

// Infinite operands are handled before the fixed finite algorithms and exact
// rational division is used only for finite operands; rounded overflow remains a value.
func infiniteArithmetic(op string, a, b float64) (Value, error) {
	switch op {
	case "+":
		return NumberValue(a + b)
	case "-":
		return NumberValue(a - b)
	case "*":
		return NumberValue(a * b)
	case "/":
		return NumberValue(a / b)
	case "//", "%":
		if math.IsInf(a, 0) {
			return nanValue(), nil
		}
		// The infinite-divisor cases are the one-sided limits of floor
		// division and its corresponding remainder, as in Python.
		opposite := a != 0 && math.Signbit(a) != math.Signbit(b)
		if op == "//" {
			if opposite {
				return number(-1), nil
			}
			return number(0), nil
		}
		if opposite {
			return number(b), nil
		}
		return number(a), nil
	case "**":
		if math.IsInf(b, 0) {
			magnitude := math.Abs(a)
			if magnitude == 1 {
				return number(1), nil
			}
			if (magnitude > 1) == (b > 0) {
				return infValue(), nil
			}
			return number(0), nil
		}
		if b < 0 {
			return number(0), nil
		}
		// IEEE real pow's infinite-base cases: a negative infinite base
		// has a negative result only for an odd integer exponent.
		if a < 0 && math.Trunc(b) == b && math.Mod(b, 2) != 0 {
			return number(math.Inf(-1)), nil
		}
		return infValue(), nil
	case "*/":
		if a < 0 || b <= 0 || b == 1 {
			return nanValue(), nil
		}
		if a == 0 {
			if b > 1 {
				return number(math.Inf(-1)), nil
			}
			return infValue(), nil
		}
		if math.IsInf(a, 1) {
			if math.IsInf(b, 1) {
				return nanValue(), nil
			}
			if b < 1 {
				return number(math.Inf(-1)), nil
			}
			return infValue(), nil
		}
		return number(0), nil
	}
	return Value{}, diagnostic("E-TYPE", "unknown arithmetic operator")
}

// Fixed binary64 algorithms, defined in spec §16.5. Explicit conversions forbid
// fused multiply-add even when these helpers are inlined by the Go compiler.
// These approximate real powers/logarithms; they do not promise correct rounding.
func numericAdd(a, b float64) float64 { return float64(a + b) }
func numericSub(a, b float64) float64 { return float64(a - b) }
func numericMul(a, b float64) float64 { return float64(a * b) }
func numericDiv(a, b float64) float64 { return float64(a / b) }

const numericLn2Hi = 0x1.62e42fee00000p-1
const numericLn2Lo = 0x1.a39ef35793c76p-33

func coreLog(x float64) float64 {
	m, k := math.Frexp(x)
	if m < 0x1.6a09e667f3bcdp-1 {
		m = numericMul(m, 2)
		k--
	}
	z := numericDiv(numericSub(m, 1), numericAdd(m, 1))
	z2 := numericMul(z, z)
	term, sum := z, z
	for j := 1; j < 24; j++ {
		term = numericMul(term, z2)
		sum = numericAdd(sum, numericDiv(term, float64(2*j+1)))
	}
	return numericAdd(numericMul(float64(k), numericLn2Hi),
		numericAdd(numericMul(2, sum), numericMul(float64(k), numericLn2Lo)))
}

func coreExp(x float64) float64 {
	if x > 0x1.62e42fefa39efp+9 {
		return math.Inf(1)
	}
	if x < -0x1.74910d52d3052p+9 {
		return 0
	}
	ln2 := numericAdd(numericLn2Hi, numericLn2Lo)
	k := int(math.Floor(numericAdd(numericDiv(x, ln2), 0.5)))
	r := numericSub(numericSub(x, numericMul(float64(k), numericLn2Hi)),
		numericMul(float64(k), numericLn2Lo))
	term, sum := 1.0, 1.0
	for j := 1; j <= 24; j++ {
		term = numericDiv(numericMul(term, r), float64(j))
		sum = numericAdd(sum, term)
	}
	return math.Ldexp(sum, k)
}

func corePower(x, y float64) float64 {
	if y == 0 || x == 1 {
		return 1
	}
	if y == 1 || x == 0 {
		return x
	}
	if y == 0.5 {
		return math.Sqrt(x)
	}
	if y == -0.5 {
		return numericDiv(1, math.Sqrt(x))
	}
	negative := x < 0 && math.Abs(y) < 0x1p53 && int64(math.Abs(y))&1 != 0
	x = math.Abs(x)
	if math.Abs(y) >= 0x1p63 {
		if x == 1 {
			return 1
		}
		if (x < 1) == (y > 0) {
			return 0
		}
		return math.Inf(1)
	}
	whole := math.Floor(math.Abs(y))
	fraction := numericSub(math.Abs(y), whole)
	if fraction > 0.5 {
		fraction = numericSub(fraction, 1)
		whole = numericAdd(whole, 1)
	}
	a, scale := 1.0, 0
	if fraction != 0 {
		a = coreExp(numericMul(fraction, coreLog(x)))
	}
	m, e := math.Frexp(x)
	for n := uint64(whole); n != 0; n >>= 1 {
		if e < -8192 || e > 8192 {
			scale += e
			break
		}
		if n&1 != 0 {
			a = numericMul(a, m)
			scale += e
		}
		m = numericMul(m, m)
		e *= 2
		if m < 0.5 {
			m = numericMul(m, 2)
			e--
		}
	}
	if y < 0 {
		a = numericDiv(1, a)
		scale = -scale
	}
	a = math.Ldexp(a, scale)
	if negative {
		a = -a
	}
	return a
}

func normalizeType(typ string) string {
	if typ == "any?" {
		return "any"
	}
	return typ
}

func checkType(v Value, typ string) error {
	typ = normalizeType(typ)
	if typ == "any" {
		return nil
	}
	if strings.HasSuffix(typ, "?") {
		if v.k == Null {
			return nil
		}
		typ = strings.TrimSuffix(typ, "?")
	}
	if v.k.String() != typ {
		return diagnostic("E-TYPE", "expected "+typ+", got "+v.k.String())
	}
	return nil
}

type Diagnostic struct {
	Phase    string
	SourceID string
	Code     string
	Message  string
	// Hint explains the LIP-specific rule behind a common migration error.
	// Message stays concise and stable for API consumers; Error renders Hint
	// as a second line for command-line users.
	Hint     string
	Site     int
	Line     int
	Column   int
	Position Position
	EvalPath Position
	Details  Value
}

func (e *Diagnostic) Error() string {
	phase := e.Phase
	if phase == "" {
		phase = "host"
	}
	message := "[" + phase + "] " + e.Message
	if e.Hint != "" {
		message += "\nHint: " + e.Hint
	}
	if e.Line > 0 {
		if e.SourceID != "" {
			return fmt.Sprintf("%s at %s:%d:%d: %s", e.Code, e.SourceID, e.Line, e.Column, message)
		}
		return fmt.Sprintf("%s at %d:%d: %s", e.Code, e.Line, e.Column, message)
	}
	return e.Code + ": " + message
}
func diagnostic(code, message string) *Diagnostic {
	return &Diagnostic{Phase: "host", Code: code, Message: message, Hint: DiagnosticHint(code, message), Site: -1}
}

// DiagnosticHint returns the canonical English migration guidance for a
// diagnostic. Compiler, REPL, learn, examples, and editor/LSP adapters should
// use this shared knowledge instead of maintaining message-specific copies.
// Message matching is intentionally based on stable diagnostic families, so
// the public Message field remains concise and compatible for API consumers.
func DiagnosticHint(code, message string) string {
	switch {
	case strings.Contains(message, "Flow declarations are not values"):
		return "A Flow is a static execution graph, not an ordinary value. Call it directly as Name(args); use fn for a passable pure callable, or construct an effect for a deferred external request."
	case strings.Contains(message, "fn cannot invoke Flow"):
		return "A regular fn cannot invoke a Flow. Move the Flow call into a Flow body, or let the fn construct and return an effect for a Flow to perform."
	case strings.Contains(message, "void Flow cannot be used as a value"):
		return "A void Flow has no result value. Invoke it as a statement, or give the Flow a return type before using its result."
	case strings.Contains(message, "fn cannot capture/read active Claim"):
		return "A Claim store belongs to the current Flow graph and cannot be captured by a pure fn. Read a completed snapshot in the Flow and pass that Data to the fn."
	case strings.Contains(message, "fn cannot construct store snapshot"):
		return "A Claim snapshot is an external read and can only be performed in a Flow. Read it in the Flow, then pass the result to a pure fn."
	case strings.Contains(message, "native calls in fn require"):
		return "A regular fn cannot make an unmarked native call. Move external business work to a Flow with effect/perform; if this native computation is genuinely pure, mark this fn body @{...} as an author purity promise."
	case strings.Contains(message, "perform requires proposal"):
		return "perform requires an unexecuted effect proposal, not an ordinary value or native result. Construct one with effect alias.member(args), or pass an effect value, then perform it from a Flow."
	case strings.Contains(message, "proposal arguments must be pure"):
		return "Effect arguments must be pure Data: constructing a request does not perform external work. Complete an external read in a Flow, then build the effect from its bound Data."
	case strings.Contains(message, "proposal argument must be Data"):
		return "An effect proposal can carry only Data arguments. Perform or unwrap the external work first, then pass its Data result; functions and effect proposals are not Data."
	case strings.Contains(message, "snapshot requires a literal store name"):
		return "Claim snapshots identify a store statically. Pass a literal store name such as claim.snapshot(\"facts\") after declaring that store in the Flow."
	case strings.Contains(message, "join RHS must be pure") || strings.Contains(message, "guard must be pure") || strings.Contains(message, "comprehension guard must be pure") || strings.Contains(message, "carry exit must be pure"):
		return "This position must be a repeatable pure expression. Bind external results in a Flow before this expression; a genuinely pure native call must be inside its own @{...} fn."
	case strings.Contains(message, "Claim declarations require Flow top level"):
		return "A Claim store belongs to a Flow instance. Declare it at the top level of a Flow body, then complete its joins before reading the store."
	case strings.Contains(message, "join requires a Flow graph statement"):
		return "join creates a dependency in the Flow graph, so it is valid only as a Flow graph statement. Move it out of fn, a value expression, or a local pure block."
	case strings.Contains(message, "operation forbidden in a node-local block"):
		return "This node-local block cannot perform graph-level work. Move the Flow call, Claim join, or external operation to a Flow graph statement."
	case strings.Contains(message, "parallel requires Concurrent ReadOnly") || strings.Contains(message, "perform is not provably Concurrent ReadOnly"):
		return "parallel requires every external operation in the body to be declared both ReadOnly and Concurrent. Run writes, Unknown operations, and unregistered requests sequentially; read in parallel, join, then write sequentially."
	case strings.Contains(message, "list index requires number or list"):
		return "List indexing accepts one number or a list of numeric indices. Use xs[i] for one item or xs[[i,j]] for a projection; indexes are zero-based."
	case strings.Contains(message, "object index requires string"):
		return "Object fields are indexed by string keys, for example object[\"name\"] or object.name. A numeric key is not converted automatically."
	case strings.Contains(message, "index requires number"):
		return "Sequence indexes must be numbers and safe integers. Use a numeric index such as xs[0], and check bounds before indexing."
	case strings.Contains(message, "value cannot be indexed"):
		return "Only lists, strings, and objects support indexing. Check the receiver type, then use a numeric index for a sequence or a string key for an object."
	case strings.Contains(message, "slice requires list or string"):
		return "Slicing is defined for lists and strings. Keep the receiver as a list/string or convert the value explicitly before using start:stop:step."
	case strings.Contains(message, "slice step cannot be zero") || strings.Contains(message, "range step is zero"):
		return "A step of zero cannot make progress. Use a positive or negative nonzero step, such as 1 or -1."
	case strings.Contains(message, "expected bool") || strings.Contains(message, "requires bool") || strings.Contains(message, "must be bool"):
		return "LIP has no truthiness conversion: conditions, !, and guards require bool. Compare explicitly, for example x != 0, text != \"\", or value == null."
	case strings.Contains(message, "expected number") || strings.Contains(message, "requires number") || strings.Contains(message, "requires numbers"):
		return "LIP keeps numbers and other types separate. When input is text, use parse_number(text) at the boundary and handle invalid numeric text; other types need explicit logic instead of implicit conversion."
	case strings.Contains(message, "expected string") || strings.Contains(message, "requires string"):
		return "This position requires a string. Use a string literal or str(value) for an explicit Data-to-text conversion; LIP does not coerce numbers automatically."
	case strings.Contains(message, "expected char") || strings.Contains(message, "requires char"):
		return "This position requires a char. Use a single-scalar literal such as 'A', to_chars/from_chars for strings, or from_codepoint/to_codepoint for explicit code point conversion."
	case strings.Contains(message, "expected list") || strings.Contains(message, "requires list") || strings.Contains(message, "comprehension requires list") || strings.Contains(message, "carry requires list"):
		return "This position requires a list. Use a list literal, range, map, or concat; an object, string, or scalar is not converted implicitly."
	case strings.Contains(message, "concat requires") || strings.Contains(message, "invalid concat arguments"):
		return "concat accepts two lists or two strings of the same kind. Use allcat for a nonempty list of same-kind sequence parts; list and string values cannot be mixed."
	case strings.Contains(message, "allcat requires") || strings.Contains(message, "invalid allcat arguments"):
		return "allcat accepts a nonempty list whose parts are all lists or all strings, and flattens exactly one level. Check each part's kind explicitly."
	case strings.Contains(message, "mapcat callback must return list") || strings.Contains(message, "invalid mapcat arguments"):
		return "mapcat accepts a list and a unary function whose result is a list. It maps in order and flattens the returned lists exactly one level."
	case strings.Contains(message, "expected object") || strings.Contains(message, "requires object"):
		return "This position requires an object. Use an object literal {key:value} or construct a new object with put; LIP does not coerce other containers."
	case strings.Contains(message, "comparison requires matching number/string") || strings.Contains(message, "comparison requires two numbers or two strings") || strings.Contains(message, "comparison requires two numbers, strings, or chars"):
		return "Ordered comparisons accept two numbers, two strings, or two chars of the same type. Convert input explicitly before comparing; use == or != for equality."
	case strings.Contains(message, "function/effect equality is undefined"):
		return "Functions and effect proposals have no equality operation. Compare their Data inputs or results, or perform the proposal and compare its returned Data."
	case strings.Contains(message, "arithmetic requires numbers"):
		return "Arithmetic operators require numbers on both sides. Parse text with parse_number(text), or keep text operations separate from numeric operations."
	case strings.Contains(message, "expected effect"):
		return "This position requires an unexecuted effect proposal. Construct one with effect alias.member(args); do not pass an ordinary value or an already executed native result to perform."
	case strings.Contains(message, "expected function") || strings.Contains(message, "got flow") || strings.Contains(message, "callee is not a language function") || strings.Contains(message, "callee is not callable"):
		return "Only fn values are ordinary callable values. Call a Flow by its declared name, and call imported Go/Python members through alias.member; a Flow cannot be stored in a variable or passed to map/fold."
	case strings.Contains(message, "duplicate binding"):
		return "Bindings are immutable within a scope, so assignment is not rebinding. Use a new name, create a nested scope to shadow deliberately, or express loop state with carry/next."
	case strings.Contains(message, "unbound name"):
		return "Check spelling, capitalization, and declaration order. Import Go/Python modules explicitly and refer to their exported members through the alias; LIP does not read a host global environment."
	case strings.Contains(message, "operation requires imported alias and member"):
		return "External operations need an explicit import alias and member, for example import go \"strings\" as strings followed by strings.ToUpper(...)."
	case strings.Contains(message, "unregistered operation"):
		return "The imported member is not registered by the Host. Check the exact case-sensitive package/member name and register the operation before compiling."
	case strings.Contains(message, "invalid import language"):
		return "LIP imports use one of the declared languages: lip, go, python, or host. Use import go \"path\" as alias, import python \"module\" as alias, or the matching project form."
	case strings.Contains(message, "full package path") || strings.Contains(message, "case-sensitive Go"):
		return "Go imports require the full, case-sensitive package path and an exported symbol. Add the dependency in the module that builds the generated program."
	case strings.Contains(message, "core functions reject named arguments"):
		return "Core functions use positional arguments only. Use map(xs, fn), fold(xs, seed, fn), or the documented positional order instead of name=value."
	case strings.Contains(message, "wrong core arity"):
		return "Check the core function signature in help(...). Core functions have fixed positional arity and do not accept omitted or extra arguments."
	case strings.Contains(message, "wrong function arity"):
		return "LIP checks function and Flow arity before executing arguments. Match the declared parameter count, or use named arguments only for declared fn/Flow parameters."
	case strings.Contains(message, "unknown/duplicate operation argument") || strings.Contains(message, "missing operation argument") || strings.Contains(message, "operation parameter"):
		return "Match the imported operation's declared parameter names and order. Each required parameter must be supplied once; optional parameters may be omitted."
	case strings.Contains(message, "reserved name") || strings.Contains(message, "reserved alias") || strings.Contains(message, "reserved declaration") || strings.Contains(message, "import alias conflicts with declaration"):
		return "This identifier is reserved by LIP. Choose a different binding or import alias; declarations cannot shadow keywords and core names in the same namespace."
	case strings.Contains(message, "duplicate object field"):
		return "An object literal cannot define the same field twice. Remove the duplicate key or combine the values into one field."
	case strings.Contains(message, "duplicate declaration") || strings.Contains(message, "duplicate import alias") || strings.Contains(message, "duplicate alias"):
		return "Each declaration and import alias must be unique in its scope. Rename one declaration or remove the duplicate."
	case strings.Contains(message, "only calls and perform may be statements"):
		return "An if, match, or ordinary value is an expression in a block. Bind it or put it in the Flow's tail, for example return if condition {value} else {other}; standalone statements are calls and perform."
	case strings.Contains(message, "return only at Flow tail"):
		return "return is allowed only for the final result of a Flow. Use return expression at the tail, and use bindings or calls before it."
	case strings.Contains(message, "declaration files cannot contain top-level statements"):
		return "A source file may declare imports, data, fn, and Flow definitions at the top level. Put executable statements inside a Flow, usually flow Main(...)."
	case strings.Contains(message, "value block requires a tail"):
		return "A value block must end with one expression. Remove the trailing separator or provide the final expression after the bindings."
	case strings.Contains(message, "expected carry exit"):
		return "A carry body must finish with its state using a next expression. Return or break outside the carry grammar cannot supply the next state."
	case strings.Contains(message, "@ must be followed by { on the same line"):
		return "The native purity marker belongs immediately before a fn body: write fn name(args)->type @{ native.call(...) }. It is not a general decorator or expression operator."
	case strings.Contains(message, "pipeline stages only accept named arguments"):
		return "A pipeline supplies its left value as the first positional argument. Use named arguments only for the remaining parameters, for example value |> convert(mode=\"fast\")."
	case strings.Contains(message, "positional argument after named argument"):
		return "Once a call uses a named argument, all later arguments must also be named. Reorder the call or name every argument after the first named one."
	case strings.Contains(message, "import requires an explicit alias") || strings.Contains(message, "LIP imports require an explicit alias"):
		return "Give every import an alias, for example import go \"strings\" as strings or import python \"math\" as math, then use alias.member."
	case strings.Contains(message, "export requires a declaration"):
		return "export applies to a declaration such as export fn or export flow. It cannot prefix a call, binding, or ordinary statement."
	case strings.Contains(message, "unknown type") || strings.Contains(message, "void cannot be nullable"):
		return "Use only LIP's declared types: any, bool, number, char, string, list, object, function, effect, or void where allowed. Nullable syntax is type?; void? is not valid."
	case strings.Contains(message, "leading zero in number"):
		return "LIP number literals do not allow leading zeroes. Write 0 for zero, 0.5 for a fraction, or 12 for an integer."
	case strings.Contains(message, "fraction requires digits"):
		return "A decimal point must be followed by digits. Write 1.0 instead of 1., or use an integer literal 1."
	case strings.Contains(message, "exponent requires digits"):
		return "An exponent must contain digits after e or E, for example 1e3 or 1.5e-2."
	case strings.Contains(message, "invalid number suffix") || strings.Contains(message, "number overflow") || strings.Contains(message, "non-finite number"):
		return "Use a complete finite number literal with LIP's decimal syntax. For nan or infinity, use the explicit nan, inf, or -inf literals."
	case strings.Contains(message, "unknown string escape") || strings.Contains(message, "unknown char escape") || strings.Contains(message, "Unicode escape requires braces"):
		return "Strings and chars use escapes such as \\\" \\\\ \\n, \\r, \\t, and \\u{...}. Check the backslash spelling and put braces around Unicode code points."
	case strings.Contains(message, "char literal must contain exactly one") || strings.Contains(message, "bare control character in char literal") || strings.Contains(message, "unterminated char literal"):
		return "A char literal uses single quotes and contains exactly one Unicode scalar, for example 'A' or '\\u{1F600}'. Use a string literal for multiple scalars."
	case strings.Contains(message, "unterminated string") || strings.Contains(message, "unterminated multiline string"):
		return "Close the string with its matching quote. Multiline strings use triple quotes: \"\"\"text\"\"\"."
	case strings.Contains(message, "expected statement separator"):
		return "Separate statements with a newline or semicolon. Keep a value expression in the Flow tail or after return."
	case code == "E-PURITY":
		return "This position must be pure and repeatable. Move external work into a Flow, construct an effect without executing it, or call a fn whose body explicitly promises pure native computation with @{...}."
	case code == "E-SCHEMA":
		return "This boundary accepts only LIP Data: null, bool, number, char, string, list, or object. Consume functions/effects inside LIP and convert native values through an explicit adapter."
	case code == "E-ARITY":
		return "Check the declared signature and argument order. LIP validates arity before evaluating arguments or executing external work."
	case code == "E-OPERATION":
		return "Check the imported alias, case-sensitive member name, Host contract, and operation registration before compiling the external call."
	case code == "E-NAME":
		return "Check the declaration, spelling, scope, and import alias. A name becomes available only after its declaration and successful cell."
	case code == "E-PARSE":
		return "LIP syntax is explicit: use fn for pure callable values, flow for execution graphs, effect to construct a request, and perform to execute it. Check the delimiter and declaration form near this position."
	case code == "E-LEX":
		return "LIP source is UTF-8 and uses explicit lexical forms. Check quotes, escapes, number spelling, and supported punctuation near this position."
	}
	return ""
}
func diagnosticPhase(err error, phase string) error {
	if e, ok := err.(*Diagnostic); ok {
		copy := cloneDiagnostic(e)
		copy.Phase = phase
		return copy
	}
	return err
}
func cloneDiagnostic(e *Diagnostic) *Diagnostic {
	if e == nil {
		return nil
	}
	copy := *e
	copy.Position = e.Position.Copy()
	copy.EvalPath = e.EvalPath.Copy()
	return &copy
}
func at(err error, t token) *Diagnostic {
	e, ok := err.(*Diagnostic)
	if !ok {
		e = diagnostic("E-VALUE", err.Error())
	}
	copy := *e
	if copy.Site < 0 {
		copy.Site, copy.Line, copy.Column = t.site, t.line, t.column
		copy.SourceID = t.source
	}
	return &copy
}

type Pair struct {
	Site  int
	Index string
}
type Position []Pair

func (p Position) Copy() Position { return append(Position(nil), p...) }
func comparePosition(a, b Position) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i].Site < b[i].Site {
			return -1
		}
		if a[i].Site > b[i].Site {
			return 1
		}
		if len(a[i].Index) < len(b[i].Index) {
			return -1
		}
		if len(a[i].Index) > len(b[i].Index) {
			return 1
		}
		if a[i].Index < b[i].Index {
			return -1
		}
		if a[i].Index > b[i].Index {
			return 1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}
func (p Position) Wire() Value {
	xs := make([]Value, len(p))
	for i, pair := range p {
		xs[i] = ListValue(textValue(strconv.Itoa(pair.Site)), textValue(pair.Index))
	}
	return ListValue(xs...)
}

type CallID struct {
	RunID       string
	Position    Position
	EvalPath    Position
	PerformSite int
}

func (id CallID) Wire() Value {
	return ListValue(textValue("call"), textValue(id.RunID), id.Position.Wire(), id.EvalPath.Wire(), textValue(strconv.Itoa(id.PerformSite)))
}
func joinWire(runID string, p Position) Value {
	return ListValue(textValue("join"), textValue(runID), p.Wire())
}
func digest(v Value) string {
	b, _ := Canonical(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func identityKey(v Value) string { b, _ := Canonical(v); return string(b) }
func compareErrors(a, b *Diagnostic) int {
	if x := comparePosition(a.Position, b.Position); x != 0 {
		return x
	}
	if x := comparePosition(a.EvalPath, b.EvalPath); x != 0 {
		return x
	}
	if a.Site < b.Site {
		return -1
	}
	if a.Site > b.Site {
		return 1
	}
	return 0
}

// parseNumericText implements the number-only Data boundary. It deliberately
// does not depend on a source lexer, so compiled programs need no parser.
func parseNumericText(s string) (Value, error) {
	if s == "nan" {
		return nanValue(), nil
	}
	if s == "inf" || s == "+inf" {
		return infValue(), nil
	}
	if s == "-inf" {
		return number(math.Inf(-1)), nil
	}
	invalid := func() (Value, error) { return Value{}, diagnostic("E-VALUE", "invalid numeric text") }
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	if i == len(s) {
		return invalid()
	}
	if s[i] == '0' {
		i++
	} else {
		if s[i] < '1' || s[i] > '9' {
			return invalid()
		}
		for i < len(s) && asciiDigit(s[i]) {
			i++
		}
	}
	if i < len(s) && s[i] == '.' {
		i++
		start := i
		for i < len(s) && asciiDigit(s[i]) {
			i++
		}
		if i == start {
			return invalid()
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		start := i
		for i < len(s) && asciiDigit(s[i]) {
			i++
		}
		if i == start {
			return invalid()
		}
	}
	if i != len(s) {
		return invalid()
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return invalid()
	}
	return NumberValue(n)
}
