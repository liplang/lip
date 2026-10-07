// Package listops is the shared, declarative pure-list catalog. It contains no
// evaluator or Host dependency; compiler and runtime use the same signatures.
package listops

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
	MinArgs, MaxArgs int // -1 means variadic.
	Types            []string
	Result           string
	Callback         int // -1 means none; otherwise the final source argument.
	CallbackArity    int
	CallbackResult   string
}

func value(name, result string, min, max int, types ...string) Spec {
	return Spec{Name: "list." + name, Result: result, MinArgs: min, MaxArgs: max, Types: types, Callback: -1}
}
func callback(name, result string, arity int, fnResult string, types ...string) Spec {
	s := value(name, result, len(types)+1, len(types)+1, append(types, "fn")...)
	s.Callback = len(types)
	s.CallbackArity = arity
	s.CallbackResult = fnResult
	return s
}

var catalog = func() map[string]Spec {
	entries := []Spec{
		value("concat", "list", 0, -1, "list"),
		value("append", "list", 2, 2, "list", "any"), value("prepend", "list", 2, 2, "list", "any"),
		value("reverse", "list", 1, 1, "list"), value("take", "list", 2, 2, "list", "number"), value("drop", "list", 2, 2, "list", "number"),
		value("slice", "list", 3, 3, "list", "number", "number"), value("partition", "list", 2, 3, "list", "number", "number"),
		value("flatten", "list", 1, 2, "list", "number"), value("transpose", "list", 1, 1, "list"), value("zip", "list", 0, -1, "list"),
		value("enumerate", "list", 1, 1, "list"), value("riffle", "list", 2, 2, "list", "any"), value("repeat", "list", 2, 2, "any", "number"),
		value("cartesian", "list", 0, -1, "list"), value("unique", "list", 1, 1, "list"), value("contains", "bool", 2, 2, "list", "any"),
		value("count", "number", 2, 2, "list", "any"), value("sort", "list", 1, 1, "list"), value("group", "list", 1, 1, "list"),
		value("sum", "number", 1, 1, "list"), value("product", "number", 1, 1, "list"),
		value("min", "any", 1, 1, "list"), value("max", "any", 1, 1, "list"), value("first", "any", 1, 1, "list"), value("last", "any", 1, 1, "list"),
		callback("map", "list", 1, "any", "list"), callback("filter", "list", 1, "bool", "list"),
		callback("group_by", "list", 1, "any", "list"), callback("split_by", "list", 1, "any", "list"), callback("sort_by", "list", 1, "any", "list"),
		callback("any", "bool", 1, "bool", "list"), callback("all", "bool", 1, "bool", "list"), callback("scan", "list", 2, "any", "list", "any"),
	}
	result := map[string]Spec{}
	for _, s := range entries {
		result[s.Name] = s
	}
	return result
}()

func Lookup(name string) (Spec, bool) { s, ok := catalog[name]; return s, ok }
func All() []Spec {
	result := make([]Spec, 0, len(catalog))
	for _, s := range catalog {
		result = append(result, s)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
func (s Spec) TypeAt(index int) string {
	if index < len(s.Types) {
		return s.Types[index]
	}
	return s.Types[len(s.Types)-1]
}
