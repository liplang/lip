package runtime

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

var randomState = struct {
	sync.Mutex
	source *rand.Rand
}{source: rand.New(rand.NewSource(time.Now().UnixNano()))}

// RandomCall implements the nondeterministic core random operations. They are
// deliberately kept out of the pure standard-library catalogs: a graph must
// not cache a random value across an Instance tick or assume it is safe to
// reorder calls.
func RandomCall(ctx context.Context, name string, args []Value) (Value, error) {
	if ctx == nil {
		return nil, errNilContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch name {
	case "random":
		if len(args) != 0 {
			return nil, fmt.Errorf("random expects 0 arguments, got %d", len(args))
		}
		randomState.Lock()
		value := randomState.source.Float64()
		randomState.Unlock()
		return value, nil
	case "random_list":
		if len(args) != 1 {
			return nil, fmt.Errorf("%s expects 1 argument, got %d", name, len(args))
		}
		n, err := integer(args[0])
		if err != nil {
			return nil, fmt.Errorf("%s count: %w", name, err)
		}
		if n < 0 {
			return nil, fmt.Errorf("%s count must be nonnegative", name)
		}
		if err := listSize(n); err != nil {
			return nil, err
		}
		out := make([]Value, n)
		randomState.Lock()
		for i := range out {
			if err := ctx.Err(); err != nil {
				randomState.Unlock()
				return nil, err
			}
			out[i] = randomState.source.Float64()
		}
		randomState.Unlock()
		return out, nil
	case "random_int":
		if len(args) != 1 && len(args) != 2 {
			return nil, fmt.Errorf("random_int expects 1 or 2 arguments, got %d", len(args))
		}
		start, end := int64(0), int64(0)
		if len(args) == 1 {
			value, err := integer(args[0])
			if err != nil {
				return nil, fmt.Errorf("random_int end: %w", err)
			}
			end = int64(value)
		} else {
			left, err := integer(args[0])
			if err != nil {
				return nil, fmt.Errorf("random_int start: %w", err)
			}
			right, err := integer(args[1])
			if err != nil {
				return nil, fmt.Errorf("random_int end: %w", err)
			}
			start, end = int64(left), int64(right)
		}
		if end <= start {
			return nil, fmt.Errorf("random_int requires start < end")
		}
		randomState.Lock()
		value := start + randomState.source.Int63n(end-start)
		randomState.Unlock()
		return float64(value), nil
	case "random_choice":
		if len(args) != 1 {
			return nil, fmt.Errorf("random_choice expects 1 argument, got %d", len(args))
		}
		items, err := listInput(args[0])
		if err != nil {
			return nil, fmt.Errorf("random_choice source: %w", err)
		}
		if len(items) == 0 {
			return nil, fmt.Errorf("random_choice needs a nonempty list")
		}
		randomState.Lock()
		index := randomState.source.Intn(len(items))
		randomState.Unlock()
		return items[index], nil
	case "random_shuffle":
		if len(args) != 1 {
			return nil, fmt.Errorf("random_shuffle expects 1 argument, got %d", len(args))
		}
		items, err := listInput(args[0])
		if err != nil {
			return nil, fmt.Errorf("random_shuffle source: %w", err)
		}
		out := append([]Value{}, items...)
		randomState.Lock()
		for i := len(out) - 1; i > 0; i-- {
			if err := ctx.Err(); err != nil {
				randomState.Unlock()
				return nil, err
			}
			j := randomState.source.Intn(i + 1)
			out[i], out[j] = out[j], out[i]
		}
		randomState.Unlock()
		return out, nil
	default:
		return nil, fmt.Errorf("unknown random operation %q", name)
	}
}
