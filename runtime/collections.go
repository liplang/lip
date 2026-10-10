package runtime

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"unicode/utf8"
)

const MaxRangeLength = 1_000_000

// MaxCallDepth is the safety bound for recursive local calls that the
// compiler cannot lower to an iterative loop. It is separate from the
// nesting limit used by recursive data walkers such as flatten.
const MaxCallDepth = 1024

// MaxNestingDepth protects recursive data walkers from cyclic or adversarial
// native values. It is not a limit on user function recursion.
const MaxNestingDepth = 256

type callDepthKey struct{}
type tailCallKey struct{}

// EnterFunction gives each call its own depth while preserving cancellation.
// Context state is scoped to a call, so sibling calls and Map workers do not
// consume one another's recursion budget.
func EnterFunction(ctx context.Context) (context.Context, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Host.Call uses this marker while jumping between tail-recursive local
	// functions. Consume it here so ordinary nested calls in the target still
	// receive their normal depth accounting.
	if tail, _ := ctx.Value(tailCallKey{}).(bool); tail {
		return context.WithValue(ctx, tailCallKey{}, false), nil
	}
	depth, _ := ctx.Value(callDepthKey{}).(int)
	if depth >= MaxCallDepth {
		return nil, fmt.Errorf("local function call depth exceeds %d", MaxCallDepth)
	}
	return context.WithValue(ctx, callDepthKey{}, depth+1), nil
}

func Length(value Value) (Value, error) {
	if text, ok := scalarString(value); ok {
		if _, err := stringInput(text); err != nil {
			return nil, err
		}
		return float64(utf8.RuneCountInString(text)), nil
	}
	if value != nil {
		v := reflect.ValueOf(value)
		if v.Kind() == reflect.Array || v.Kind() == reflect.Slice || v.Kind() == reflect.Map && v.Type().Key().Kind() == reflect.String {
			return float64(v.Len()), nil
		}
	}
	return nil, fmt.Errorf("len expects string, list or object, got %s", TypeName(value))
}

// IsEmpty reports whether a string, list or object has no elements. Null and
// other scalar values are errors so callers cannot accidentally confuse a
// missing value with an empty collection.
func IsEmpty(value Value) (bool, error) {
	if value == nil {
		return false, fmt.Errorf("isEmpty expects string, list or object, got null")
	}
	if text, ok := scalarString(value); ok {
		if _, err := stringInput(text); err != nil {
			return false, err
		}
		return utf8.RuneCountInString(text) == 0, nil
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Array, reflect.Slice:
		return v.Len() == 0, nil
	case reflect.Map:
		if v.Type().Key().Kind() == reflect.String {
			return v.Len() == 0, nil
		}
	}
	return false, fmt.Errorf("isEmpty expects string, list or object, got %s", TypeName(value))
}

func IsNotEmpty(value Value) (bool, error) {
	empty, err := IsEmpty(value)
	return !empty, err
}

// Range materializes a finite half-open interval; there is no iterator state.
func Range(ctx context.Context, args []Value) (Value, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(args) < 1 || len(args) > 3 {
		return nil, fmt.Errorf("range expects 1 to 3 arguments")
	}
	numbers := []float64{0, 0, 1}
	for i, arg := range args {
		n, err := Number(arg)
		if err != nil || math.Abs(n) > 9007199254740991 || n != math.Trunc(n) {
			return nil, fmt.Errorf("range argument %d must be a safe integer", i+1)
		}
		if len(args) == 1 {
			numbers[1] = n
		} else {
			numbers[i] = n
		}
	}
	start, end, step := numbers[0], numbers[1], numbers[2]
	if step == 0 {
		return nil, fmt.Errorf("range step cannot be zero")
	}
	count := math.Ceil((end - start) / step)
	if count <= 0 {
		return []Value{}, nil
	}
	if count > MaxRangeLength {
		return nil, fmt.Errorf("range exceeds %d elements", MaxRangeLength)
	}
	items := make([]Value, 0, int(count))
	for value := start; step > 0 && value < end || step < 0 && value > end; value += step {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(items) >= MaxRangeLength {
			return nil, fmt.Errorf("range exceeds %d elements", MaxRangeLength)
		}
		items = append(items, value)
	}
	return items, nil
}

// MapValues evaluates a composed comprehension in source order. Unlike a
// standalone Map node it never starts more workers, so nested comprehensions
// cannot multiply the enclosing scheduler's parallelism limit.
func MapValues(ctx context.Context, source Value, mapper func(context.Context, Value) Result) (Value, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if mapper == nil {
		return nil, fmt.Errorf("comprehension has no element evaluator")
	}
	items, err := listInput(source)
	if err != nil {
		return nil, fmt.Errorf("comprehension source: %w", err)
	}
	spec := &MapSpec{Eval: func(ctx context.Context, item Value, _ map[string]Value) Result {
		return mapper(ctx, item)
	}}
	return ResolveValue(ctx, mapSequential(ctx, spec, items, nil))
}

// Fold always visits source order, even when the enclosing graph is parallel.
func Fold(ctx context.Context, source, seed Value, reducer Op) (Value, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if reducer == nil {
		return nil, fmt.Errorf("fold needs a reducer")
	}
	items, err := listInput(source)
	if err != nil {
		return nil, fmt.Errorf("fold source: %w", err)
	}
	accumulator := seed
	for index, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		accumulator, err = ResolveValue(ctx, reducer(ctx, []Value{accumulator, item}))
		if err != nil {
			return nil, fmt.Errorf("fold element %d: %w", index, err)
		}
	}
	return accumulator, nil
}
