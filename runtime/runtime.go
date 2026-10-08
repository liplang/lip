// Package runtime is the small execution kernel used by generated LIP code.
package runtime

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	stdruntime "runtime"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"lipalpha/internal/listops"
	"lipalpha/internal/stringops"
)

//go:embed *.go python_worker.py
var buildSources embed.FS

// WriteBuildModule materializes the installed runtime for an isolated lipc build.
func WriteBuildModule(directory string) error {
	write := func(name string, data []byte) error {
		path := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o644)
	}
	files, err := buildSources.ReadDir(".")
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.IsDir() || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		data, err := buildSources.ReadFile(file.Name())
		if err != nil {
			return err
		}
		if err := write("runtime/"+file.Name(), data); err != nil {
			return err
		}
	}
	for name, source := range map[string]string{
		"go.mod":                        "module lipalpha\n\ngo 1.27\n",
		"internal/listops/catalog.go":   listops.Source,
		"internal/stringops/catalog.go": stringops.Source,
	} {
		if err := write(name, []byte(source)); err != nil {
			return err
		}
	}
	return nil
}

type Value = any

type Status int

const (
	Pending Status = iota
	Running
	Completed
	Error
	Cancelled
	Skipped
)

func (s Status) String() string {
	switch s {
	case Pending:
		return "Pending"
	case Running:
		return "Running"
	case Completed:
		return "Completed"
	case Error:
		return "Error"
	case Cancelled:
		return "Cancelled"
	case Skipped:
		return "Skipped"
	default:
		return "Unknown"
	}
}

// Result is returned by a host operation. Future is optional; when present,
// the scheduler waits for one result from that channel before continuing.
type Result struct {
	Value  Value
	Err    error
	Future <-chan Result
}

func Ready(v Value) Result          { return Result{Value: v} }
func Failed(err error) Result       { return Result{Err: err} }
func Await(ch <-chan Result) Result { return Result{Future: ch} }

type Op func(context.Context, []Value) Result

var errNilContext = errors.New("nil context")

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return errNilContext
	}
	return nil
}

type Effect int

const (
	EffectUnknown Effect = iota
	EffectPure
	EffectReadOnly
	EffectExternalWrite
)

func (e Effect) String() string {
	switch e {
	case EffectPure:
		return "Pure"
	case EffectReadOnly:
		return "ReadOnly"
	case EffectExternalWrite:
		return "ExternalWrite"
	default:
		return "Unknown"
	}
}

// Host is the Go interop boundary. Register operations before executing a Flow.
type Host struct {
	ops          map[string]Op
	effects      map[string]Effect
	python       *PythonWorker
	pythonEffect Effect
}

func NewHost() Host {
	return Host{ops: make(map[string]Op), effects: make(map[string]Effect)}
}

// Clone gives a generated Flow its own local function namespace. Registrations
// in one Flow must not overwrite the caller's operations or another Flow.
func (h Host) Clone() Host {
	copy := NewHost()
	for name, op := range h.ops {
		copy.ops[name] = op
	}
	for name, effect := range h.effects {
		copy.effects[name] = effect
	}
	copy.python, copy.pythonEffect = h.python, h.pythonEffect
	return copy
}

func (h Host) HasOperation(name string) bool { _, ok := h.ops[name]; return ok }

func (h Host) RequireOperation(name string) error {
	if strings.HasSuffix(name, ".*") {
		prefix := strings.TrimSuffix(name, "*")
		for registered := range h.ops {
			if strings.HasPrefix(registered, prefix) {
				return nil
			}
		}
	} else if h.HasOperation(name) {
		return nil
	}
	return fmt.Errorf("required host operation %q is not registered", name)
}

func (h Host) Register(name string, op Op) {
	if h.ops == nil {
		panic("runtime.Host is not initialized")
	}
	h.ops[name] = op
	h.effects[name] = EffectExternalWrite
}

// RegisterPure registers an operation that is safe to run concurrently with
// other independent operations. The operation must not mutate shared state or
// depend on call order.
func (h Host) RegisterPure(name string, op Op) {
	if h.ops == nil {
		panic("runtime.Host is not initialized")
	}
	h.ops[name] = op
	h.effects[name] = EffectPure
}

func (h Host) IsPure(name string) bool { return h.EffectOf(name) == EffectPure }

// RegisterReadOnly is a concurrent-safe Host registration with an explicit
// read-only effect classification.
func (h Host) RegisterReadOnly(name string, op Op) {
	if h.ops == nil {
		panic("runtime.Host is not initialized")
	}
	h.ops[name] = op
	h.effects[name] = EffectReadOnly
}

func (h Host) EffectOf(name string) Effect {
	if effect, ok := h.effects[name]; ok {
		return effect
	}
	if h.python != nil {
		return h.pythonEffect
	}
	return EffectUnknown
}

// EffectsOf classifies a whole expression, including calls inside arguments,
// sources and conditional branches. Unknown operations are write barriers.
func (h Host) EffectsOf(names []string) Effect {
	effect := EffectPure
	for _, name := range names {
		switch h.EffectOf(name) {
		case EffectPure:
		case EffectReadOnly:
			effect = EffectReadOnly
		default:
			return EffectExternalWrite
		}
	}
	return effect
}

func (h Host) Call(ctx context.Context, name string, args []Value) Result {
	if err := checkContext(ctx); err != nil {
		return Failed(err)
	}
	if err := ctx.Err(); err != nil {
		return Failed(err)
	}
	op, ok := h.ops[name]
	if !ok {
		if h.python != nil {
			return h.python.Call(ctx, name, args)
		}
		return Failed(fmt.Errorf("unknown host operation %q", name))
	}
	return op(ctx, args)
}

// NewPythonHost creates the standard Host with a generic Python fallback. Any
// unknown LIP operation is sent to Python as a dotted callable name, so a Flow
// can write numpy.linalg.solve(...) or torch.nn.functional.relu(...) without
// registering every library function in Go.
func NewPythonHost(worker *PythonWorker) Host {
	return DefaultHost().WithPython(worker)
}

// NewPythonPureHost creates a generic Python Host whose fallback is marked
// Pure. Use it only for a worker contract containing deterministic, isolated
// functions; NewPythonHost is the conservative default.
func NewPythonPureHost(worker *PythonWorker) Host {
	return DefaultHost().WithPythonPure(worker)
}

