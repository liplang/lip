package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"lipalpha/internal/stringops"
)

func TestStringCatalogSemantics(t *testing.T) {
	cases := []struct{ name, args, want string }{
		{"trim", `["\t你好 🌱\u3000"]`, `"你好 🌱"`},
		{"trim_start", `["  x  "]`, `"x  "`}, {"trim_end", `["  x  "]`, `"  x"`},
		{"lower", `["ÄBC你好"]`, `"äbc你好"`}, {"upper", `["äbc你好"]`, `"ÄBC你好"`},
		{"split", `["a,,b,",","]`, `["a","","b",""]`}, {"split", `["",","]`, `[""]`},
		{"split_whitespace", `[" \t你好\u3000世界\n "]`, `["你好","世界"]`}, {"split_whitespace", `[" \t"]`, `[]`},
		{"lines", `["a\r\nb\n"]`, `["a","b"]`}, {"lines", `["\n\n"]`, `["",""]`},
		{"lines", `[""]`, `[]`}, {"lines", `["a\rb\r"]`, `["a\rb\r"]`},
		{"chars", `["你好🌱"]`, `["你","好","🌱"]`}, {"chars", `[""]`, `[]`},
		{"join", `[["你好","🌱"]," / "]`, `"你好 / 🌱"`}, {"join", `[[],","]`, `""`},
		{"replace", `["aaaa","aa","x"]`, `"xx"`}, {"replace", `["aaaa","aa","x",1]`, `"xaa"`},
		{"replace", `["aaaa","aa","x",0]`, `"aaaa"`},
		{"contains", `["你好🌱","🌱"]`, `true`}, {"contains", `["",""]`, `true`},
		{"starts_with", `["你好","你"]`, `true`}, {"ends_with", `["你好","你"]`, `false`},
		{"find", `["你好🌱","🌱"]`, `2`}, {"find", `["你好","x"]`, `-1`}, {"find", `["你好",""]`, `0`},
		{"count", `["aaaaa","aa"]`, `2`}, {"count", `["你好你好","你好"]`, `2`},
		{"slice", `["你好🌱",1,3]`, `"好🌱"`}, {"slice", `["你好🌱",-2,-1]`, `"好"`},
		{"slice", `["你好🌱",-99,99]`, `"你好🌱"`}, {"slice", `["你好🌱",2,1]`, `""`},
		{"slice", `["",0,0]`, `""`}, {"slice", `["abc",3,3]`, `""`},
		{"repeat", `["🌱",3]`, `"🌱🌱🌱"`}, {"repeat", `["",999999999]`, `""`},
		{"parse_number", `["-1.25e2"]`, `-125`}, {"parse_number", `["+.5"]`, `0.5`},
	}
	covered := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name+tc.args, func(t *testing.T) {
			var args []Value
			if err := json.Unmarshal([]byte(tc.args), &args); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(args)
			got, err := StringCall(context.Background(), "string."+tc.name, args)
			encoded, _ := json.Marshal(got)
			if err != nil || string(encoded) != tc.want {
				t.Fatalf("got %s %v, want %s", encoded, err, tc.want)
			}
			after, _ := json.Marshal(args)
			if string(before) != string(after) {
				t.Fatal("mutated input")
			}
			covered["string."+tc.name] = true
		})
	}
	for _, spec := range stringops.All() {
		if !covered[spec.Name] {
			t.Errorf("missing semantic case: %s", spec.Name)
		}
	}
}

func TestStringErrorsAndBounds(t *testing.T) {
	cases := []struct {
		name string
		args []Value
		want string
	}{
		{"unknown", nil, "unknown string operation"}, {"trim", nil, "expects 1 argument"},
		{"trim", []Value{3}, "expected string"}, {"trim", []Value{string([]byte{255})}, "valid UTF-8"},
		{"join", []Value{[]Value{"a", 3}, ","}, "element 1"}, {"join", []Value{"abc", ","}, "expected list"},
		{"split", []Value{"abc", ""}, "nonempty"}, {"count", []Value{"abc", ""}, "nonempty"},
		{"replace", []Value{"abc", "", "x"}, "nonempty"}, {"replace", []Value{"abc", "a", "x", -1}, "nonnegative"},
		{"repeat", []Value{"x", -1}, "nonnegative"}, {"slice", []Value{"abc", 1.5, 2}, "integer"},
		{"repeat", []Value{"x", MaxStringBytes + 1}, "exceeds"},
		{"replace", []Value{strings.Repeat("a", MaxStringBytes/2+1), "a", "bb"}, "exceeds"},
		{"join", []Value{[]Value{strings.Repeat("a", MaxStringBytes/2), strings.Repeat("b", MaxStringBytes/2)}, ","}, "exceeds"},
		{"chars", []Value{strings.Repeat("x", MaxListLength+1)}, "element slots"},
		{"split", []Value{strings.Repeat(",", MaxListLength), ","}, "element slots"},
		{"lines", []Value{strings.Repeat("\n", MaxListLength+1)}, "element slots"},
		{"split_whitespace", []Value{strings.Repeat("x ", MaxListLength+1)}, "element slots"},
		{"trim", []Value{strings.Repeat("x", MaxStringBytes+1)}, "exceeds"},
	}
	for _, tc := range cases {
		_, err := StringCall(context.Background(), "string."+tc.name, tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.HasPrefix(err.Error(), "string."+tc.name+":") {
			t.Errorf("%s: %v, want %s", tc.name, err, tc.want)
		}
	}
	for _, text := range []string{"", " 1", "1 ", "NaN", "Inf", "0x1p2", "1_000", "1e999"} {
		if _, err := StringCall(context.Background(), "string.parse_number", []Value{text}); err == nil {
			t.Errorf("parsed %q", text)
		}
	}
	if _, err := StringCall(nil, "string.trim", []Value{"x"}); err == nil {
		t.Fatal("nil context")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, spec := range stringops.All() {
		if _, err := StringCall(ctx, spec.Name, nil); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: %v", spec.Name, err)
		}
	}
}

func TestStringCompositionLaws(t *testing.T) {
	ctx := context.Background()
	for _, text := range []string{"", "你好🌱", "e\u0301", "a,,b,"} {
		chars, err := StringCall(ctx, "string.chars", []Value{text})
		if err != nil {
			t.Fatal(err)
		}
		back, err := StringCall(ctx, "string.join", []Value{chars, ""})
		if err != nil || back != text {
			t.Fatalf("chars/join %q: %v %v", text, back, err)
		}
		parts, err := StringCall(ctx, "string.split", []Value{text, ","})
		if err != nil {
			t.Fatal(err)
		}
		back, err = StringCall(ctx, "string.join", []Value{parts, ","})
		if err != nil || back != text {
			t.Fatalf("split/join %q: %v %v", text, back, err)
		}
		length, _ := Length(text)
		back, err = StringCall(ctx, "string.slice", []Value{text, 0, length})
		if err != nil || !reflect.DeepEqual(back, text) {
			t.Fatalf("whole slice: %v %v", back, err)
		}
	}
}
