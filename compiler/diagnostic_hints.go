package compiler

import (
	"errors"
	"os"
	"regexp"
	"strings"
)

var functionOutputMismatch = regexp.MustCompile(`function \S+ returns \S+, declared \S+`)
var callArgumentMismatch = regexp.MustCompile(`argument [0-9]+ expects (\S+), got (\S+)`)
var callbackProblem = regexp.MustCompile(`(?:list\.[a-z_]+ callback|fold reducer|list\.scan (?:seed|reducer))`)
var feedbackProblem = regexp.MustCompile(`(?:^|: )feedback `)
var retryProblem = regexp.MustCompile(`(?:^|: )retry `)

func diagnosticAdvice(stage string, err error) (string, []string) {
	var attached *diagnosticError
	if errors.As(err, &attached) {
		return attached.code, append([]string{}, attached.hints...)
	}
	message := err.Error()
	has := func(text string) bool { return strings.Contains(message, text) }
	advice := func(code string, hints ...string) (string, []string) { return code, hints }
	if stage == "LIP_IO_ERROR" {
		switch {
		case errors.Is(err, os.ErrNotExist):
			return advice(stage, "Check the source path; the file does not exist.")
		case errors.Is(err, os.ErrPermission):
			return advice(stage, "Check the source file's read permissions.")
		default:
			return advice(stage, "Check the source path and the operating-system error shown above.")
		}
	}
	if stage == "LIP_LEX_ERROR" {
		switch {
		case has("unexpected character '#'"):
			return advice(stage, "Use // for line comments.")
		case has("unterminated string"):
			return advice(stage, `Close the string with a double quote. To include a newline in text, use \n.`)
		case has("newline in string"):
			return advice(stage, `Use \n inside a double-quoted string for a newline; literal line breaks cannot appear in a string.`)
		case has("invalid string"):
			return advice(stage, `Use valid escapes inside double quotes, such as \n, \t, \", \\ or \u4F60.`)
		case has("invalid finite number"):
			return advice(stage, "Use a finite decimal number with ASCII digits, such as 0.4 or 1.2e3; an exponent needs digits.")
		case has(`unexpected character '\''`):
			return advice(stage, "Use double quotes for string literals.")
		default:
			return advice(stage, "Remove or replace the unexpected character shown at the caret.")
		}
	}
	switch {
	case has("REPL cells"):
		return advice(stage, "Enter expressions, bindings or fn declarations directly. Run a complete Flow program with lipc run file.lip.")
	case has("only the final expression in a REPL cell"):
		return advice(stage, "Assign earlier expressions to names, or print them explicitly; the final expression is displayed automatically.")
	case has("same line must be separated"):
		return advice(stage, "Write a = 2; b = 3; print(a / b), or put each statement on its own line. Spaces alone do not separate statements.")
	case has("mixed with top-level statements"):
		return advice(stage, "Use one explicit flow or top-level executable statements per file. Move statements inside the flow, or remove its wrapper.")
	case has("must precede top-level statements"):
		return advice(stage, "Place import declarations first, fn declarations next, then the executable statements.")
	case has("has been replaced"):
		return advice("LIP_DEPENDENCY_ERROR", `Use import python "numpy" as np, import host "fetch" or import go "example.com/adapter".`)
	case has("aliases are supported by import python") || has("aliases are only supported by import python"):
		return advice("LIP_DEPENDENCY_ERROR", "Use as with Python modules only. Host/Go operations use their registered Host names.")
	case has("module alias") || has("Python module") && has("not a function"):
		switch {
		case has("not a function"):
			return advice("LIP_NAME_ERROR", "Call an operation inside the imported module; a module name cannot be called as a function.")
		case has("conflicts") || has("duplicate"):
			return advice("LIP_NAME_ERROR", "Choose a distinct alias or rename the conflicting declaration; one local name must have one meaning.")
		case has("reserved"):
			return advice("LIP_NAME_ERROR", "Choose an alias that is not a built-in, a type name or a generated-name prefix, e.g. np or ml.")
		default:
			return advice("LIP_NAME_ERROR", `Choose one identifier after as, e.g. import python "numpy" as np.`)
		}
	case has("invalid python dependency"):
		if has(`"scikit-learn"`) {
			return advice("LIP_DEPENDENCY_ERROR", `Use import python "sklearn" as ml. sklearn is the module name; scikit-learn is the installation name.`)
		}
		return advice("LIP_DEPENDENCY_ERROR", `Use the actual Python module name, optionally followed by version constraints, e.g. import python "numpy>=1.26" as np. Dotted module paths are supported.`)
	case has("invalid host dependency"):
		return advice("LIP_DEPENDENCY_ERROR", `Use a registered operation name or namespace wildcard, e.g. import host "fetch" or import host "service.*".`)
	case has("invalid go dependency"):
		return advice("LIP_DEPENDENCY_ERROR", `Use a Go module path without spaces or a version constraint, e.g. import go "example.com/adapter".`)
	case has("unknown dependency kind"):
		return advice("LIP_DEPENDENCY_ERROR", "Choose python, host or go after import.")
	case has("dependency spec cannot be empty"):
		return advice("LIP_DEPENDENCY_ERROR", "Put the Python module, Host operation or Go module path inside the quotes.")
	case has("duplicate") && has("dependency"):
		return advice("LIP_DEPENDENCY_ERROR", "Remove the repeated import declaration.")
	case has("duplicate object key"):
		return advice(stage, "Each object key must be unique; remove the repeated key or give it a different name.")
	case has("duplicate parameter"):
		return advice("LIP_NAME_ERROR", "Use a distinct name for each parameter in the declaration.")
	case has("duplicate function"):
		return advice("LIP_NAME_ERROR", "Use one declaration per function name; rename or remove the repeated function.")
	case has("duplicate binding"):
		return advice(stage, "Bindings are immutable. Use a new name; in the REPL, :reset clears existing bindings.")
	case has("reserved"):
		return advice("LIP_NAME_ERROR", "Choose a name that is not reserved for built-ins or generated graph nodes.")
	case has("expression alone"):
		return advice(stage, "Print the value with print(expression), save it with name = expression, or use lipc repl to display expressions automatically.")
	case has("empty statement"):
		return advice(stage, "Use a single semicolon between statements; the final semicolon is optional.")
	case has("expected ,"):
		return advice(stage, "Separate parameters, call arguments, list elements and object fields with commas; a trailing comma is allowed. Semicolons separate statements only.")
	case has("expected else"):
		return advice(stage, "An if expression needs both values: if condition { value } else { other_value }. For conditional effects, use when condition { print(value) }.")
	case has("object key must be"):
		return advice(stage, `Use a name or a quoted key followed by a colon, e.g. {name: "Ada", "two words": 2}.`)
	case has("expected :"):
		return advice(stage, "Separate an object key from its value with a colon: {key: value}.")
	case has("expected string after import"):
		return advice("LIP_DEPENDENCY_ERROR", `Put the module or operation name in double quotes, e.g. import python "math".`)
	case has("fn body needs"):
		return advice(stage, "A pure fn has one result expression, e.g. fn twice(n: number) { n * 2 }; return is optional. Put effectful statements in the Flow.")
	case has("output type needs"):
		return advice(stage, "Put -> before a returned type. A Flow with effects only can omit the output declaration.")
	case has("void cannot be optional"):
		return advice("LIP_TYPE_ERROR", "Omit the Flow output declaration when there is no result. Use -> number? (or another value type) for an optional result.")
	case has("unknown type") || has("unknown return type"):
		return advice("LIP_TYPE_ERROR", "Value types are number, bool, string, list, object and any. void is allowed only as a Flow output; a Flow without a result can omit its output declaration.")
	case has("needs an explicit parameter list"):
		return advice(stage, "Put () after the Flow name when there are no inputs: flow main() { print(79 / 134) }.")
	case has("needs an explicit type"):
		return advice(stage, "Annotate the parameter, e.g. value: number or value: any. Output declarations do not replace input annotations.")
	case has("explicit output type"):
		return advice("LIP_RETURN_ERROR", "Declare the returned type, e.g. flow main() -> number { return 79 / 134 }. For effects only, omit the output declaration and return.")
	case has("no return value") || has("return needs a value"):
		return advice("LIP_RETURN_ERROR", "Add a final return with a value matching the declared output. For effects only, omit the output type and return.")
	case functionOutputMismatch.MatchString(message):
		return advice("LIP_TYPE_ERROR", "Match the fn output annotation to its returned expression; a pure fn produces a value.")
	case has("return has type"):
		return advice("LIP_TYPE_ERROR", "Make the returned value and the declared output type agree. For a Flow with effects only, omit the output declaration and return.")
	case has("gated return"):
		return advice("LIP_RETURN_ERROR", "A return inside when can be skipped. Declare an optional output such as -> number?, or return an if expression outside when.")
	case has("statements after return"):
		return advice("LIP_RETURN_ERROR", "Move executable statements before the final return. return finishes this block; it does not cancel earlier graph nodes.")
	case has("exactly one return"):
		return advice("LIP_RETURN_ERROR", "Use one final return; select a conditional value with if condition { value } else { other_value }.")
	case has("at most one bare return"):
		return advice("LIP_RETURN_ERROR", "A Flow without a result needs no return. Remove repeated bare returns; when gates are not early-exit control flow.")
	case has("scoped to a when block"):
		return advice("LIP_NAME_ERROR", "A when-local binding is available only inside that block. Move the use into the block, or define the needed value outside it.")
	case has("undefined or forward reference"):
		return advice("LIP_NAME_ERROR", "Check the spelling and define the binding before use. A forward reference is not a saved value.")
	case has("unknown function parameter"):
		return advice("LIP_NAME_ERROR", "A pure fn uses its parameters, not Flow bindings. Add the value as a typed parameter and pass it at the call site.")
	case has("recursive function"):
		return advice("LIP_RECURSION_ERROR", "Declare a concrete output type on every function in the recursive cycle, e.g. -> number, and include a terminating base case.")
	case has("callback must be pure"):
		return advice("LIP_EFFECT_ERROR", "Run external operations in the Flow first, then use their saved values in the callback. Inline callbacks may capture immutable Flow bindings; keep IO and print outside the callback.")
	case has("must be pure"):
		return advice("LIP_EFFECT_ERROR", "Keep external calls and print in the Flow. Pass their results into a pure fn as parameters.")
	case has("nested external call") && has("comprehension source"):
		return advice("LIP_EFFECT_ERROR", "Bind the external source in the Flow first, e.g. values = fetch(); mapped = [x * 2 for x in values]. A comprehension source may directly combine pure expressions such as range(1, 19).")
	case has("nested external call") && has("comprehension element"):
		return advice("LIP_EFFECT_ERROR", "Keep external calls as standalone Flow or Map operations. For an effectful comprehension, bind it first (mapped = [fetch(x) for x in values]) and pass mapped to the surrounding call.")
	case has("nested external call"):
		return advice("LIP_EFFECT_ERROR", "Bind the external call first, then pass its saved value to the surrounding call. Pure expressions may be nested; external operations are separate Flow nodes.")
	case has("state") && (has("Flow binding") || has("initial value")):
		return advice("LIP_EFFECT_ERROR", "Declare state as a binding with one initial value, e.g. counter = state(0). State updates use the host's Instance API.")
	case has("control operations") || has("only supported as a Flow node"):
		return advice("LIP_EFFECT_ERROR", "Place retry or feedback as a separate Flow node; do not nest control operations inside fn, Map or call arguments.")
	case has("source must be a list"):
		return advice("LIP_TYPE_ERROR", "Pass a list as the source; use range(start, end) to construct a numeric sequence.")
	case callbackProblem.MatchString(message):
		switch {
		case has("must name") || has("must be a local pure") || has("needs a local pure"):
			return advice("LIP_CALLBACK_ERROR", "Pass a local pure fn name or inline fn(x) { ... }, with the required parameter count; do not call a named function at the callback position.")
		case has("must return"):
			return advice("LIP_CALLBACK_ERROR", "Make the callback's return value match the required result type shown above.")
		case has("accumulator") || has("seed"):
			return advice("LIP_CALLBACK_ERROR", "Make the seed and each reducer return value compatible with the accumulator type shown above. Annotate the inline accumulator parameter when a fixed type is needed.")
		}
	case feedbackProblem.MatchString(message):
		switch {
		case has("attempt count"):
			return advice("LIP_ARGUMENT_ERROR", "Use a positive integer literal for the attempt limit, e.g. feedback(initial(), step, verify, 3).")
		case has("must return bool"):
			return advice("LIP_TYPE_ERROR", "The verifier takes one candidate and returns true or false.")
		case has("candidate is"):
			return advice("LIP_TYPE_ERROR", "The initial result, step input/result and verifier input must agree on the candidate type.")
		default:
			return advice("LIP_ARGUMENT_ERROR", "Use feedback(initial_call(), step, verify, positive_integer). step and verify are operation names and each takes one candidate.")
		}
	case retryProblem.MatchString(message):
		if has("attempt count") {
			return advice("LIP_ARGUMENT_ERROR", "Use a positive integer literal for the attempt limit, e.g. retry(fetch(), 3).")
		}
		return advice("LIP_ARGUMENT_ERROR", "Use retry(operation_call(), positive_integer); the first argument is an ordinary call, not a value or another control operation.")
	case has("condition must be bool") || has("operator ! expects bool") || has("expects booleans"):
		return advice("LIP_TYPE_ERROR", "Use a bool expression, e.g. value != 0. Numbers and strings are not implicitly treated as true or false.")
	case has("if branches have incompatible types"):
		return advice("LIP_TYPE_ERROR", "Return compatible types from both if branches. null is allowed with a value for an optional result.")
	case has("operator + cannot combine"):
		if has("string") {
			return advice("LIP_TYPE_ERROR", `For text, explicitly convert the other value: "count=" + str(count). For arithmetic, use numbers on both sides.`)
		}
		return advice("LIP_TYPE_ERROR", "Use two numbers for addition or two strings for concatenation.")
	case has("cannot compare"):
		return advice("LIP_TYPE_ERROR", "Compare values of compatible types; check the two operand types shown above.")
	case has("expects numbers"):
		if has("string") {
			return advice("LIP_TYPE_ERROR", "Use numeric operands; convert numeric text explicitly with string.parse_number(text).")
		}
		return advice("LIP_TYPE_ERROR", "Use numeric operands; bool, list and object values cannot be used as numbers.")
	case callArgumentMismatch.MatchString(message):
		match := callArgumentMismatch.FindStringSubmatch(message)
		if match[1] == "number" && match[2] == "string" {
			return advice("LIP_TYPE_ERROR", "The indicated argument needs a number. Use string.parse_number(text) if the string contains numeric text.")
		}
		return advice("LIP_TYPE_ERROR", "Make the indicated argument match its parameter type; changing a different argument or the output type does not fix this call.")
	case has("expects string") || has("len expects") || has("fail expects"):
		return advice("LIP_TYPE_ERROR", "Pass a value of the type shown in the operation error.")
	case has("not declared") || has("needs an explicit import"):
		if has("Host operation") {
			return advice("LIP_DEPENDENCY_ERROR", "Check the operation spelling first. If it is a Host operation, add the import shown above and register that name in the Go host; import does not implement it.")
		}
		return advice("LIP_DEPENDENCY_ERROR", "Check the module or alias spelling first. Declare the actual installed Python module with import python; declarations do not install dependencies.")
	}
	if stage == "LIP_SYNTAX_ERROR" {
		return advice(stage, "Check the expected token shown above at the caret; finish the expression or close its matching delimiter.")
	}
	return stage, []string{}
}