// WithPython attaches a generic, conservative ExternalWrite fallback to an
// existing Host. Use WithPythonPure only when every dynamically called Python
// operation is deterministic and free of external effects.
func (h Host) WithPython(worker *PythonWorker) Host {
	return h.WithPythonEffect(worker, EffectExternalWrite)
}

func (h Host) WithPythonPure(worker *PythonWorker) Host {
	return h.WithPythonEffect(worker, EffectPure)
}

func (h Host) WithPythonEffect(worker *PythonWorker, effect Effect) Host {
	if worker == nil {
		panic("nil python worker")
	}
	if effect == EffectUnknown {
		effect = EffectExternalWrite
	}
	h.python = worker
	h.pythonEffect = effect
	return h
}

func DefaultHost() Host {
	h := NewHost()
	// Arithmetic and comparisons are language operators. The only conversion
	// in the default host is explicit str(value), so mixed-type expressions do
	// not silently stringify or coerce values.
	h.RegisterPure("str", func(_ context.Context, args []Value) Result {
		if len(args) != 1 {
			return Failed(fmt.Errorf("str expects 1 argument, got %d", len(args)))
		}
		return Ready(StringValue(args[0]))
	})
	h.Register("print", func(_ context.Context, args []Value) Result {
		if err := PrintValues(os.Stdout, args); err != nil {
			return Failed(err)
		}
		return Ready(nil)
	})
	return h
}

func Number(v Value) (float64, error) {
	n, err := rawNumber(v)
	if err == nil && (math.IsNaN(n) || math.IsInf(n, 0)) {
		return 0, fmt.Errorf("expected a finite number, got %v", n)
	}
	return n, err
}

func rawNumber(v Value) (float64, error) {
	switch n := v.(type) {
	case int:
		return float64(n), nil
	case int8:
		return float64(n), nil
	case int16:
		return float64(n), nil
	case int32:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case uint:
		return float64(n), nil
	case uint8:
		return float64(n), nil
	case uint16:
		return float64(n), nil
	case uint32:
		return float64(n), nil
	case uint64:
		return float64(n), nil
	case float32:
		return float64(n), nil
	case float64:
		return n, nil
	case json.Number:
		return n.Float64()
	}
	rv := reflect.ValueOf(v)
	if rv.IsValid() {
		switch rv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return float64(rv.Int()), nil
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return float64(rv.Uint()), nil
		case reflect.Float32, reflect.Float64:
			return rv.Float(), nil
		}
	}
	return 0, fmt.Errorf("expected number, got %s (%s)", TypeName(v), StringValue(v))
}

func Bool(v Value) (bool, error) {
	b, ok := v.(bool)
	if !ok {
		if rv := reflect.ValueOf(v); rv.IsValid() && rv.Kind() == reflect.Bool {
			return rv.Bool(), nil
		}
		return false, fmt.Errorf("expected bool, got %s (%s)", TypeName(v), StringValue(v))
	}
	return b, nil
}

// CheckType validates an explicitly annotated Alpha input or function
// argument. "any" deliberately accepts every value, including nil.
func CheckType(v Value, typ string) error {
	base := strings.TrimSuffix(typ, "?")
	switch base {
	case "any", "string", "number", "bool", "list", "object":
	case "":
		if typ != "" {
			return fmt.Errorf("unknown type %q", typ)
		}
	default:
		return fmt.Errorf("unknown type %q", typ)
	}
	if strings.HasSuffix(typ, "?") {
		if v == nil {
			return nil
		}
		typ = strings.TrimSuffix(typ, "?")
	}
	switch typ {
	case "any", "":
		return nil
	case "string":
		if _, ok := scalarString(v); ok {
			_, err := stringInput(v)
			return err
		}
	case "number":
		if n, err := Number(v); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
			return nil
		}
	case "bool":
		if _, err := Bool(v); err == nil {
			return nil
		}
	case "list":
		if v != nil {
			kind := reflect.TypeOf(v).Kind()
			if kind == reflect.Slice || kind == reflect.Array {
				return nil
			}
		}
	case "object":
		if v != nil {
			t := reflect.TypeOf(v)
			if t.Kind() == reflect.Map && t.Key().Kind() == reflect.String {
				return nil
			}
		}
	default:
		return fmt.Errorf("unknown type %q", typ)
	}
	return fmt.Errorf("expected %s, got %s (%s)", typ, TypeName(v), StringValue(v))
}

func Field(object Value, name string) (Value, error) {
	if object == nil {
		return nil, fmt.Errorf("cannot read field %q from null", name)
	}
	v := reflect.ValueOf(object)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, fmt.Errorf("cannot read field %q from nil pointer", name)
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.Map && v.Type().Key().Kind() == reflect.String {
		value := v.MapIndex(reflect.ValueOf(name).Convert(v.Type().Key()))
		if !value.IsValid() {
			return nil, fmt.Errorf("missing field %q", name)
		}
		return value.Interface(), nil
	}
	if v.Kind() == reflect.Struct {
		field := v.FieldByName(name)
		if field.IsValid() && field.CanInterface() {
			return field.Interface(), nil
		}
		// Go convention: a LIP field uses lower-case names while exported Go
		// fields are usually title-cased.
		if len(name) > 0 {
			first, size := utf8.DecodeRuneInString(name)
			field = v.FieldByName(string(unicode.ToUpper(first)) + name[size:])
			if field.IsValid() && field.CanInterface() {
				return field.Interface(), nil
			}
		}
	}
	return nil, fmt.Errorf("field %q is not available on %s", name, TypeName(object))
}

