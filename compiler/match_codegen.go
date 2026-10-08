package compiler

import "lipalpha/compiler/ast"

func (e *valueEmitter) matchValue(expr *ast.MatchExpr) (string, error) {
	scrutinee, err := e.value(expr.Value)
	if err != nil {
		return "", err
	}
	// A wildcard-only match still evaluates its scrutinee exactly once.
	e.line("_ = %s", scrutinee)
	result, matched := e.temp(), e.temp()
	e.line("var %s runtime.Value", result)
	e.line("%s := false", matched)
	for _, arm := range expr.Arms {
		e.line("if !%s {", matched)
		branch := e.child(e.indent + "\t")
		if arm.Pattern != nil {
			comparison := branch.temp()
			branch.line("%s, err := runtime.Binary(\"==\", %s, %s)", comparison, scrutinee, literal(arm.Pattern.Value))
			branch.line("if err != nil { return runtime.Failed(err) }")
			condition := branch.temp()
			branch.line("%s, err := runtime.Bool(%s)", condition, comparison)
			branch.line("if err != nil { return runtime.Failed(err) }")
			branch.line("if %s {", condition)
			branch = branch.child(branch.indent + "\t")
		}
		if arm.Guard != nil {
			guard, err := branch.value(arm.Guard)
			if err != nil {
				return "", err
			}
			condition := branch.temp()
			branch.line("%s, err := runtime.Bool(%s)", condition, guard)
			branch.line("if err != nil { return runtime.Failed(err) }")
			branch.line("if %s {", condition)
			branch = branch.child(branch.indent + "\t")
		}
		value, err := branch.value(arm.Expr)
		if err != nil {
			return "", err
		}
		branch.line("%s = %s", result, value)
		branch.line("%s = true", matched)
		if arm.Guard != nil {
			e.line("}")
		}
		if arm.Pattern != nil {
			e.line("}")
		}
		e.line("}")
	}
	e.line("if !%s { return runtime.Failed(fmt.Errorf(\"non-exhaustive match\")) }", matched)
	return result, nil
}
