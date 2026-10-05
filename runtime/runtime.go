// Package runtime is the small execution kernel used by generated LIP code.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	stdruntime "runtime"
	"strings"
)

type Value = any

type Status int

const (
	Pending Status = iota
	Running
	Completed
	Error
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

// Host is the intentionally small Go interop boundary for Alpha 0.1.
type Host struct {
	ops  map[string]Op
	pure map[string]bool
}

func NewHost() Host { return Host{ops: make(map[string]Op), pure: make(map[string]bool)} }

func (h Host) Register(name string, op Op) {
	if h.ops == nil {
		panic("runtime.Host is not initialized")
	}
	h.ops[name] = op
	delete(h.pure, name)
}

// RegisterPure registers an operation that is safe to run concurrently with
// other independent operations. The operation must not mutate shared state or
// depend on call order.
func (h Host) RegisterPure(name string, op Op) {
	if h.ops == nil {
		panic("runtime.Host is not initialized")
	}
	h.ops[name] = op
	h.pure[name] = true
}

func (h Host) IsPure(name string) bool { return h.pure[name] }

func (h Host) Call(ctx context.Context, name string, args []Value) Result {
	op, ok := h.ops[name]
	if !ok {
		return Failed(fmt.Errorf("unknown host operation %q", name))
	}
	return op(ctx, args)
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
		return Ready(fmt.Sprint(args[0]))
	})
	h.Register("print", func(_ context.Context, args []Value) Result {
		for i, arg := range args {
			if i > 0 {
				_, _ = io.WriteString(os.Stdout, " ")
			}
			_, _ = io.WriteString(os.Stdout, fmt.Sprint(arg))
		}
		_, _ = io.WriteString(os.Stdout, "\n")
		return Ready(nil)
	})
	return h
}

func Number(v Value) (float64, error) {
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
	}
	return 0, fmt.Errorf("expected number, got %T (%v)", v, v)
}

func Bool(v Value) (bool, error) {
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("expected bool, got %T (%v)", v, v)
	}
	return b, nil
}

// CheckType validates an explicitly annotated Alpha input or function
// argument. "any" deliberately accepts every value, including nil.
func CheckType(v Value, typ string) error {
	switch typ {
	case "any", "":
		return nil
	case "string":
		if _, ok := v.(string); ok {
			return nil
		}
	case "number":
		if _, err := Number(v); err == nil {
			return nil
		}
	case "bool":
		if _, ok := v.(bool); ok {
			return nil
		}
	default:
		return fmt.Errorf("unknown type %q", typ)
	}
	return fmt.Errorf("expected %s, got %T (%v)", typ, v, v)
}

func Field(object Value, name string) (Value, error) {
	if object == nil {
		return nil, fmt.Errorf("cannot read field %q from nil", name)
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
			field = v.FieldByName(strings.ToUpper(name[:1]) + name[1:])
			if field.IsValid() && field.CanInterface() {
				return field.Interface(), nil
			}
		}
	}
	return nil, fmt.Errorf("field %q is not available on %T", name, object)
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
	case reflect.Slice, reflect.Array, reflect.String:
		n, err := integer(index)
		if err != nil {
			return nil, err
		}
		if n < 0 || n >= v.Len() {
			return nil, fmt.Errorf("index %d out of range", n)
		}
		return v.Index(n).Interface(), nil
	case reflect.Map:
		key := reflect.ValueOf(index)
		if !key.IsValid() || !key.Type().AssignableTo(v.Type().Key()) {
			if key.IsValid() && key.Type().ConvertibleTo(v.Type().Key()) {
				key = key.Convert(v.Type().Key())
			} else {
				return nil, fmt.Errorf("index type %T is not %s", index, v.Type().Key())
			}
		}
		value := v.MapIndex(key)
		if !value.IsValid() {
			return nil, fmt.Errorf("missing map key %v", index)
		}
		return value.Interface(), nil
	default:
		return nil, fmt.Errorf("cannot index %T", object)
	}
}