func Index(object, index Value) (Value, error) {
	if object == nil {
		return nil, errors.New("cannot index nil")
	}
	v := reflect.ValueOf(object)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, errors.New("cannot index nil pointer")
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.String:
		if _, err := stringInput(v.String()); err != nil {
			return nil, err
		}
		n, err := integer(index)
		if err != nil {
			return nil, err
		}
		runes := []rune(v.String())
		if n < 0 || n >= len(runes) {
			return nil, fmt.Errorf("index %d out of range", n)
		}
		return string(runes[n]), nil
	case reflect.Slice, reflect.Array:
		n, err := integer(index)
		if err != nil {
			return nil, err
		}
		if n < 0 || n >= v.Len() {
			return nil, fmt.Errorf("index %d out of range", n)
		}
		return v.Index(n).Interface(), nil
	case reflect.Map:
		if v.Type().Key().Kind() == reflect.String {
			if _, ok := scalarString(index); !ok {
				return nil, fmt.Errorf("object index must be string, got %s", TypeName(index))
			}
		}
		key := reflect.ValueOf(index)
		if !key.IsValid() || !key.Type().AssignableTo(v.Type().Key()) {
			if key.IsValid() && key.Type().ConvertibleTo(v.Type().Key()) {
				key = key.Convert(v.Type().Key())
			} else {
				return nil, fmt.Errorf("index type %T is not %s", index, v.Type().Key())
			}
		}
		if !key.Comparable() {
			return nil, fmt.Errorf("map index must be comparable, got %s", TypeName(index))
		}
		value := v.MapIndex(key)
		if !value.IsValid() {
			return nil, fmt.Errorf("missing map key %v", index)
		}
		return value.Interface(), nil
	default:
		return nil, fmt.Errorf("cannot index %s", TypeName(object))
	}
}

func Binary(op string, left, right Value) (value Value, err error) {
	defer func() {
		if n, ok := value.(float64); err == nil && ok && (math.IsNaN(n) || math.IsInf(n, 0)) {
			value, err = nil, errors.New("numeric result is not finite")
		}
	}()
	switch op {
	case "+":
		if ls, ok := scalarString(left); ok {
			rs, ok := scalarString(right)
			if !ok {
				return nil, fmt.Errorf("operator + expects two strings or two numbers, got string and %s", TypeName(right))
			}
			if _, err := stringInput(ls); err != nil {
				return nil, err
			}
			if _, err := stringInput(rs); err != nil {
				return nil, err
			}
			if len(ls) > MaxStringBytes-len(rs) {
				return nil, fmt.Errorf("string result exceeds %d bytes", MaxStringBytes)
			}
			return ls + rs, nil
		}
		if _, ok := scalarString(right); ok {
			return nil, fmt.Errorf("operator + expects two strings or two numbers, got %s and string", TypeName(left))
		}
		l, err := Number(left)
		if err != nil {
			return nil, err
		}
		r, err := Number(right)
		if err != nil {
			return nil, err
		}
		return l + r, nil
	case "-", "/", "//", "%", "**", "*/":
		l, err := Number(left)
		if err != nil {
			return nil, err
		}
		r, err := Number(right)
		if err != nil {
			return nil, err
		}
		if (op == "/" || op == "//") && r == 0 {
			return nil, errors.New("division by zero")
		}
		if op == "%" && r == 0 {
			return nil, errors.New("modulo by zero")
		}
		switch op {
		case "-":
			return l - r, nil
		case "//":
			quotient := math.Floor(l / r)
			if quotient == 0 {
				return float64(0), nil
			}
			return quotient, nil
		case "**":
			return math.Pow(l, r), nil
		case "%":
			remainder := math.Mod(l, r)
			if remainder != 0 && (remainder < 0) != (r < 0) {
				remainder += r
			}
			if remainder == 0 {
				return float64(0), nil
			}
			return remainder, nil
		case "*/":
			if l <= 0 {
				return nil, errors.New("logarithm argument must be positive")
			}
			if r <= 0 || r == 1 {
				return nil, errors.New("logarithm base must be positive and different from 1")
			}
			if l == 1 {
				return float64(0), nil
			}
			if r == 2 {
				return math.Log2(l), nil
			}
			if r == 10 {
				return math.Log10(l), nil
			}
			return math.Log(l) / math.Log(r), nil
		default:
			return l / r, nil
		}
	case "*":
		if ls, ok := scalarString(left); ok {
			n, err := integer(right)
			if err != nil {
				return nil, fmt.Errorf("operator * on a string expects a non-negative integer: %w", err)
			}
			if n < 0 {
				return nil, errors.New("operator * on a string expects a non-negative integer")
			}
			return repeatString(ls, n)
		}
		if rs, ok := scalarString(right); ok {
			n, err := integer(left)
			if err != nil {
				return nil, fmt.Errorf("operator * on a string expects a non-negative integer: %w", err)
			}
			if n < 0 {
				return nil, errors.New("operator * on a string expects a non-negative integer")
			}
			return repeatString(rs, n)
		}
		l, err := Number(left)
		if err != nil {
			return nil, err
		}
		r, err := Number(right)
		if err != nil {
			return nil, err
		}
		return l * r, nil
	case "==":
		return equalValues(left, right), nil
	case "!=":
		return !equalValues(left, right), nil
	case ">", ">=", "<", "<=":
		l, err := Number(left)
		if err != nil {
			return nil, err
		}
		r, err := Number(right)
		if err != nil {
			return nil, err
		}
		switch op {
		case ">":
			return l > r, nil
		case ">=":
			return l >= r, nil
		case "<":
			return l < r, nil
		default:
			return l <= r, nil
		}
	case "&&", "||":
		l, err := Bool(left)
		if err != nil {
			return nil, err
		}
		r, err := Bool(right)
		if err != nil {
			return nil, err
		}
		if op == "&&" {
			return l && r, nil
		}
		return l || r, nil
	default:
		return nil, fmt.Errorf("unsupported binary operator %q", op)
	}
}

func integer(v Value) (int, error) {
	n, err := Number(v)
	if err != nil {
		return 0, err
	}
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	if math.IsNaN(n) || math.IsInf(n, 0) || n < float64(minInt) || n >= float64(maxInt) || n != math.Trunc(n) {
		return 0, fmt.Errorf("expected integer, got %v", n)
	}
	return int(n), nil
}

func repeatString(value string, count int) (string, error) {
	if _, err := stringInput(value); err != nil {
		return "", err
	}
	if len(value) > 0 && count > MaxStringBytes/len(value) {
		return "", fmt.Errorf("string result exceeds %d bytes", MaxStringBytes)
	}
	if value == "" || count == 0 {
		return "", nil
	}
	return strings.Repeat(value, count), nil
}

