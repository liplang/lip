package compiler

import (
	"fmt"
	"strings"

	"lipalpha/compiler/ast"
)

func callbackError(operation string, arity int, message string) error {
	example := "fn(x) { x * 2 }"
	if operation == "list.filter" || operation == "list.any" || operation == "list.all" {
		example = "fn(x) { x > 0 }"
	}
	if arity == 2 {
		example = "fn(total, x) { total + x }"
	}
	return repairError("LIP_CALLBACK_ERROR", message, fmt.Sprintf("Pass a local pure function name or an inline callback such as %s to %s. The callback is passed without calling it.", example, operation))
}

func callbackTypeError(message string) error {
	hint := "Match the callback's return value to the required type shown above."
	if strings.Contains(message, "must return bool") {
		hint += " Return a bool or a comparison such as fn(x) { x > 0 }."
	} else if strings.Contains(message, "sort_by") {
		hint += " Select a number or string key, e.g. fn(row) { row.amount }."
	}
	return repairError("LIP_CALLBACK_ERROR", message, hint)
}

func validateLambda(fn *ast.LambdaExpr) error {
	seen := map[string]bool{}
	for _, param := range fn.Params {
		if reservedName(param) {
			return fmt.Errorf("callback parameter %q is reserved for generated graph nodes", param)
		}
		if seen[param] {
			return repairError("LIP_NAME_ERROR", fmt.Sprintf("duplicate callback parameter %q", param), "Use a distinct name for each callback parameter; for a reducer, use fn(total, x) { ... }.")
		}
		seen[param] = true
		if !validTypeName(paramType(fn.ParamTypes, param)) {
			return fmt.Errorf("unknown callback parameter type %q", fn.ParamTypes[param])
		}
	}
	if fn.ReturnType != "" && !validTypeName(fn.ReturnType) {
		return fmt.Errorf("unknown callback return type %q", fn.ReturnType)
	}
	return validateSimpleExpr(fn.Return)
}

func validateCallArguments(call *ast.CallExpr) error {
	callback := callbackIndex(call)
	for index, arg := range call.Args {
		if fn, ok := arg.(*ast.LambdaExpr); ok && index == callback {
			if err := validateLambda(fn); err != nil {
				return fmt.Errorf("%s callback: %w", call.Name, err)
			}
			continue
		}
		if err := validateSimpleExpr(arg); err != nil {
			return fmt.Errorf("call argument: %w", err)
		}
	}
	return nil
}

// Named callbacks are closed local functions; inline callbacks additionally
// capture immutable values from the current expression's lexical scope.
func inferCallback(operation string, arg ast.Expr, arity int, env, fnTypes map[string]string, fnParams map[string][]string) ([]string, string, error) {
	var params []string
	result := "any"
	switch fn := arg.(type) {
	case *ast.IdentExpr:
		var exists bool
		params, exists = fnParams[fn.Name]
		if !exists {
			return nil, "any", callbackError(operation, arity, fmt.Sprintf("%s callback %q must be a local pure function or inline fn", operation, fn.Name))
		}
		if fnTypes[fn.Name] != "" {
			result = fnTypes[fn.Name]
		}
	case *ast.LambdaExpr:
		if err := validateLambda(fn); err != nil {
			return nil, "any", err
		}
		params = make([]string, len(fn.Params))
		for index, param := range fn.Params {
			params[index] = paramType(fn.ParamTypes, param)
		}
		if len(params) == arity {
			scope := cloneStringMap(env)
			for index, param := range fn.Params {
				scope[param] = params[index]
			}
			actual, err := inferExprType(fn.Return, scope, fnTypes, fnParams)
			if err != nil {
				return nil, "any", fmt.Errorf("%s callback: %w", operation, err)
			}
			result = actual
			fn.InferredReturnType = actual
			if fn.ReturnType != "" {
				if !compatibleType(fn.ReturnType, actual) {
					return nil, "any", callbackTypeError(fmt.Sprintf("%s callback returns %s, declared %s", operation, actual, fn.ReturnType))
				}
				if err := validateDeclaredResult(fn.Return, fn.ReturnType, scope, fnTypes, fnParams); err != nil {
					return nil, "any", callbackTypeError(fmt.Sprintf("%s callback: %v", operation, err))
				}
				result = fn.ReturnType
			}
		}
	default:
		return nil, "any", callbackError(operation, arity, fmt.Sprintf("%s callback must be a local pure function or inline fn", operation))
	}
	if len(params) != arity {
		noun := "parameters"
		if arity == 1 {
			noun = "parameter"
		}
		return nil, "any", callbackError(operation, arity, fmt.Sprintf("%s callback expects %d %s, got %d", operation, arity, noun, len(params)))
	}
	return params, result, nil
}
