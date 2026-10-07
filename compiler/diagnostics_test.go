package compiler

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckReports(t *testing.T) {
	for _, tc := range []struct {
		source, code string
		line         int
	}{
		{"flow Good(x:string)->string{return x}", "", 0},
		{"flow Bad()->string {\n return string.nope(\"x\")\n}", "LIP_UNKNOWN_OPERATION", 2},
		{"flow Bad()->string {\n return string.trim(1)\n}", "LIP_TYPE_ERROR", 2},
		{"flow Bad()->string {\n return missing\n}", "LIP_NAME_ERROR", 2},
		{"flow Bad()->string {\n return \"oops\n}", "LIP_LEX_ERROR", 2},
		{"flow Bad(x:string) string {return x}", "LIP_SYNTAX_ERROR", 1},
		{"flow Bad(x:string)->string{return fetch(x)}", "LIP_DEPENDENCY_ERROR", 1},
		{"flow Bad()->number{type=1 type=2 return type}", "LIP_CHECK_ERROR", 1},
		{"fn f(x:number){return f(x)} flow Bad()->number{return f(1)}", "LIP_RECURSION_ERROR", 1},
	} {
		report := CheckSource("中文.lip", tc.source)
		if report.Schema != "lip.diagnostics.v1" || report.File != "中文.lip" || report.OK != (tc.code == "") {
			t.Fatal(report)
		}
		data, err := json.Marshal(report)
		if err != nil || !json.Valid(data) {
			t.Fatal(err)
		}
		if tc.code == "" {
			if len(report.Diagnostics) != 0 || report.Flow != "Good" {
				t.Fatal(report)
			}
		} else {
			if len(report.Diagnostics) != 1 {
				t.Fatal(report)
			}
			d := report.Diagnostics[0]
			if d.Code != tc.code || d.Line != tc.line || d.Column < 1 || d.Severity != "error" || len(d.Hints) == 0 {
				t.Fatalf("%s: %+v", tc.source, d)
			}
			if d.SourceLine != strings.Split(tc.source, "\n")[tc.line-1] {
				t.Fatal(d)
			}
		}
		if second, _ := json.Marshal(CheckSource("中文.lip", tc.source)); string(second) != string(data) {
			t.Fatal("unstable report")
		}
	}
	report := CheckFile(filepath.Join(t.TempDir(), "missing.lip"))
	if report.OK || report.Diagnostics[0].Code != "LIP_IO_ERROR" || report.Diagnostics[0].Line != 0 {
		t.Fatal(report)
	}
}