func equalValues(left, right Value) bool {
	// Alpha has one number type even though Go adapters may return any numeric
	// representation. Normalize only numeric pairs; strings and numbers never
	// compare equal through formatting or parsing.
	if l, err := Number(left); err == nil {
		if r, err := Number(right); err == nil {
			return l == r
		}
	}
	if l, ok := scalarString(left); ok {
		r, ok := scalarString(right)
		return ok && l == r
	}
	if l, err := Bool(left); err == nil {
		r, err := Bool(right)
		return err == nil && l == r
	}
	return reflect.DeepEqual(left, right)
}

type NodeSpec struct {
	Name   string
	Op     string
	Effect Effect
	Deps   []string
	Gates  []string
	// ValueType checks a completed value, including asynchronous Host results.
	ValueType string
	// After waits for named nodes to complete or be skipped, without consuming
	// their values. Failed or cancelled predecessors block this node.
	After    []string
	Eval     func(context.Context, map[string]Value) Result
	Gate     func(map[string]Value) (bool, error)
	Output   bool
	Map      *MapSpec
	State    bool
	Retry    *RetrySpec
	Feedback *FeedbackSpec
}

// MapSpec describes a one-shot dynamic map. The graph contains one node for
// the map; the runtime creates one execution instance per source element.
// Source names an existing graph value, or SourceEval computes a source once
// after dependencies and gates are ready. Eval receives the current item and
// a stable outer-values snapshot while evaluating the element expression.
type MapSpec struct {
	Source     string
	SourceEval func(context.Context, map[string]Value) Result
	Ops        []string
	Eval       func(context.Context, Value, map[string]Value) Result
}

// RetrySpec applies a bounded retry policy to one computation. Attempts
// includes the first execution and must be positive.
type RetrySpec struct {
	Attempts int
	Eval     func(context.Context, map[string]Value) Result
}

// FeedbackSpec is a bounded feedback loop. Init creates the first candidate,
// Verify decides whether it is acceptable, and Step creates the next
// candidate. Attempts counts verification rounds.
type FeedbackSpec struct {
	Attempts int
	Ops      []string
	Init     func(context.Context, map[string]Value) Result
	Step     func(context.Context, Value) Result
	Verify   func(context.Context, Value) Result
}

type TraceEvent struct {
	Tick   uint64
	Node   string
	Status Status
	Reason string
}

type Graph struct {
	mu           sync.Mutex
	nodes        []NodeSpec
	trace        []TraceEvent
	output       Value
	hasOut       bool
	outputIndex  int
	options      GraphOptions
	lifetimePlan *valueLifetimePlan
}

func NewGraph() *Graph { return &Graph{} }

// GraphOptions controls optional optimizations for graphs with explicit contracts.
type GraphOptions struct {
	// ReleaseIntermediates removes execution-table references after the last
	// declared consumer completes or is skipped. Every value read by Eval, Gate,
	// Map, Retry or Feedback must be declared in Deps/Gates (including Map.Source),
	// and node/input value names must be unique. Callbacks must treat the values map as
	// read-only and may use it only until their final asynchronous result completes.
	// Individual values may still escape to outputs or Host-owned storage.
	ReleaseIntermediates bool
}

// NewGraphWithOptions creates a graph with opt-in execution contracts. NewGraph
// preserves the full values-map behavior for existing hand-written Go graphs.
func NewGraphWithOptions(options GraphOptions) *Graph { return &Graph{options: options} }

func (g *Graph) Add(node NodeSpec) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nodes = append(g.nodes, node)
	g.lifetimePlan = nil
}

func (g *Graph) Trace() []TraceEvent {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.traceSnapshot()
}

func (g *Graph) traceSnapshot() []TraceEvent {
	return append([]TraceEvent(nil), g.trace...)
}

// Instance keeps graph values and state across logical ticks. A Graph remains
// reusable and one-shot Run/RunParallel retain their Alpha 0.1 semantics;
// callers opt into persistence by creating an Instance.
type Instance struct {
	mu             sync.Mutex
	graph          *Graph
	host           Host
	inputs         map[string]Value
	states         map[string]Value
	stateOverrides map[string]Value
	cache          []instanceNode
	tick           uint64
	trace          []TraceEvent
	versions       map[string]uint64
}

type instanceNode struct {
	valid         bool
	value         Value
	depValues     map[string]Value
	depPresent    map[string]bool
	afterVersions map[string]uint64
}

// NewInstance creates a persistent execution instance. Initial input
// validation is performed by generated Flow wrappers; the runtime also
// accepts a nil input map for flows without parameters.
func (g *Graph) NewInstance(host Host, inputs map[string]Value) *Instance {
	g.mu.Lock()
	defer g.mu.Unlock()
	initial := cloneValues(inputs)
	return &Instance{
		graph:          &Graph{nodes: append([]NodeSpec(nil), g.nodes...), options: g.options, lifetimePlan: g.lifetimePlan},
		host:           host,
		inputs:         initial,
		states:         make(map[string]Value),
		stateOverrides: make(map[string]Value),
		cache:          make([]instanceNode, len(g.nodes)),
		versions:       make(map[string]uint64),
	}
}

