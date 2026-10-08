package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

type nativeNumber float64
type nativeInteger int64
type nativeUnsigned uint64
type nativeBool bool
type nativeText string

func TestDefinedGoScalarTypes(t *testing.T) {
	for _, tc := range []struct {
		value Value
		typ   string
	}{
		{nativeNumber(3.5), "number"}, {nativeInteger(7), "number"},
		{nativeUnsigned(8), "number"}, {nativeBool(true), "bool"}, {nativeText("你好"), "string"},
	} {
		if got := TypeName(tc.value); got != tc.typ {
			t.Fatalf("%T: type %s", tc.value, got)
		}
		if err := CheckType(tc.value, tc.typ); err != nil {
			t.Fatalf("%T is reported as %s but rejected: %v", tc.value, tc.typ, err)
		}
		if err := CheckType(tc.value, tc.typ+"?"); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		op          string
		left, right Value
		want        Value
	}{
		{"+", nativeNumber(3.5), nativeInteger(2), float64(5.5)},
		{"+", nativeText("你"), "好", "你好"},
		{"*", nativeText("好"), nativeInteger(2), "好好"},
		{"&&", nativeBool(true), true, true},
		{"==", nativeNumber(7), float64(7), true},
		{"==", nativeText("好"), "好", true},
		{"==", nativeBool(false), false, true},
		{"==", nativeText("7"), nativeInteger(7), false},
	} {
		got, err := Binary(tc.op, tc.left, tc.right)
		if err != nil || got != tc.want {
			t.Fatalf("%T %s %T: %v %v", tc.left, tc.op, tc.right, got, err)
		}
	}
	for _, value := range []Value{nativeNumber(math.Inf(1)), nativeNumber(math.NaN()), nativeText(string([]byte{255}))} {
		if err := CheckType(value, TypeName(value)); err == nil {
			t.Fatalf("invalid %T accepted", value)
		}
	}
	if _, err := Number(nativeText("7")); err == nil {
		t.Fatal("string implicitly converted to number")
	}
	if _, err := Bool(nativeInteger(1)); err == nil {
		t.Fatal("number implicitly converted to bool")
	}
	if _, err := Binary("+", json.Number("7"), "x"); err == nil {
		t.Fatal("json.Number treated as string")
	}
}

func TestDefinedGoStringsAcrossCoreOperations(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		args []Value
		want Value
	}{
		{"string.upper", []Value{nativeText("hello")}, "HELLO"},
		{"string.contains", []Value{nativeText("你好"), nativeText("好")}, true},
		{"string.replace", []Value{nativeText("a-a"), nativeText("a"), nativeText("b")}, "b-b"},
		{"string.join", []Value{[]nativeText{"a", "b"}, nativeText("/")}, "a/b"},
	} {
		got, err := StringCall(ctx, tc.name, tc.args)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: %v %v", tc.name, got, err)
		}
		if _, ok := tc.args[len(tc.args)-1].(nativeText); !ok {
			t.Fatal("operation changed caller's argument slice")
		}
	}
	if got, err := Length(nativeText("你好")); err != nil || got != float64(2) {
		t.Fatalf("len: %v %v", got, err)
	}
	if got, err := Index(map[nativeText]int{"你好": 7}, nativeText("你好")); err != nil || got != 7 {
		t.Fatalf("object key: %v %v", got, err)
	}
	if got, err := ListCall(ctx, "list.sort", []Value{[]nativeText{"b", "a"}}, nil); err != nil || !reflect.DeepEqual(got, []Value{nativeText("a"), nativeText("b")}) {
		t.Fatalf("sort: %v %v", got, err)
	}
	var printed bytes.Buffer
	if err := PrintValues(&printed, []Value{nativeText("你好")}); err != nil || printed.String() != "你好\n" || StringValue(nativeText("你好")) != "你好" {
		t.Fatalf("display: %q %v", printed.String(), err)
	}
	if _, err := Fail(ctx, nativeText("stop")); err == nil || err.Error() != "fail: stop" {
		t.Fatal(err)
	}
}

func TestNativeMapIndexAndUnicodeField(t *testing.T) {
	if _, err := Index(map[any]Value{}, []Value{1}); err == nil || !strings.Contains(err.Error(), "comparable") {
		t.Fatalf("unhashable Host key did not report an error: %v", err)
	}
	object := struct{ État nativeText }{État: "ready"}
	if got, err := Field(object, "état"); err != nil || got != nativeText("ready") {
		t.Fatalf("Unicode exported field: %v %v", got, err)
	}
}
