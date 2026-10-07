// Package stringops defines the fixed pure string library signatures shared by
// the compiler and runtime. Names follow Rust where practical.
package stringops

import (
	_ "embed"
	"sort"
)

// Source is bundled for standalone compiler builds.
//
//go:embed catalog.go
var Source string

type Spec struct {
	Name             string
	MinArgs, MaxArgs int
	Types            []string
	Result           string
}

func op(name, result string, min, max int, types ...string) Spec {
	return Spec{Name: "string." + name, Result: result, MinArgs: min, MaxArgs: max, Types: types}
}

var catalog = func() map[string]Spec {
	entries := []Spec{
		op("trim", "string", 1, 1, "string"), op("trim_start", "string", 1, 1, "string"), op("trim_end", "string", 1, 1, "string"),
		op("lower", "string", 1, 1, "string"), op("upper", "string", 1, 1, "string"),
		op("split", "list", 2, 2, "string", "string"), op("split_whitespace", "list", 1, 1, "string"),
		op("lines", "list", 1, 1, "string"), op("chars", "list", 1, 1, "string"),
		op("join", "string", 2, 2, "list", "string"), op("replace", "string", 3, 4, "string", "string", "string", "number"),
		op("contains", "bool", 2, 2, "string", "string"), op("starts_with", "bool", 2, 2, "string", "string"), op("ends_with", "bool", 2, 2, "string", "string"),
		op("find", "number", 2, 2, "string", "string"), op("count", "number", 2, 2, "string", "string"),
		op("slice", "string", 3, 3, "string", "number", "number"), op("repeat", "string", 2, 2, "string", "number"),
		op("parse_number", "number", 1, 1, "string"),
	}
	result := make(map[string]Spec, len(entries))
	for _, spec := range entries {
		result[spec.Name] = spec
	}
	return result
}()

func Lookup(name string) (Spec, bool) { spec, ok := catalog[name]; return spec, ok }
func All() []Spec {
	result := make([]Spec, 0, len(catalog))
	for _, spec := range catalog {
		result = append(result, spec)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
