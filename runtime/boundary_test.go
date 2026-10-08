package runtime

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type brokenPrintWriter struct{ err error }

func (w brokenPrintWriter) Write(p []byte) (int, error) { return 0, w.err }

func TestUnifiedValueDisplay(t *testing.T) {
	for _, tc := range []struct {
		value     Value
		text, typ string
	}{
		{nil, "null", "null"}, {true, "true", "bool"}, {float64(0.4), "0.4", "number"}, {"你好", "你好", "string"},
		{[]any{float64(2), nil, "a"}, `[2,null,"a"]`, "list"}, {map[string]any{"b": nil, "a": float64(1)}, `{"a":1,"b":null}`, "object"},
	} {
		var output bytes.Buffer
		if err := PrintValues(&output, []Value{tc.value}); err != nil {
			t.Fatal(err)
		}
		formatted, err := FormatValue(tc.value)
		if err != nil || formatted != tc.text || StringValue(tc.value) != tc.text || output.String() != tc.text+"\n" || TypeName(tc.value) != tc.typ {
			t.Fatalf("%#v: format %q, str %q, print %q, type %q", tc.value, formatted, StringValue(tc.value), output.String(), TypeName(tc.value))
		}
	}
	if err := PrintValues(brokenPrintWriter{io.ErrClosedPipe}, []Value{2}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	if err := PrintValues(brokenPrintWriter{}, []Value{2}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	if _, err := Number(nil); err == nil || !strings.Contains(err.Error(), "got null") {
		t.Fatal(err)
	}
}

func TestExecutionHintsAvoidMisleadingConversions(t *testing.T) {
	for _, tc := range []struct{ message, want, avoid string }{
		{"expected number, got string (3)", "parse_number", "print"},
		{"expected number, got null (null)", "handle the missing", "parse_number"},
		{"expected bool, got number (1)", "comparison", "print"},
		{"index 3 out of range", "index >= 0", "index < len" + " only"},
		{"range step cannot be zero", "positive step", "str("},
		{"list.sort: sort key at element 1 has type string, expected number", "all numbers or all strings", "parse_number"},
		{"list.first: needs a nonempty list", "len(values) == 0", "callback"},
		{"unknown Python handle abc", "same cell", "import host"},
		{"ModuleNotFoundError: No module named x", "interpreter environment", "scikit-learn"},
	} {
		hint := ExecutionHint(errors.New(tc.message))
		if !strings.Contains(hint, tc.want) || strings.Contains(hint, tc.avoid) {
			t.Fatalf("%s: %s", tc.message, hint)
		}
	}
	for _, message := range []string{"fail: division by zero is intentional", "list.transpose: row 2 has 3 columns, expected 2", "unexpected host error"} {
		if hint := ExecutionHint(errors.New(message)); hint != "" {
			t.Fatalf("speculative hint for %q: %s", message, hint)
		}
	}
}