// SetState schedules a state value for the next Tick. State updates are
// external inputs to the dependency graph; they do not create a feedback edge
// by themselves.
func (i *Instance) SetState(name string, value Value) error {
	if i == nil || i.graph == nil {
		return errors.New("nil runtime instance")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, node := range i.graph.nodes {
		if node.Name == name && node.State {
			if err := CheckType(value, node.ValueType); err != nil {
				return fmt.Errorf("state %s: %w", name, err)
			}
			i.stateOverrides[name] = value
			return nil
		}
	}
	return fmt.Errorf("unknown state %q", name)
}

// State returns the most recently committed value of a state node.
func (i *Instance) State(name string) (Value, bool) {
	if i == nil {
		return nil, false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	value, ok := i.states[name]
	return value, ok
}

func (i *Instance) TickCount() uint64 {
	if i == nil {
		return 0
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.tick
}

// Trace returns the most recent tick trace.
func (i *Instance) Trace() []TraceEvent {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]TraceEvent(nil), i.trace...)
}

// Tick merges input changes, invalidates affected nodes and propagates the
// change through the graph. Unchanged nodes reuse their previous result.
func (i *Instance) Tick(ctx context.Context, inputs map[string]Value) (Value, []TraceEvent, error) {
	if i == nil || i.graph == nil {
		return nil, nil, errors.New("nil runtime instance")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := checkContext(ctx); err != nil {
		return nil, nil, err
	}
	for name, value := range inputs {
		i.inputs[name] = value
	}
	i.tick++
	tick := i.tick
	i.trace = nil
	defer func() { i.stateOverrides = make(map[string]Value) }()
	values := cloneValues(i.inputs)
	lifetime := i.graph.newValueLifetime(values)
	status := make([]Status, len(i.graph.nodes))
	// Tick is serialized and already commits completed nodes on failure. Updating
	// this slice directly also stops the old cache retaining invalidated values.
	nextCache := i.cache
	var output Value
	hasOutput, outputIndex := false, -1
	record := func(node string, state Status, reason string) {
		i.trace = append(i.trace, TraceEvent{Tick: tick, Node: node, Status: state, Reason: reason})
	}
	if err := ctx.Err(); err != nil {
		for index := range status {
			status[index] = Pending
		}
		abortNodes(i.graph.nodes, status, -1, err, record)
		return nil, append([]TraceEvent(nil), i.trace...), err
	}
	fail := func(index int, err error) (Value, []TraceEvent, error) {
		node := i.graph.nodes[index]
		status[index] = failureStatus(err)
		nextCache[index] = instanceNode{}
		record(node.Name, status[index], err.Error())
		abortNodes(i.graph.nodes, status, index, err, record)
		i.cache = nextCache
		return nil, append([]TraceEvent(nil), i.trace...), fmt.Errorf("node %s: %w", node.Name, err)
	}

	for completed := 0; completed < len(i.graph.nodes); {
		progress := false
		for index, node := range i.graph.nodes {
			if status[index] != Pending {
				continue
			}
			if err := ctx.Err(); err != nil {
				return fail(index, err)
			}
			override, hasOverride := i.stateOverrides[node.Name]
			state, hasState := i.states[node.Name]
			// An initializer is a dependency only until state is initialized.
			readyNode := node
			if node.State && (hasState || hasOverride) {
				readyNode.Deps = nil
			}
			ready, blocked, reason := dependencies(readyNode, i.graph.nodes, status, values)
			if !ready && !blocked {
				continue
			}
			progress = true
			completed++
			if !blocked && node.Gate != nil {
				ok, err := node.Gate(values)
				if err != nil {
					return fail(index, err)
				}
				if !ok {
					blocked, reason = true, "gate is false"
				}
			}
			if blocked {
				status[index] = Skipped
				nextCache[index] = instanceNode{}
				i.versions[node.Name] = tick
				record(node.Name, Skipped, reason)
				lifetime.finish(index, node, values)
				continue
			}

			cached := nextCache[index]
			reused := false
			var result Result
			if node.State && hasState && (!hasOverride || equalValues(state, override)) {
				result, reused = Ready(state), true
			} else if !node.State && nodeEffect(node, i.host) == EffectPure && reusableNode(cached, node, values, i.versions) {
				result, reused = Ready(cached.value), true
			} else {
				nextCache[index] = instanceNode{}
				status[index] = Running
				record(node.Name, Running, "")
				if node.State && hasOverride {
					result = Ready(override)
				} else {
					result = evaluateNode(ctx, node, values, 1)
				}
			}
			result, err := awaitNodeResult(ctx, node, result)
			if err != nil {
				return fail(index, err)
			}
			if result.Err != nil {
				return fail(index, result.Err)
			}
			values[node.Name] = result.Value
			status[index] = Completed
			if !reused {
				i.versions[node.Name] = tick
			}
			if !node.State && nodeEffect(node, i.host) == EffectPure {
				nextCache[index] = snapshotNode(result.Value, node, values, i.versions)
			} else {
				// State has its own store; effectful nodes cannot reuse a result.
				nextCache[index] = instanceNode{}
			}
			if node.State {
				i.states[node.Name] = result.Value
				delete(i.stateOverrides, node.Name)
			}
			if node.Output && index >= outputIndex {
				output, hasOutput, outputIndex = result.Value, true, index
			}
			reason = ""
			if reused {
				reason = "reused"
			}
			record(node.Name, Completed, reason)
			lifetime.finish(index, node, values)
		}
		if !progress {
			i.cache = nextCache
			return nil, append([]TraceEvent(nil), i.trace...), errors.New("dependency graph did not make progress (cycle or unresolved dependency)")
		}
	}
	i.cache = nextCache
	if !hasOutput {
		return nil, append([]TraceEvent(nil), i.trace...), nil
	}
	return output, append([]TraceEvent(nil), i.trace...), nil
}

func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func failureStatus(err error) Status {
	if isCancellation(err) {
		return Cancelled
	}
	return Error
}

func snapshotNode(value Value, node NodeSpec, values map[string]Value, versions map[string]uint64) instanceNode {
	cached := instanceNode{valid: true, value: value, depValues: make(map[string]Value), depPresent: make(map[string]bool), afterVersions: make(map[string]uint64)}
	for _, dep := range append(append([]string{}, node.Deps...), node.Gates...) {
		cached.depValues[dep], cached.depPresent[dep] = values[dep]
	}
	for _, after := range node.After {
		cached.afterVersions[after] = versions[after]
	}
	return cached
}

func reusableNode(cached instanceNode, node NodeSpec, values map[string]Value, versions map[string]uint64) bool {
	if !cached.valid {
		return false
	}
	for _, dep := range append(append([]string{}, node.Deps...), node.Gates...) {
		current, present := values[dep]
		if cached.depPresent[dep] != present || present && !equalValues(cached.depValues[dep], current) {
			return false
		}
	}
	for _, after := range node.After {
		if cached.afterVersions[after] != versions[after] {
			return false
		}
	}
	return true
}

// DefaultParallelism is deliberately bounded. It gives automatic scheduling a
// useful default without allowing a large machine to create an unbounded
// number of concurrent operations.
func DefaultParallelism() int {
	n := stdruntime.GOMAXPROCS(0)
	if n < 1 {
		return 1
	}
	if n > 8 {
		return 8
	}
	return n
}

func (g *Graph) record(node string, status Status, reason string) {
	g.recordAt(0, node, status, reason)
}

func (g *Graph) recordAt(tick uint64, node string, status Status, reason string) {
	g.trace = append(g.trace, TraceEvent{Tick: tick, Node: node, Status: status, Reason: reason})
}

func abortNodes(nodes []NodeSpec, status []Status, failed int, err error, record func(string, Status, string)) {
	for index, node := range nodes {
		if index == failed || status[index] != Pending && status[index] != Running {
			continue
		}
		reason := "flow aborted: " + err.Error()
		if status[index] == Running || failed < 0 && isCancellation(err) {
			status[index] = Cancelled
		} else {
			status[index] = Skipped
		}
		record(node.Name, status[index], reason)
	}
}

func (g *Graph) abortRun(status []Status, failed int, err error) (Value, []TraceEvent, error) {
	if failed >= 0 {
		status[failed] = failureStatus(err)
		g.record(g.nodes[failed].Name, status[failed], err.Error())
	}
	abortNodes(g.nodes, status, failed, err, g.record)
	if failed >= 0 {
		err = fmt.Errorf("node %s: %w", g.nodes[failed].Name, err)
	}
	return nil, g.traceSnapshot(), err
}

func (g *Graph) Run(ctx context.Context, inputs map[string]Value) (Value, []TraceEvent, error) {
	if err := checkContext(ctx); err != nil {
		return nil, nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.trace = nil
	g.output = nil
	defer func() { g.output = nil }()
	g.hasOut = false
	g.outputIndex = -1
	values := make(map[string]Value, len(inputs))
	for k, v := range inputs {
		values[k] = v
	}
	lifetime := g.newValueLifetime(values)
	status := make([]Status, len(g.nodes))
	for i := range status {
		status[i] = Pending
	}
	if err := ctx.Err(); err != nil {
		return g.abortRun(status, -1, err)
	}
	for completed := 0; completed < len(g.nodes); {
		if err := ctx.Err(); err != nil {
			return g.abortRun(status, -1, err)
		}
		progress := false
		for i, node := range g.nodes {
			if status[i] != Pending {
				continue
			}
			ready, blocked, reason := dependencies(node, g.nodes, status, values)
			if blocked {
				status[i] = Skipped
				g.record(node.Name, Skipped, reason)
				lifetime.finish(i, node, values)
				completed++
				progress = true
				continue
			}
			if !ready {
				continue
			}
			if node.Gate != nil {
				ok, err := node.Gate(values)
				if err != nil {
					return g.abortRun(status, i, err)
				}
				if !ok {
					status[i] = Skipped
					g.record(node.Name, Skipped, "gate is false")
					lifetime.finish(i, node, values)
					completed++
					progress = true
					continue
				}
			}
			status[i] = Running
			g.record(node.Name, Running, "")
			result, waitErr := awaitNodeResult(ctx, node, evaluateNode(ctx, node, values, 1))
			if waitErr != nil {
				return g.abortRun(status, i, waitErr)
			}
			if result.Err != nil {
				return g.abortRun(status, i, result.Err)
			}
			values[node.Name] = result.Value
			status[i] = Completed
			g.record(node.Name, Completed, "")
			if node.Output && i >= g.outputIndex {
				g.output = result.Value
				g.hasOut = true
				g.outputIndex = i
			}
			lifetime.finish(i, node, values)
			completed++
			progress = true
		}
		if !progress {
			return nil, g.traceSnapshot(), errors.New("dependency graph did not make progress (cycle or unresolved dependency)")
		}
	}
	if !g.hasOut {
		return nil, g.traceSnapshot(), nil
	}
	return g.output, g.traceSnapshot(), nil
}

// RunAuto is the default user-facing scheduler: independent pure nodes and
// RegisterPure host operations are run concurrently with a bounded limit.
func (g *Graph) RunAuto(ctx context.Context, host Host, inputs map[string]Value) (Value, []TraceEvent, error) {
	return g.RunParallel(ctx, host, inputs, DefaultParallelism())
}

// RunParallel executes independent pure nodes concurrently, bounded by limit.
// Nodes with side effects remain sequential and act as ordering barriers. A
// host operation is eligible when it was registered with Host.RegisterPure or
// when the generated graph marks the node Pure.
func (g *Graph) RunParallel(ctx context.Context, host Host, inputs map[string]Value, limit int) (Value, []TraceEvent, error) {
	if err := checkContext(ctx); err != nil {
		return nil, nil, err
	}
	if limit < 1 {
		return nil, nil, fmt.Errorf("parallelism limit must be at least 1")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	g.trace = nil
	g.output = nil
	defer func() { g.output = nil }()
	g.hasOut = false
	g.outputIndex = -1
	values := make(map[string]Value, len(inputs))
	for k, v := range inputs {
		values[k] = v
	}
	lifetime := g.newValueLifetime(values)
	status := make([]Status, len(g.nodes))
	for i := range status {
		status[i] = Pending
	}
	type resultEvent struct {
		index  int
		result Result
		err    error
	}
	results := make(chan resultEvent)
	active := make(map[int]bool)
	completed := 0
	for completed < len(g.nodes) {
		if err := ctx.Err(); err != nil {
			return g.abortRun(status, -1, err)
		}
		progress := false
		ready := make([]int, 0)
		for i, node := range g.nodes {
			if status[i] != Pending {
				continue
			}
			isReady, blocked, reason := dependencies(node, g.nodes, status, values)
			if blocked {
				status[i] = Skipped
				g.record(node.Name, Skipped, reason)
				lifetime.finish(i, node, values)
				completed++
				progress = true
				continue
			}
			if !isReady {
				continue
			}
			if node.Gate != nil {
				ok, err := node.Gate(values)
				if err != nil {
					return g.abortRun(status, i, err)
				}
				if !ok {
					status[i] = Skipped
					g.record(node.Name, Skipped, "gate is false")
					lifetime.finish(i, node, values)
					completed++
					progress = true
					continue
				}
			}
			ready = append(ready, i)
		}

		// Source-order write barriers prevent later reads from overtaking a
		// write, even when the write still waits for an earlier dependency.
		barrier := len(g.nodes)
		for index, node := range g.nodes {
			if (status[index] == Pending || status[index] == Running) && !nodeCanRunParallel(node, host) {
				barrier = index
				break
			}
		}
		for _, i := range ready {
			if len(active) >= limit {
				break
			}
			if i > barrier && !neededForNode(i, barrier, g.nodes) {
				continue
			}
			node := g.nodes[i]
			if !nodeCanRunParallel(node, host) {
				continue
			}
			status[i] = Running
			active[i] = true
			g.record(node.Name, Running, "")
			snapshot := nodeValues(node, values, g.options.ReleaseIntermediates)
			go func(index int, spec NodeSpec, snapshot map[string]Value) {
				result, err := awaitNodeResult(ctx, spec, evaluateNode(ctx, spec, snapshot, limit))
				select {
				case results <- resultEvent{index: index, result: result, err: err}:
				case <-ctx.Done():
				}
			}(i, node, snapshot)
			progress = true
		}

		if len(active) > 0 {
			var event resultEvent
			select {
			case event = <-results:
			case <-ctx.Done():
				return g.abortRun(status, -1, ctx.Err())
			}
			delete(active, event.index)
			if event.err != nil {
				return g.abortRun(status, event.index, event.err)
			}
			if event.result.Err != nil {
				return g.abortRun(status, event.index, event.result.Err)
			}
			values[g.nodes[event.index].Name] = event.result.Value
			status[event.index] = Completed
			g.record(g.nodes[event.index].Name, Completed, "")
			if g.nodes[event.index].Output && event.index >= g.outputIndex {
				g.output = event.result.Value
				g.hasOut = true
				g.outputIndex = event.index
			}
			lifetime.finish(event.index, g.nodes[event.index], values)
			completed++
			continue
		}

		// An effectful node is executed only after all active pure work has
		// drained, preserving the sequential semantics of side effects.
		for _, i := range ready {
			node := g.nodes[i]
			if nodeCanRunParallel(node, host) || i != barrier {
				continue
			}
			status[i] = Running
			g.record(node.Name, Running, "")
			result, waitErr := awaitNodeResult(ctx, node, evaluateNode(ctx, node, values, 1))
			if waitErr != nil {
				return g.abortRun(status, i, waitErr)
			}
			if result.Err != nil {
				return g.abortRun(status, i, result.Err)
			}
			values[node.Name] = result.Value
			status[i] = Completed
			g.record(node.Name, Completed, "")
			if node.Output && i >= g.outputIndex {
				g.output = result.Value
				g.hasOut = true
				g.outputIndex = i
			}
			lifetime.finish(i, node, values)
			completed++
			progress = true
			break
		}
		if !progress && len(active) == 0 {
			return nil, g.traceSnapshot(), errors.New("dependency graph did not make progress (cycle or unresolved dependency)")
		}
	}
	if !g.hasOut {
		return nil, g.traceSnapshot(), nil
	}
	return g.output, g.traceSnapshot(), nil
}

func evaluateNode(ctx context.Context, node NodeSpec, values map[string]Value, limit int) Result {
	if err := ctx.Err(); err != nil {
		return Failed(err)
	}
	if node.Map != nil {
		return evaluateMap(ctx, node.Map, values, limit)
	}
	if node.Retry != nil {
		return evaluateRetry(ctx, node.Retry, values)
	}
	if node.Feedback != nil {
		return evaluateFeedback(ctx, node.Feedback, values)
	}
	if node.Eval == nil {
		return Failed(fmt.Errorf("node %q has no evaluator", node.Name))
	}
	return node.Eval(ctx, values)
}

func evaluateRetry(ctx context.Context, spec *RetrySpec, values map[string]Value) Result {
	if spec == nil || spec.Eval == nil {
		return Failed(errors.New("retry node has no evaluator"))
	}
	if spec.Attempts < 1 {
		return Failed(errors.New("retry attempts must be at least 1"))
	}
	var last Result
	for attempt := 0; attempt < spec.Attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return Failed(err)
		}
		result, waitErr := awaitResult(ctx, spec.Eval(ctx, values))
		if waitErr != nil {
			if isCancellation(waitErr) {
				return Failed(waitErr)
			}
			last = Failed(waitErr)
			continue
		}
		if result.Err == nil {
			return result
		}
		if isCancellation(result.Err) {
			return result
		}
		last = result
	}
	return last
}

func evaluateFeedback(ctx context.Context, spec *FeedbackSpec, values map[string]Value) Result {
	if spec == nil || spec.Init == nil || spec.Step == nil || spec.Verify == nil {
		return Failed(errors.New("feedback node has incomplete evaluators"))
	}
	if spec.Attempts < 1 {
		return Failed(errors.New("feedback attempts must be at least 1"))
	}
	current, err := awaitResult(ctx, spec.Init(ctx, values))
	if err != nil {
		return Failed(err)
	}
	if current.Err != nil {
		return current
	}
	for attempt := 0; attempt < spec.Attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return Failed(err)
		}
		verified, err := awaitResult(ctx, spec.Verify(ctx, current.Value))
		if err != nil {
			return Failed(err)
		}
		if verified.Err != nil {
			return verified
		}
		ok, err := Bool(verified.Value)
		if err != nil {
			return Failed(fmt.Errorf("feedback verifier: %w", err))
		}
		if ok {
			return Ready(current.Value)
		}
		if attempt == spec.Attempts-1 {
			return Failed(fmt.Errorf("feedback did not converge after %d attempts", spec.Attempts))
		}
		next, err := awaitResult(ctx, spec.Step(ctx, current.Value))
		if err != nil {
			return Failed(err)
		}
		if next.Err != nil {
			return next
		}
		current = next
	}
	return Failed(errors.New("feedback did not converge"))
}

