package runtime

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestRangeBoundsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		args []Value
		want []Value
	}{
		{[]Value{0, 5}, []Value{float64(0), float64(1), float64(2), float64(3), float64(4)}},
		{[]Value{5, -1, -2}, []Value{float64(5), float64(3), float64(1)}},
		{[]Value{1, 1}, []Value{}}, {[]Value{5, 0}, []Value{}},
		{[]Value{0, 5, -1}, []Value{}},
		{[]Value{9007199254740989.0, 9007199254740991.0}, []Value{9007199254740989.0, 9007199254740990.0}},
	} {
		got, err := Range(context.Background(), tc.args)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("range(%v)=%v, %v", tc.args, got, err)
		}
	}
	for _, args := range [][]Value{{1}, {0, 3, 0}, {0, 1.5}, {0, math.Inf(1)}, {0, 9007199254740992.0}, {0, MaxRangeLength + 1}, {"0", 3}} {
		if _, err := Range(context.Background(), args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	// Exactly the advertised cap is valid; failures do not partially return data.
	value, err := Range(context.Background(), []Value{0, MaxRangeLength})
	if err != nil || len(value.([]Value)) != MaxRangeLength {
		t.Fatalf("range cap: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if value, err := Range(ctx, []Value{0, 10}); value != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled range: %v %v", value, err)
	}
}

func TestLocalCallDepthAndCancellation(t *testing.T) {
	root := context.Background()
	deep := root
	for index := 0; index < MaxCallDepth; index++ {
		child, err := EnterFunction(deep)
		if err != nil {
			t.Fatal(err)
		}
		deep = child
	}
	if _, err := EnterFunction(deep); err == nil {
		t.Fatal("accepted an unbounded call")
	}
	if _, err := EnterFunction(root); err != nil {
		t.Fatal("sibling inherited another call's depth", err)
	}
	ctx, cancel := context.WithCancel(root)
	cancel()
	if _, err := EnterFunction(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled local call: %v", err)
	}
}

func TestFoldOrderEmptyFailureAndCancellation(t *testing.T) {
	ctx := context.Background()
	appendDigit := func(_ context.Context, args []Value) Result {
		left, _ := Number(args[0])
		right, _ := Number(args[1])
		return Ready(left*10 + right)
	}
	if got, err := Fold(ctx, []int{1, 2, 3}, 0, appendDigit); err != nil || got != float64(123) {
		t.Fatalf("order: %v %v", got, err)
	}
	calls := 0
	if got, err := Fold(ctx, []Value{}, "seed", func(context.Context, []Value) Result { calls++; return Failed(errors.New("unused")) }); err != nil || got != "seed" || calls != 0 {
		t.Fatalf("empty: %v %v %d", got, err, calls)
	}
	sentinel := errors.New("reducer failure")
	_, err := Fold(ctx, []int{1, 2, 3}, 0, func(_ context.Context, args []Value) Result {
		if args[1] == 2 {
			return Failed(sentinel)
		}
		return Ready(args[0])
	})
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "element 1") {
		t.Fatalf("failure index: %v", err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	calls = 0
	_, err = Fold(cancelCtx, []int{1, 2, 3}, 0, func(context.Context, []Value) Result { calls++; cancel(); return Ready(0) })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancellation: %v, calls=%d", err, calls)
	}
	for _, source := range []Value{nil, "abc", 2, map[string]Value{}} {
		if _, err := Fold(ctx, source, 0, appendDigit); err == nil {
			t.Fatalf("accepted source %v", source)
		}
	}
}

func TestDataShapesAndUnicode(t *testing.T) {
	for _, tc := range []struct {
		value Value
		typ   string
		valid bool
	}{
		{[]int{1}, "list", true}, {[2]int{}, "list", true}, {map[string]int{}, "object", true},
		{map[int]int{}, "object", false}, {struct{}{}, "object", false}, {nil, "list", false},
		{nil, "number?", true}, {"bad", "number?", false}, {nil, "any", true},
	} {
		if err := CheckType(tc.value, tc.typ); (err == nil) != tc.valid {
			t.Fatalf("%T as %s: %v", tc.value, tc.typ, err)
		}
	}
	if got, err := Length("你好🌱"); err != nil || got != float64(3) {
		t.Fatalf("length: %v %v", got, err)
	}
	if got, err := Index("你好🌱", 2); err != nil || got != "🌱" {
		t.Fatalf("index: %v %v", got, err)
	}
	if _, err := Index(map[string]Value{"A": 1}, 65); err == nil {
		t.Fatal("numeric key converted to a string")
	}
}
