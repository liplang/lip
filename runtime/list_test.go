package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"lipalpha/internal/listops"
)

func TestListCatalogSemanticsAndImmutability(t *testing.T) {
	identity := func(_ context.Context, args []Value) Result { return Ready(args[0]) }
	positive := func(_ context.Context, args []Value) Result { n, _ := Number(args[0]); return Ready(n > 0) }
	double := func(_ context.Context, args []Value) Result { n, _ := Number(args[0]); return Ready(n * 2) }
	negative := func(_ context.Context, args []Value) Result { n, _ := Number(args[0]); return Ready(-n) }
	add := func(_ context.Context, args []Value) Result {
		v, err := Binary("+", args[0], args[1])
		if err != nil {
			return Failed(err)
		}
		return Ready(v)
	}
	cases := []struct {
		name, args, want string
		callback         Op
	}{
		{"concat", `[[1,2],[],[3]]`, `[1,2,3]`, nil},
		{"append", `[[1,2],3]`, `[1,2,3]`, nil}, {"prepend", `[[1,2],3]`, `[3,1,2]`, nil},
		{"reverse", `[[1,2,3]]`, `[3,2,1]`, nil},
		{"take", `[[1,2,3],-2]`, `[2,3]`, nil}, {"drop", `[[1,2,3],-2]`, `[1]`, nil},
		{"slice", `[[1,2,3,4],-3,-1]`, `[2,3]`, nil},
		{"partition", `[[1,2,3,4,5],2]`, `[[1,2],[3,4],[5]]`, nil},
		{"partition", `[[1,2,3],2,1]`, `[[1,2],[2,3],[3]]`, nil},
		{"flatten", `[[1,[2,[3]],[],null]]`, `[1,2,3,null]`, nil},
		{"flatten", `[[1,[2,[3]]],1]`, `[1,2,[3]]`, nil},
		{"flatten", `[[1,[2,[3]]],0]`, `[1,[2,[3]]]`, nil},
		{"transpose", `[[[1,2,3],[4,5,6]]]`, `[[1,4],[2,5],[3,6]]`, nil},
		{"zip", `[[1,2,3],["a","b"]]`, `[[1,"a"],[2,"b"]]`, nil},
		{"enumerate", `[["a","b"]]`, `[[0,"a"],[1,"b"]]`, nil},
		{"riffle", `[[1,2,3],"x"]`, `[1,"x",2,"x",3]`, nil},
		{"repeat", `[{"a":1},3]`, `[{"a":1},{"a":1},{"a":1}]`, nil},
		{"cartesian", `[[1,2],["a","b"]]`, `[[1,"a"],[1,"b"],[2,"a"],[2,"b"]]`, nil},
		{"unique", `[[1,2,1,{"a":1},{"a":1},null,null]]`, `[1,2,{"a":1},null]`, nil},
		{"contains", `[[1,2],2]`, `true`, nil}, {"count", `[[1,2,1],1]`, `2`, nil},
		{"sort", `[[3,1,2]]`, `[1,2,3]`, nil}, {"sort", `[["b","a"]]`, `["a","b"]`, nil},
		{"sort_by", `[[3,1,2]]`, `[3,2,1]`, negative},
		{"group", `[[1,2,1]]`, `[{"key":1,"values":[1,1]},{"key":2,"values":[2]}]`, nil},
		{"group_by", `[[1,0,2,-1]]`, `[{"key":true,"values":[1,2]},{"key":false,"values":[0,-1]}]`, positive},
		{"split_by", `[[1,2,0,3]]`, `[{"key":true,"values":[1,2]},{"key":false,"values":[0]},{"key":true,"values":[3]}]`, positive},
		{"map", `[[1,2,3]]`, `[2,4,6]`, double}, {"filter", `[[1,0,2,-1]]`, `[1,2]`, positive},
		{"any", `[[0,1,-1]]`, `true`, positive}, {"all", `[[1,2,3]]`, `true`, positive},
		{"scan", `[[1,2,3],0]`, `[0,1,3,6]`, add},
		{"sum", `[[1,2,3]]`, `6`, nil}, {"product", `[[1,2,3]]`, `6`, nil},
		{"min", `[[3,1,2]]`, `1`, nil}, {"max", `[["a","b"]]`, `"b"`, nil},
		{"first", `[[1,2,3]]`, `1`, nil}, {"last", `[[1,2,3]]`, `3`, nil},
		{"concat", `[]`, `[]`, nil}, {"zip", `[]`, `[]`, nil}, {"cartesian", `[]`, `[[]]`, nil},
		{"cartesian", `[[1],[]]`, `[]`, nil}, {"transpose", `[[]]`, `[]`, nil}, {"transpose", `[[[],[]]]`, `[]`, nil},
		{"scan", `[[],0]`, `[0]`, add}, {"any", `[[]]`, `false`, positive}, {"all", `[[]]`, `true`, positive},
		{"map", `[[]]`, `[]`, identity}, {"group_by", `[[]]`, `[]`, identity},
		{"sum", `[[]]`, `0`, nil}, {"product", `[[]]`, `1`, nil},
		{"take", `[[1,2],99]`, `[1,2]`, nil}, {"drop", `[[1,2],99]`, `[]`, nil},
		{"slice", `[[1,2],1,0]`, `[]`, nil},
	}
	covered := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name+tc.args, func(t *testing.T) {
			var args []Value
			if err := json.Unmarshal([]byte(tc.args), &args); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(args)
			value, err := ListCall(context.Background(), "list."+tc.name, args, tc.callback)
			got, _ := json.Marshal(value)
			if err != nil || string(got) != tc.want {
				t.Fatalf("got %s %v, want %s", got, err, tc.want)
			}
			after, _ := json.Marshal(args)
			if string(before) != string(after) {
				t.Fatal("operation changed its source")
			}
			covered["list."+tc.name] = true
		})
	}
	for _, spec := range listops.All() {
		if !covered[spec.Name] {
			t.Errorf("no semantic example for %s", spec.Name)
		}
	}
}

