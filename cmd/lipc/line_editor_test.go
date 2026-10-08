package main

import (
	"bufio"
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	"lipalpha/compiler/ast"
	"lipalpha/runtime"
)

func TestLineEditorKeys(t *testing.T) {
	for _, tc := range []struct {
		name, keys, want string
		history          []string
		err              error
	}{
		{"arrows-insert", "print(13)\x1b[D\x1b[D\x1b[D2\x1b[C\x1b[C\n", "print(213)\n", nil, nil},
		{"home-end-delete", "abc\x1b[H\x1b[3~\x1b[F!\n", "bc!\n", nil, nil},
		{"control-keys", "foo bar\x01X\x05\x17baz\n", "Xfoo baz\n", nil, nil},
		{"erase-start", "abcd\x1b[D\x1b[D\x15XY\n", "XYcd\n", nil, nil},
		{"erase-end", "abcd\x1b[D\x1b[D\x0b\n", "ab\n", nil, nil},
		{"delete-forward", "abcd\x01\x04\n", "bcd\n", nil, nil},
		{"cjk", "你好\x1b[D世\x1b[C\x7f\n", "你世\n", nil, nil},
		{"combining-delete", "ae\u0301\x7f\n", "a\n", nil, nil},
		{"emoji-delete", "a👩🏽‍💻\x7f\n", "a\n", nil, nil},
		{"flag-delete", "a🇨🇳\x1b[D\x1b[3~\n", "a\n", nil, nil},
		{"history", "\x1b[A\x1b[A\x1b[B\x7f!\n", "secon!\n", []string{"first", "second"}, nil},
		{"restore-draft-cursor", "draft\x1b[D\x1b[A\x1b[B!\n", "draf!t\n", []string{"old"}, nil},
		{"history-multiline", "\x1b[A\n", "a = 2\nb = 3\n", []string{"a = 2\nb = 3"}, nil},
		{"paste", "\x1b[200~a=2;\r\nb=3\r\nprint(a/b)\r\n\x1b[201~\n", "a=2;\nb=3\nprint(a/b)\n", nil, nil},
		{"paste-control", "\x1b[200~a\x03b\x1b[201~\n", "ab\n", nil, nil},
		{"cancel", "[1,\x03", "", nil, errInputCanceled},
		{"eof-empty", "\x04", "", nil, io.EOF},
		{"escape-alone", "\x1b", "", nil, io.EOF},
		{"clear-screen", "abc\x0c\n", "abc\n", nil, nil},
		{"ctrl-arrows", "foo bar\x1b[1;5D!\x1b[1;5C?\n", "foo !bar?\n", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			editor := &lineEditor{input: bufio.NewReader(strings.NewReader(tc.keys)), output: &output, history: tc.history, columns: 12}
			got, err := editor.readLine("In [1]: ")
			if got != tc.want || err != tc.err {
				t.Fatalf("got %q, %v; want %q, %v", got, err, tc.want, tc.err)
			}
			if !strings.HasSuffix(output.String(), "\x1b[?2004l") {
				t.Fatal("bracketed paste was not restored")
			}
		})
	}
}

func TestLineEditorCompletion(t *testing.T) {
	s := &replSession{values: map[string]runtime.Value{"alphabet": 2}, functions: []*ast.Function{{Name: "double"}}, dependencies: []ast.Dependency{{Kind: "python", Spec: "math", Alias: "m"}}}
	for prefix, want := range map[string][]string{"alph": {"alphabet"}, ":q": {":quit"}, "dou": {"double"}, "m.": {"m."}, "math.": {"math."}, "string.tri": {"string.trim", "string.trim_end", "string.trim_start"}} {
		if got := s.completions(prefix); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %v", prefix, got)
		}
	}
	var output bytes.Buffer
	editor := &lineEditor{input: bufio.NewReader(strings.NewReader("alph\t + 1\n")), output: &output, complete: s.completions}
	if line, err := editor.readLine(""); err != nil || line != "alphabet + 1\n" {
		t.Fatalf("%q: %v", line, err)
	}
}

func TestImportCompletionUsesCallableNames(t *testing.T) {
	s := &replSession{values: map[string]runtime.Value{"hidden": nil, "math": 1}, dependencies: []ast.Dependency{
		{Kind: "python", Spec: "math", Alias: "m"},
		{Kind: "python", Spec: "numpy>=1.26", Alias: "np"},
		{Kind: "go", Spec: "fmt", Alias: "f"},
		{Kind: "go", Spec: "example.com/adapter", Alias: "adapter"},
		{Kind: "host", Spec: "service.*", Alias: "s"},
		{Kind: "host", Spec: "service.fetch", Alias: "fetch"},
		{Kind: "host", Spec: "hidden.*"},
	}}
	for prefix, want := range map[string][]string{
		"math.": nil, "m.": {"m."}, "np.": {"np."}, "numpy.": {"numpy."},
		"fmt.": {"fmt."}, "f.": {"f."}, "adapter.": {"adapter."}, "example.com/": nil,
		"service.": {"service.", "service.fetch"}, "s.": {"s."}, "fetch": {"fetch"},
		"fetch.": nil, "hidden.": nil,
	} {
		if got := s.completions(prefix); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, want %v", prefix, got, want)
		}
	}
}

func TestLibraryCompletionRespectsImportNamespaces(t *testing.T) {
	for _, tc := range []struct {
		dependency ast.Dependency
		wantCore   bool
	}{
		{ast.Dependency{Kind: "python", Spec: "string"}, false},
		{ast.Dependency{Kind: "python", Spec: "string", Alias: "text"}, true},
		{ast.Dependency{Kind: "python", Spec: "math", Alias: "string"}, false},
		{ast.Dependency{Kind: "go", Spec: "string"}, false},
		{ast.Dependency{Kind: "host", Spec: "string.*"}, false},
		{ast.Dependency{Kind: "host", Spec: "string.*", Alias: "text"}, true},
		{ast.Dependency{Kind: "host", Spec: "emit", Alias: "string"}, true},
	} {
		s := &replSession{dependencies: []ast.Dependency{tc.dependency}}
		got := s.completions("string.trim")
		if (len(got) > 0) != tc.wantCore {
			t.Errorf("%+v: misleading library completion %v", tc.dependency, got)
		}
	}
}

func TestInputLayout(t *testing.T) {
	for _, tc := range []struct {
		text                           string
		cursor, columns, end, row, col int
	}{
		{"你好a", 2, 12, 1, 1, 0},
		{"e\u0301a", 2, 12, 0, 0, 9},
		{"🇨🇳a", 2, 12, 0, 0, 10},
		{"a\nb", 2, 12, 1, 1, 8},
		{"a\tb", 2, 20, 0, 0, 12},
	} {
		_, end, row, col := inputLayout("In [1]: ", []rune(tc.text), tc.cursor, tc.columns)
		if end != tc.end || row != tc.row || col != tc.col {
			t.Fatalf("%q: end %d, cursor %d:%d", tc.text, end, row, col)
		}
	}
}