// nodeEffect uses explicit Effect or resolves it from Host operations.
// An unclassified operation is treated as a write barrier.
func nodeEffect(node NodeSpec, host Host) Effect {
	if node.Effect != EffectUnknown {
		return node.Effect
	}
	var ops []string
	if node.Map != nil {
		ops = node.Map.Ops
	} else if node.Feedback != nil {
		ops = node.Feedback.Ops
	} else if node.Op != "" {
		ops = []string{node.Op}
	}
	if len(ops) == 0 {
		return EffectUnknown
	}
	return host.EffectsOf(ops)
}

func nodeCanRunParallel(node NodeSpec, host Host) bool {
	effect := nodeEffect(node, host)
	return effect == EffectPure || effect == EffectReadOnly
}

// neededForNode lets a pure predecessor run even when it appears later in the
// source list than an effect barrier. This matters for graphs assembled by Go
// callers, which are not required to add nodes in topological order.
func neededForNode(candidate, target int, nodes []NodeSpec) bool {
	if candidate < 0 || target < 0 || candidate >= len(nodes) || target >= len(nodes) {
		return false
	}
	seen := make(map[int]bool)
	var visit func(int) bool
	visit = func(index int) bool {
		if seen[index] {
			return false
		}
		seen[index] = true
		for _, name := range append(append([]string{}, nodes[index].Deps...), nodes[index].Gates...) {
			for predecessor, node := range nodes {
				if node.Name != name {
					continue
				}
				if predecessor == candidate || visit(predecessor) {
					return true
				}
			}
		}
		for _, name := range nodes[index].After {
			for predecessor, node := range nodes {
				if node.Name != name {
					continue
				}
				if predecessor == candidate || visit(predecessor) {
					return true
				}
			}
		}
		return false
	}
	return visit(target)
}