func TestListAlgebraAndStableSort(t *testing.T) {
	ctx := context.Background()
	values := []Value{1, 2, 3, 4, 5, 6, 7}
	for _, size := range []int{1, 2, 3, 9} {
		chunks, err := ListCall(ctx, "list.partition", []Value{values, size}, nil)
		if err != nil {
			t.Fatal(err)
		}
		flat, err := ListCall(ctx, "list.flatten", []Value{chunks, 1}, nil)
		if err != nil || !reflect.DeepEqual(flat, values) {
			t.Fatalf("partition/flatten: %v %v", flat, err)
		}
	}
	matrix := []Value{[]Value{1, 2, 3}, []Value{4, 5, 6}}
	columns, err := ListCall(ctx, "list.transpose", []Value{matrix}, nil)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ListCall(ctx, "list.transpose", []Value{columns}, nil)
	if err != nil || !reflect.DeepEqual(back, matrix) {
		t.Fatalf("transpose twice: %v %v", back, err)
	}
	rows := []Value{map[string]Value{"key": 1, "id": "a"}, map[string]Value{"key": 0, "id": "b"}, map[string]Value{"key": 1, "id": "c"}}
	sorted, err := ListCall(ctx, "list.sort_by", []Value{rows}, func(_ context.Context, args []Value) Result {
		value, err := Field(args[0], "key")
		if err != nil {
			return Failed(err)
		}
		return Ready(value)
	})
	if err != nil || !reflect.DeepEqual(sorted, []Value{rows[1], rows[0], rows[2]}) {
		t.Fatalf("stable sort: %v %v", sorted, err)
	}
	unique, err := ListCall(ctx, "list.unique", []Value{[]Value{1, float64(1), json.Number("1.0"), "1"}}, nil)
	if err != nil || !reflect.DeepEqual(unique, []Value{1, "1"}) {
		t.Fatalf("numeric equality: %v %v", unique, err)
	}
}

func TestListFailuresAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []Value
	}{
		{"list.transpose", []Value{[]Value{[]Value{1, 2}, []Value{3}}}},
		{"list.transpose", []Value{[]Value{1}}},
		{"list.partition", []Value{[]int{1, 2}, 0}}, {"list.partition", []Value{[]int{1, 2}, 1, -1}},
		{"list.flatten", []Value{[]int{}, -1}}, {"list.take", []Value{[]int{}, 1.5}},
		{"list.repeat", []Value{1, -1}}, {"list.repeat", []Value{1, MaxListLength + 1}},
		{"list.first", []Value{[]int{}}}, {"list.last", []Value{[]int{}}},
		{"list.min", []Value{[]int{}}}, {"list.max", []Value{[]int{}}},
		{"list.sort", []Value{[]Value{1, "a"}}}, {"list.sort", []Value{[]Value{nil}}},
		{"list.sum", []Value{[]Value{1, "a"}}}, {"list.product", []Value{[]Value{1e308, 1e308}}},
		{"list.cartesian", []Value{make([]Value, 1001), make([]Value, 1001)}},
		{"list.concat", []Value{make([]Value, 600001), make([]Value, 600001)}},
		{"list.append", []Value{make([]Value, MaxListLength), 1}},
		{"list.group", []Value{[]Value{func() {}}}},
	} {
		if value, err := ListCall(context.Background(), tc.name, tc.args, nil); err == nil || value != nil {
			t.Fatalf("%s accepted invalid args (%v)", tc.name, err)
		}
	}
	cycle := make([]Value, 1)
	cycle[0] = cycle
	if _, err := ListCall(context.Background(), "list.flatten", []Value{cycle}, nil); err == nil || !strings.Contains(err.Error(), "nesting exceeds") {
		t.Fatalf("cycle: %v", err)
	}
	for _, spec := range listops.All() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := ListCall(ctx, spec.Name, nil, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s did not observe cancellation: %v", spec.Name, err)
		}
	}
}

func TestListCallbacksShortCircuitAndErrors(t *testing.T) {
	for _, name := range []string{"list.any", "list.all"} {
		calls := 0
		value, err := ListCall(context.Background(), name, []Value{[]int{1, 2, 3}}, func(context.Context, []Value) Result { calls++; return Ready(name == "list.any") })
		if err != nil || calls != 1 || value != (name == "list.any") {
			t.Fatalf("short circuit %s: %v %v %d", name, value, err, calls)
		}
	}
	sentinel := errors.New("callback failed")
	for _, name := range []string{"list.map", "list.filter", "list.sort_by", "list.group_by", "list.split_by", "list.any", "list.all", "list.scan"} {
		args := []Value{[]int{1, 2}}
		if name == "list.scan" {
			args = append(args, 0)
		}
		_, err := ListCall(context.Background(), name, args, func(context.Context, []Value) Result { return Failed(sentinel) })
		if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "element 0") {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := ListCall(context.Background(), "list.filter", []Value{[]int{1}}, func(context.Context, []Value) Result { return Ready(1) }); err == nil {
		t.Fatal("predicate truthiness accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := ListCall(ctx, "list.map", []Value{[]int{1, 2}}, func(context.Context, []Value) Result { calls++; cancel(); return Ready(1) })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancelled callback: %v %d", err, calls)
	}
}

func TestListErrorsKeepTheActualCause(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []Value
		want string
	}{
		{"list.transpose", []Value{[]Value{[]Value{1}, 2}}, "row 1: expected list, got number"},
		{"list.transpose", []Value{[]Value{make([]Value, MaxListLength+1)}}, "row 0: list result exceeds"},
		{"list.sort", []Value{[]Value{1, "two"}}, "element 1 has type string, expected number"},
		{"list.sort", []Value{[]Value{nil}}, "element 0 must be number or string, got null"},
	} {
		_, err := ListCall(context.Background(), tc.name, tc.args, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
}
