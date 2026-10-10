package runtime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestForEachControlsAndFutures(t *testing.T) {
	seen := []Value{}
	value, err := ForEach(context.Background(), []int{0, 1, 2, 3, 4}, func(ctx context.Context, item Value) Result {
		seen = append(seen, item)
		switch item {
		case 1:
			return Failed(fmt.Errorf("wrapped: %w", ContinueLoop().Err))
		case 3:
			return Failed(fmt.Errorf("wrapped: %w", BreakLoop().Err))
		}
		future := make(chan Result, 1)
		future <- Ready("discarded")
		return Result{Future: future}
	})
	if err != nil || value != nil || !reflect.DeepEqual(seen, []Value{0, 1, 2, 3}) {
		t.Fatalf("value=%v seen=%v err=%v", value, seen, err)
	}
}

func TestForEachLimitsAndStops(t *testing.T) {
	ctx := context.Background()
	calls := 0
	body := func(context.Context, Value) Result { calls++; return Ready(nil) }
	for _, source := range []Value{nil, 7, "abc", []Value(make([]Value, MaxListLength+1))} {
		if _, err := ForEach(ctx, source, body); err == nil {
			t.Fatalf("accepted source %T", source)
		}
	}
	if _, err := ForEach(ctx, []Value{}, body); err != nil || calls != 0 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	if _, err := ForEach(nil, []Value{}, body); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := ForEach(ctx, []Value{}, nil); err == nil {
		t.Fatal("nil body accepted")
	}
	_, err := ForEach(ctx, []int{0, 1, 2}, func(_ context.Context, item Value) Result {
		calls++
		if item == 1 {
			return Failed(errors.New("stop"))
		}
		return Ready(nil)
	})
	if err == nil || !strings.Contains(err.Error(), "for iteration 1: stop") || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	calls = 0
	cancelCtx, cancel := context.WithCancel(ctx)
	_, err = ForEach(cancelCtx, []int{0, 1, 2}, func(context.Context, Value) Result { calls++; cancel(); return Ready(nil) })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	calls = 0
	if _, err := ForEach(cancelCtx, []Value{}, body); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal(err)
	}
}

func TestForEverBreakContinueAndCancellation(t *testing.T) {
	calls := 0
	value, err := ForEver(context.Background(), func(context.Context) Result {
		calls++
		if calls < 3 {
			return ContinueLoop()
		}
		return BreakLoop()
	})
	if err != nil || value != nil || calls != 3 {
		t.Fatalf("infinite loop controls: value=%v err=%v calls=%d", value, err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls = 0
	_, err = ForEver(ctx, func(context.Context) Result {
		calls++
		cancel()
		return Ready(nil)
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("infinite loop cancellation: err=%v calls=%d", err, calls)
	}
}