func evaluateMap(ctx context.Context, spec *MapSpec, values map[string]Value, limit int) Result {
	if spec == nil || spec.Eval == nil {
		return Failed(errors.New("map node has no evaluator"))
	}
	var input Value
	if spec.SourceEval != nil {
		var err error
		input, err = ResolveValue(ctx, spec.SourceEval(ctx, values))
		if err != nil {
			return Failed(fmt.Errorf("map source: %w", err))
		}
	} else {
		var ok bool
		input, ok = values[spec.Source]
		if !ok {
			return Failed(fmt.Errorf("map source %q is unavailable", spec.Source))
		}
	}
	items, err := listInput(input)
	if err != nil {
		return Failed(fmt.Errorf("map source: %w", err))
	}
	if limit < 1 {
		limit = 1
	}
	if limit > len(items) && len(items) > 0 {
		limit = len(items)
	}
	if len(items) == 0 {
		return Ready([]Value{})
	}
	if limit == 1 {
		return mapSequential(ctx, spec, items, values)
	}
	return mapParallel(ctx, spec, items, values, limit)
}

func mapSequential(ctx context.Context, spec *MapSpec, items []Value, values map[string]Value) Result {
	out := make([]Value, len(items))
	for i, item := range items {
		if err := ctx.Err(); err != nil {
			return Failed(err)
		}
		result, err := awaitResult(ctx, spec.Eval(ctx, item, values))
		if err != nil {
			return Failed(fmt.Errorf("map element %d: %w", i, err))
		}
		if result.Err != nil {
			return Failed(fmt.Errorf("map element %d: %w", i, result.Err))
		}
		out[i] = result.Value
	}
	return Ready(out)
}