func Binary(op string, left, right Value) (Value, error) {
	switch op {
	case "+":
		if ls, ok := left.(string); ok {
			rs, ok := right.(string)
			if !ok {
				return nil, fmt.Errorf("operator + expects two strings or two numbers, got string and %T", right)
			}
			return ls + rs, nil
		}
		if _, ok := right.(string); ok {
			return nil, fmt.Errorf("operator + expects two strings or two numbers, got %T and string", left)
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
	case "-", "/":
		l, err := Number(left)
		if err != nil {
			return nil, err
		}
		r, err := Number(right)
		if err != nil {
			return nil, err
		}
		if op == "/" && r == 0 {
			return nil, errors.New("division by zero")
		}
		switch op {
		case "-":
			return l - r, nil
		default:
			return l / r, nil
		}
	case "*":
		if ls, ok := left.(string); ok {
			n, err := integer(right)
			if err != nil {
				return nil, fmt.Errorf("operator * on a string expects a non-negative integer: %w", err)
			}
			if n < 0 {
				return nil, errors.New("operator * on a string expects a non-negative integer")
			}
			return repeatString(ls, n)
		}
		if rs, ok := right.(string); ok {
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
	if count > 0 && len(value) > int(^uint(0)>>1)/count {
		return "", errors.New("operator * string result is too large")
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
	return reflect.DeepEqual(left, right)
}

type NodeSpec struct {
	Name   string
	Op     string
	Pure   bool
	Deps   []string
	Gates  []string
	Eval   func(context.Context, map[string]Value) Result
	Gate   func(map[string]Value) (bool, error)
	Output bool
}

type TraceEvent struct {
	Node   string
	Status Status
	Reason string
}

type Graph struct {
	nodes       []NodeSpec
	trace       []TraceEvent
	output      Value
	hasOut      bool
	outputIndex int
}

func NewGraph() *Graph               { return &Graph{} }
func (g *Graph) Add(node NodeSpec)   { g.nodes = append(g.nodes, node) }
func (g *Graph) Trace() []TraceEvent { return append([]TraceEvent(nil), g.trace...) }

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
	g.trace = append(g.trace, TraceEvent{Node: node, Status: status, Reason: reason})
}

func (g *Graph) Run(ctx context.Context, inputs map[string]Value) (Value, []TraceEvent, error) {
	g.trace = nil
	g.output = nil
	g.hasOut = false
	g.outputIndex = -1
	values := make(map[string]Value, len(inputs))
	for k, v := range inputs {
		values[k] = v
	}
	status := make([]Status, len(g.nodes))
	for i := range status {
		status[i] = Pending
	}
	for completed := 0; completed < len(g.nodes); {
		progress := false
		for i, node := range g.nodes {
			if status[i] != Pending {
				continue
			}
			ready, blocked, reason := dependencies(node, g.nodes, status, values)
			if blocked {
				status[i] = Skipped
				g.record(node.Name, Skipped, reason)
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
					status[i] = Error
					g.record(node.Name, Error, err.Error())
					return nil, g.Trace(), fmt.Errorf("node %s: %w", node.Name, err)
				}
				if !ok {
					status[i] = Skipped
					g.record(node.Name, Skipped, "gate is false")
					completed++
					progress = true
					continue
				}
			}
			status[i] = Running
			g.record(node.Name, Running, "")
			result, waitErr := awaitResult(ctx, node.Eval(ctx, values))
			if waitErr != nil {
				status[i] = Error
				g.record(node.Name, Error, waitErr.Error())
				return nil, g.Trace(), waitErr
			}
			if result.Err != nil {
				status[i] = Error
				g.record(node.Name, Error, result.Err.Error())
				return nil, g.Trace(), fmt.Errorf("node %s: %w", node.Name, result.Err)
			}
			values[node.Name] = result.Value
			status[i] = Completed
			g.record(node.Name, Completed, "")
			if node.Output && i >= g.outputIndex {
				g.output = result.Value
				g.hasOut = true
				g.outputIndex = i
			}
			completed++
			progress = true
		}
		if !progress {
			return nil, g.Trace(), errors.New("dependency graph did not make progress (cycle or unresolved dependency)")
		}
	}
	if !g.hasOut {
		return nil, g.Trace(), nil
	}
	return g.output, g.Trace(), nil
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
	if limit < 1 {
		return nil, nil, fmt.Errorf("parallelism limit must be at least 1")
	}
	g.trace = nil
	g.output = nil
	g.hasOut = false
	g.outputIndex = -1
	values := make(map[string]Value, len(inputs))
	for k, v := range inputs {
		values[k] = v
	}
	status := make([]Status, len(g.nodes))
	for i := range status {
		status[i] = Pending
	}
	type resultEvent struct {
		index  int
		result Result
		err    error
	}
	results := make(chan resultEvent, len(g.nodes))
	active := make(map[int]bool)
	completed := 0
	for completed < len(g.nodes) {
		if err := ctx.Err(); err != nil {
			return nil, g.Trace(), err
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
					status[i] = Error
					g.record(node.Name, Error, err.Error())
					return nil, g.Trace(), fmt.Errorf("node %s: %w", node.Name, err)
				}
				if !ok {
					status[i] = Skipped
					g.record(node.Name, Skipped, "gate is false")
					completed++
					progress = true
					continue
				}
			}
			ready = append(ready, i)
		}

		for _, i := range ready {
			if len(active) >= limit {
				break
			}
			node := g.nodes[i]
			if !node.Pure && !host.IsPure(node.Op) {
				continue
			}
			status[i] = Running
			active[i] = true
			g.record(node.Name, Running, "")
			snapshot := cloneValues(values)
			go func(index int, spec NodeSpec) {
				result, err := awaitResult(ctx, spec.Eval(ctx, snapshot))
				results <- resultEvent{index: index, result: result, err: err}
			}(i, node)
			progress = true
		}

		if len(active) > 0 {
			event := <-results
			delete(active, event.index)
			if event.err != nil {
				status[event.index] = Error
				g.record(g.nodes[event.index].Name, Error, event.err.Error())
				return nil, g.Trace(), event.err
			}
			if event.result.Err != nil {
				status[event.index] = Error
				g.record(g.nodes[event.index].Name, Error, event.result.Err.Error())
				return nil, g.Trace(), fmt.Errorf("node %s: %w", g.nodes[event.index].Name, event.result.Err)
			}
			values[g.nodes[event.index].Name] = event.result.Value
			status[event.index] = Completed
			g.record(g.nodes[event.index].Name, Completed, "")
			if g.nodes[event.index].Output && event.index >= g.outputIndex {
				g.output = event.result.Value
				g.hasOut = true
				g.outputIndex = event.index
			}
			completed++
			continue
		}

		// An effectful node is executed only after all active pure work has
		// drained, preserving the sequential semantics of side effects.
		for _, i := range ready {
			node := g.nodes[i]
			if node.Pure || host.IsPure(node.Op) {
				continue
			}
			status[i] = Running
			g.record(node.Name, Running, "")
			result, waitErr := awaitResult(ctx, node.Eval(ctx, values))
			if waitErr != nil {
				status[i] = Error
				g.record(node.Name, Error, waitErr.Error())
				return nil, g.Trace(), waitErr
			}
			if result.Err != nil {
				status[i] = Error
				g.record(node.Name, Error, result.Err.Error())
				return nil, g.Trace(), fmt.Errorf("node %s: %w", node.Name, result.Err)
			}
			values[node.Name] = result.Value
			status[i] = Completed
			g.record(node.Name, Completed, "")
			if node.Output && i >= g.outputIndex {
				g.output = result.Value
				g.hasOut = true
				g.outputIndex = i
			}
			completed++
			progress = true
			break
		}
		if !progress && len(active) == 0 {
			return nil, g.Trace(), errors.New("dependency graph did not make progress (cycle or unresolved dependency)")
		}
	}
	if !g.hasOut {
		return nil, g.Trace(), nil
	}
	return g.output, g.Trace(), nil
}

func awaitResult(ctx context.Context, result Result) (Result, error) {
	for result.Future != nil {
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case result = <-result.Future:
		}
	}
	return result, nil
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
	return true, false, ""
}
