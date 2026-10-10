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
	Callback         int // -1 means none; otherwise the source argument position.
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

func sortBy() Spec {
	s := callback("sort_by", "list", 1, "any", "list")
	s.MaxArgs = 3
	s.Types = append(s.Types, "bool")
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
		value("count", "number", 2, 2, "list", "any"), value("sort", "list", 1, 2, "list", "bool"), value("group", "list", 1, 1, "list"),
		value("sum", "number", 1, 1, "list"), value("product", "number", 1, 1, "list"),
		value("min", "any", 1, 1, "list"), value("max", "any", 1, 1, "list"), value("first", "any", 1, 1, "list"), value("last", "any", 1, 1, "list"),
		callback("map", "list", 1, "any", "list"), callback("filter", "list", 1, "bool", "list"),
		callback("group_by", "list", 1, "any", "list"), callback("split_by", "list", 1, "any", "list"), sortBy(), callback("sort_with", "list", 2, "bool", "list"),
		callback("any", "bool", 1, "bool", "list"), callback("all", "bool", 1, "bool", "list"), callback("fold", "any", 2, "any", "list", "any"), callback("scan", "list", 2, "any", "list", "any"),
	}
	result := map[string]Spec{}
	for _, s := range entries {
		result[s.Name] = s
	}
	return result
}()

func Lookup(name string) (Spec, bool) {
	if s, ok := catalog[name]; ok {
		return s, true
	}
	// Keep the namespaced form canonical while allowing the short spelling in
	// interactive use and small scripts.
	switch name {
	case "sort":
		return catalog["list.sort"], true
	case "sort_by":
		return catalog["list.sort_by"], true
	case "sort_with":
		return catalog["list.sort_with"], true
	case "fold":
		return catalog["list.fold"], true
	}
	return Spec{}, false
}
func All() []Spec {
	result := make([]Spec, 0, len(catalog))
	for _, s := range catalog {
		result = append(result, s)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// Aliases are the short spellings accepted by the compiler and Runtime API.
// Canonical list.* names remain the entries returned by All.
func Aliases() []string { return []string{"sort", "sort_by", "sort_with"} }
func (s Spec) TypeAt(index int) string {
	if index < len(s.Types) {
		return s.Types[index]
	}
	return s.Types[len(s.Types)-1]
}
