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
)

func (k Kind) String() string {
	return [...]string{"null", "bool", "number", "string", "list", "object", "function", "effect"}[k]
}

// Value is immutable. Constructors and accessors never expose mutable storage.
type Value struct {
	k Kind
	b bool
	n float64
	s string
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
func textValue(s string) Value    { return Value{k: String, s: s} }
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
	if !v.IsData() {
		return "", diagnostic("E-TYPE", "str requires Data")
	}
	if v.k == String {
		return v.s, nil
	}
	var b strings.Builder
	displayJSON(&b, v)
	return b.String(), nil
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

func finiteNumber(n float64) (Value, error) {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return Value{}, diagnostic("E-NUMBER", "finite arithmetic produced a non-finite result")
	}
	return NumberValue(n)
}
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
	if (op == "/" || op == "//" || op == "%") && b.n == 0 {
		return Value{}, diagnostic("E-NUMBER", "division by zero")
	}
	if math.IsInf(a.n, 0) || math.IsInf(b.n, 0) {
		return infiniteArithmetic(op, a.n, b.n)
	}
	switch op {
	case "+":
		return finiteNumber(a.n + b.n)
	case "-":
		return finiteNumber(a.n - b.n)
	case "*":
		return finiteNumber(a.n * b.n)
	case "/":
		return finiteNumber(a.n / b.n)
	case "**":
		if a.n == 0 && b.n < 0 || a.n < 0 && math.Trunc(b.n) != b.n {
			return Value{}, diagnostic("E-NUMBER", "power has no finite real value")
		}
		return finiteNumber(corePower(a.n, b.n))
	case "*/":
		if a.n <= 0 || b.n <= 0 || b.n == 1 {
			return Value{}, diagnostic("E-NUMBER", "logarithm requires value > 0, base > 0 and base != 1")
		}
		if a.n == 1 {
			return number(0), nil
		}
		if a.n == b.n {
			return number(1), nil
		}
		return finiteNumber(numericDiv(coreLog(a.n), coreLog(b.n)))
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
			return finiteNumber(n)
		}
		r := new(big.Rat).Sub(x, new(big.Rat).Mul(new(big.Rat).SetInt(whole), y))
		n, _ := r.Float64()
		if n == b.n {
			n = math.Nextafter(b.n, 0)
		}
		return finiteNumber(n)
	}
	return Value{}, diagnostic("E-TYPE", "unknown arithmetic operator")
}

// Infinite operands are handled before the fixed finite algorithms and exact
// rational division. Finite overflow still goes through finiteNumber.
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
		if a <= 0 || b <= 0 || b == 1 {
			return Value{}, diagnostic("E-NUMBER", "logarithm requires value > 0, base > 0 and base != 1")
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
	if e.Line > 0 {
		if e.SourceID != "" {
			return fmt.Sprintf("%s at %s:%d:%d: %s", e.Code, e.SourceID, e.Line, e.Column, message)
		}
		return fmt.Sprintf("%s at %d:%d: %s", e.Code, e.Line, e.Column, message)
	}
	return e.Code + ": " + message
}
func diagnostic(code, message string) *Diagnostic {
	return &Diagnostic{Phase: "host", Code: code, Message: message, Site: -1}
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