func mapParallel(ctx context.Context, spec *MapSpec, items []Value, values map[string]Value, limit int) Result {
	mapCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type itemResult struct {
		index int
		value Value
		err   error
	}
	jobs := make(chan int)
	results := make(chan itemResult, len(items))
	workers := limit
	if workers > len(items) {
		workers = len(items)
	}
	var wg sync.WaitGroup
	var stopOnce sync.Once
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if mapCtx.Err() != nil {
					continue
				}
				result, err := awaitResult(mapCtx, spec.Eval(mapCtx, items[index], values))
				if err != nil {
					stopOnce.Do(cancel)
					results <- itemResult{index: index, err: err}
					continue
				}
				if result.Err != nil {
					stopOnce.Do(cancel)
					results <- itemResult{index: index, err: result.Err}
					continue
				}
				results <- itemResult{index: index, value: result.Value}
			}
		}()
	}
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		defer close(jobs)
		for i := range items {
			select {
			case jobs <- i:
			case <-mapCtx.Done():
				return
			}
		}
	}()
	<-producerDone
	wg.Wait()
	close(results)
	out := make([]Value, len(items))
	var firstErr error
	firstErrIndex := int(^uint(0) >> 1)
	for result := range results {
		if result.err != nil {
			if firstErr == nil || result.index < firstErrIndex {
				firstErr = fmt.Errorf("map element %d: %w", result.index, result.err)
				firstErrIndex = result.index
			}
			continue
		}
		out[result.index] = result.value
	}
	if err := ctx.Err(); err != nil {
		return Failed(err)
	}
	if firstErr != nil {
		return Failed(firstErr)
	}
	return Ready(out)
}

func sequenceValues(value Value) ([]Value, error) {
	if value == nil {
		return nil, errors.New("expected list, got null")
	}
	if values, ok := value.([]Value); ok {
		return append([]Value(nil), values...), nil
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, fmt.Errorf("expected list, got %s", TypeName(value))
	}
	items := make([]Value, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		items[i] = rv.Index(i).Interface()
	}
	return items, nil
}

func awaitResult(ctx context.Context, result Result) (Result, error) {
	if err := checkContext(ctx); err != nil {
		return Result{}, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if result.Future == nil {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case next, ok := <-result.Future:
			if !ok {
				return Result{}, errors.New("host future closed without a result")
			}
			result = next
		}
	}
}

func cloneValues(values map[string]Value) map[string]Value {
	copy := make(map[string]Value, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func dependencies(node NodeSpec, nodes []NodeSpec, status []Status, values map[string]Value) (ready, blocked bool, reason string) {
	for _, dep := range append(append([]string{}, node.Deps...), node.Gates...) {
		if _, ok := values[dep]; ok {
			continue
		}
		for i, candidate := range nodes {
			if candidate.Name != dep {
				continue
			}
			switch status[i] {
			case Error:
				return false, true, "dependency " + dep + " failed"
			case Cancelled:
				return false, true, "dependency " + dep + " was cancelled"
			case Skipped:
				return false, true, "dependency " + dep + " was skipped"
			case Completed:
				if _, ok := values[dep]; !ok {
					return false, true, "dependency " + dep + " produced no value"
				}
			default:
				return false, false, ""
			}
		}
		return false, false, ""
	}
	for _, after := range node.After {
		found := false
		for index, predecessor := range nodes {
			if predecessor.Name != after {
				continue
			}
			found = true
			switch status[index] {
			case Completed, Skipped:
			case Error:
				return false, true, "ordering predecessor " + after + " failed"
			case Cancelled:
				return false, true, "ordering predecessor " + after + " was cancelled"
			default:
				return false, false, ""
			}
		}
		if !found {
			return false, false, ""
		}
	}
	return true, false, ""
}
