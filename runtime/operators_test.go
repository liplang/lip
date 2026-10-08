package runtime

import (
	"math"
	"strings"
	"testing"
)

func TestNumericOperators(t *testing.T) {
	for _, tc := range []struct {
		op          string
		left, right Value
		want        float64
	}{
		{"//", 7, 2, 3}, {"//", -7, 2, -4}, {"//", 7, -2, -4}, {"//", -7, -2, 3},
		{"//", 7.5, 2, 3}, {"//", -7.5, 2, -4}, {"//", 0, -2, 0},
		{"**", 2, 3, 8}, {"**", 2, -2, 0.25}, {"**", 9, 0.5, 3},
		{"**", -2, 3, -8}, {"**", 0, 0, 1}, {"**", 2, -1075, 0},
		{"%", 7, 2, 1}, {"%", -7, 2, 1}, {"%", 7, -2, -1}, {"%", -7, -2, -1},
		{"%", 7.5, 2, 1.5}, {"%", -7.5, 2, 0.5}, {"%", 8, -2, 0},
		{"*/", 8, 2, 3}, {"*/", 1000, 10, 3}, {"*/", 9, 3, 2}, {"*/", 1, 0.5, 0},
	} {
		got, err := Binary(tc.op, tc.left, tc.right)
		if err != nil || got != tc.want && !(tc.op == "*/" && math.Abs(got.(float64)-tc.want) < 1e-12) {
			t.Fatalf("%v %s %v: %v, %v", tc.left, tc.op, tc.right, got, err)
		}
		if tc.want == 0 && math.Signbit(got.(float64)) {
			t.Fatalf("negative zero: %v %s %v", tc.left, tc.op, tc.right)
		}
	}
	for _, tc := range []struct {
		op          string
		left, right Value
		want        string
	}{
		{"//", 1, 0, "division by zero"}, {"//", 0, math.Copysign(0, -1), "division by zero"},
		{"//", "7", 2, "expected number"}, {"**", 2, true, "expected number"},
		{"//", 1e308, 1e-308, "not finite"},
		{"**", 2, 1024, "not finite"}, {"**", -4, 0.5, "not finite"}, {"**", 0, -1, "not finite"},
		{"%", 1, 0, "modulo by zero"}, {"%", true, 2, "expected number"},
		{"*/", 0, 2, "argument must be positive"}, {"*/", -8, 2, "argument must be positive"},
		{"*/", 8, 1, "base must be positive"}, {"*/", 8, 0, "base must be positive"}, {"*/", 8, -2, "base must be positive"},
		{"*/", 8, "2", "expected number"},
	} {
		if _, err := Binary(tc.op, tc.left, tc.right); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v %s %v: %v", tc.left, tc.op, tc.right, err)
		}
	}
}

func TestQuotientRemainderIdentity(t *testing.T) {
	for _, dividend := range []float64{-100, -7.5, -7, 0, 7, 7.5, 100} {
		for _, divisor := range []float64{-3, -2, 2, 3} {
			quotient, err := Binary("//", dividend, divisor)
			if err != nil {
				t.Fatal(err)
			}
			remainder, err := Binary("%", dividend, divisor)
			if err != nil {
				t.Fatal(err)
			}
			if dividend != quotient.(float64)*divisor+remainder.(float64) || math.Abs(remainder.(float64)) >= math.Abs(divisor) {
				t.Fatalf("%v / %v: quotient %v remainder %v", dividend, divisor, quotient, remainder)
			}
		}
	}
}
