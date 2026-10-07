package compiler

import (
	"fmt"
	"lipalpha/compiler/ast"
	"lipalpha/internal/listops"
	"lipalpha/internal/stringops"
)

// Collection operations remain expressions, with no hidden Host capability.
func inferCollectionCall(call *ast.CallExpr, env, fnTypes map[string]string, fnParams map[string][]string) (string, error) {
	args := call.Args
	switch call.Name {
	case "len":
		if len(args) != 1 {
			return "any", fmt.Errorf("len expects 1 argument, got %d", len(args))
		}
		typ, err := inferExprType(args[0], env, fnTypes, fnParams)
		if err != nil {
			return "any", err
		}
		if typ != "never" && typ != "any" && typ != "string" && typ != "list" && typ != "object" {
			return "any", fmt.Errorf("len expects string, list or object, got %s", typ)
		}
		return "number", nil
	case "range":
		if len(args) != 2 && len(args) != 3 {
			return "any", fmt.Errorf("range expects 2 or 3 arguments, got %d", len(args))
		}
		for _, arg := range args {
			typ, err := inferExprType(arg, env, fnTypes, fnParams)
			if err != nil {
				return "any", err
			}
			if !compatibleType("number", typ) {
				return "any", fmt.Errorf("range expects numbers, got %s", typ)
			}
		}
		return "list", nil
	case "fold":
		if len(args) != 3 {
			return "any", fmt.Errorf("fold expects 3 arguments, got %d", len(args))
		}
		reducer, ok := args[2].(*ast.IdentExpr)
		if !ok {
			return "any", fmt.Errorf("fold reducer must name a local pure function")
		}
		params, ok := fnParams[reducer.Name]
		if !ok || len(params) != 2 {
			return "any", fmt.Errorf("fold reducer %q must be a local pure function with 2 parameters", reducer.Name)
		}
		source, err := inferExprType(args[0], env, fnTypes, fnParams)
		if err != nil {
			return "any", err
		}
		if !compatibleType("list", source) {
			return "any", fmt.Errorf("fold source must be a list, got %s", source)
		}
		seed, err := inferExprType(args[1], env, fnTypes, fnParams)
		if err != nil {
			return "any", err
		}
		if !compatibleType(params[0], seed) {
			return "any", fmt.Errorf("fold reducer %s expects accumulator %s, seed is %s", reducer.Name, params[0], seed)
		}
		result := fnTypes[reducer.Name]
		if result == "" {
			result = "any"
		}
		if !compatibleType(params[0], result) {
			return "any", fmt.Errorf("fold reducer %s returns %s, accumulator requires %s", reducer.Name, result, params[0])
		}
		// The result includes the seed because the source can be empty.
		if result == seed {
			return seed, nil
		}
		return "any", nil
	}
	return "any", fmt.Errorf("unknown collection operation %q", call.Name)
}

func inferStringCall(call *ast.CallExpr, env, fnTypes map[string]string, fnParams map[string][]string) (string, error) {
	spec, _ := stringops.Lookup(call.Name)
	if len(call.Args) < spec.MinArgs || len(call.Args) > spec.MaxArgs {
		return "any", fmt.Errorf("%s has invalid argument count: got %d", call.Name, len(call.Args))
	}
	for index, arg := range call.Args {
		actual, err := inferExprType(arg, env, fnTypes, fnParams)
		if err != nil {
			return "any", err
		}
		if !compatibleType(spec.Types[index], actual) {
			return "any", fmt.Errorf("%s argument %d expects %s, got %s", call.Name, index+1, spec.Types[index], actual)
		}
	}
	return spec.Result, nil
}

func callbackIndex(call *ast.CallExpr) int {
	if call.Name == "fold" && len(call.Args) == 3 {
		return 2
	}
	if spec, ok := listops.Lookup(call.Name); ok && spec.Callback >= 0 && spec.Callback < len(call.Args) {
		return spec.Callback
	}
	return -1
}

func inferListCall(call *ast.CallExpr, env, fnTypes map[string]string, fnParams map[string][]string) (string, error) {
	spec, _ := listops.Lookup(call.Name)
	if len(call.Args) < spec.MinArgs || spec.MaxArgs >= 0 && len(call.Args) > spec.MaxArgs {
		return "any", fmt.Errorf("%s has invalid argument count: got %d", call.Name, len(call.Args))
	}
	for index, arg := range call.Args {
		if index == spec.Callback {
			fn, ok := arg.(*ast.IdentExpr)
			if !ok {
				return "any", fmt.Errorf("%s callback must name a local pure function", call.Name)
			}
			params, ok := fnParams[fn.Name]
			if !ok || len(params) != spec.CallbackArity {
				return "any", fmt.Errorf("%s callback must be a local pure function with %d parameters", call.Name, spec.CallbackArity)
			}
			result := fnTypes[fn.Name]
			if result == "" {
				result = "any"
			}
			if !compatibleType(spec.CallbackResult, result) {
				return "any", fmt.Errorf("%s callback must return %s, got %s", call.Name, spec.CallbackResult, result)
			}
			if call.Name == "list.scan" {
				seed, err := inferExprType(call.Args[1], env, fnTypes, fnParams)
				if err != nil {
					return "any", err
				}
				if !compatibleType(params[0], seed) || !compatibleType(params[0], result) {
					return "any", fmt.Errorf("list.scan seed and reducer result must match the accumulator type")
				}
			}
			continue
		}
		actual, err := inferExprType(arg, env, fnTypes, fnParams)
		if err != nil {
			return "any", err
		}
		if !compatibleType(spec.TypeAt(index), actual) {
			return "any", fmt.Errorf("%s argument %d expects %s, got %s", call.Name, index+1, spec.TypeAt(index), actual)
		}
	}
	return spec.Result, nil
}
