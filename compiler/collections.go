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
			return "any", argumentCountError("len", 1, 1, len(args))
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
		if len(args) < 1 || len(args) > 3 {
			return "any", argumentCountError("range", 1, 3, len(args))
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
			return "any", argumentCountError("fold", 3, 3, len(args))
		}
		params, result, err := inferCallback("fold", args[2], 2, env, fnTypes, fnParams)
		if err != nil {
			return "any", err
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
			return "any", fmt.Errorf("fold reducer expects accumulator %s, seed is %s", params[0], seed)
		}
		if !compatibleType(params[0], result) {
			return "any", fmt.Errorf("fold reducer returns %s, accumulator requires %s", result, params[0])
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
		return "any", argumentCountError(call.Name, spec.MinArgs, spec.MaxArgs, len(call.Args))
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
	if externalCall(call) {
		return -1
	}
	if call.Name == "fold" && len(call.Args) == 3 {
		return 2
	}
	if spec, ok := listops.Lookup(call.Name); ok && spec.Callback >= 0 && spec.Callback < len(call.Args) {
		return spec.Callback
	}
	return -1
}

func callbackArity(call *ast.CallExpr) int {
	if call.Name == "fold" {
		return 2
	}
	spec, _ := listops.Lookup(call.Name)
	return spec.CallbackArity
}

func inferListCall(call *ast.CallExpr, env, fnTypes map[string]string, fnParams map[string][]string) (string, error) {
	spec, _ := listops.Lookup(call.Name)
	if len(call.Args) < spec.MinArgs || spec.MaxArgs >= 0 && len(call.Args) > spec.MaxArgs {
		return "any", argumentCountError(call.Name, spec.MinArgs, spec.MaxArgs, len(call.Args))
	}
	for index, arg := range call.Args {
		if index == spec.Callback {
			params, result, err := inferCallback(call.Name, arg, spec.CallbackArity, env, fnTypes, fnParams)
			if err != nil {
				return "any", err
			}
			if !compatibleType(spec.CallbackResult, result) {
				return "any", callbackTypeError(fmt.Sprintf("%s callback must return %s, got %s", call.Name, spec.CallbackResult, result))
			}
			if call.Name == "list.sort_by" && result != "any" && result != "never" && result != "number" && result != "string" {
				return "any", callbackTypeError(fmt.Sprintf("list.sort_by callback must return number or string, got %s", result))
			}
			if call.Name == "list.scan" {
				seed, err := inferExprType(call.Args[1], env, fnTypes, fnParams)
				if err != nil {
					return "any", err
				}
				if !compatibleType(params[0], seed) {
					return "any", fmt.Errorf("list.scan seed has type %s, accumulator type is %s", seed, params[0])
				}
				if !compatibleType(params[0], result) {
					return "any", fmt.Errorf("list.scan reducer result has type %s, accumulator type is %s", result, params[0])
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

// Check collection signatures before visiting their arguments so an extra or
// missing argument does not produce a misleading callback/dependency error.
func validateCollectionCallShape(call *ast.CallExpr) error {
	if externalCall(call) {
		return nil
	}
	minimum, maximum := -1, -1
	if spec, ok := listops.Lookup(call.Name); ok {
		minimum, maximum = spec.MinArgs, spec.MaxArgs
	} else if spec, ok := stringops.Lookup(call.Name); ok {
		minimum, maximum = spec.MinArgs, spec.MaxArgs
	} else {
		switch call.Name {
		case "range":
			minimum, maximum = 1, 3
		case "fold":
			minimum, maximum = 3, 3
		case "len", "str", "fail":
			minimum, maximum = 1, 1
		}
	}
	if minimum >= 0 && (len(call.Args) < minimum || maximum >= 0 && len(call.Args) > maximum) {
		return argumentCountError(call.Name, minimum, maximum, len(call.Args))
	}
	if index := callbackIndex(call); index >= 0 {
		switch call.Args[index].(type) {
		case *ast.IdentExpr, *ast.LambdaExpr:
		default:
			return callbackError(call.Name, callbackArity(call), fmt.Sprintf("%s callback must be a local pure function name or inline fn", call.Name))
		}
	}
	return nil
}
