package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// Input declares one positional CLI parameter. Libraries pass native Go values
// and validate the same types through CheckType.
type Input struct{ Name, Type string }

// StringValue is the fixed, explicit language conversion. Host adapters
// cannot redefine it or change the effect classification of a local fn.
func StringValue(value Value) string {
	text, err := FormatValue(value)
	if err == nil {
		return text
	}
	// Native host values may have a display form without a JSON representation.
	return fmt.Sprint(value)
}

// TypeName uses language types at the user boundary; native host types remain
// identifiable when they do not correspond to a LIP value.
func TypeName(value Value) string {
	if value == nil {
		return "null"
	}
	if _, ok := value.(json.Number); ok {
		return "number"
	}
	switch reflect.TypeOf(value).Kind() {
	case reflect.Bool:
		return "bool"
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "list"
	case reflect.Map:
		if reflect.TypeOf(value).Key().Kind() == reflect.String {
			return "object"
		}
	}
	return fmt.Sprintf("host value %T", value)
}

func PrintValues(writer io.Writer, values []Value) error {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = StringValue(value)
	}
	text := strings.Join(parts, " ") + "\n"
	written, err := io.WriteString(writer, text)
	if err == nil && written != len(text) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return fmt.Errorf("print: %w", err)
	}
	return nil
}

func argumentCountError(minimum, maximum, got int) error {
	expected := fmt.Sprint(minimum)
	if maximum < 0 {
		expected = fmt.Sprintf("at least %d", minimum)
	} else if maximum != minimum {
		expected = fmt.Sprintf("%d to %d", minimum, maximum)
		if maximum == minimum+1 {
			expected = fmt.Sprintf("%d or %d", minimum, maximum)
		}
	}
	noun := "arguments"
	if minimum == 1 && maximum == 1 {
		noun = "argument"
	}
	return fmt.Errorf("expects %s %s, got %d", expected, noun, got)
}

var decimalNumber = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

func ParseCLIInputs(args []string, params []Input) (map[string]Value, error) {
	if len(args) != len(params) {
		noun := "arguments"
		if len(params) == 1 {
			noun = "argument"
		}
		message := fmt.Sprintf("expected %d %s, got %d", len(params), noun, len(args))
		if len(args) < len(params) {
			missing := make([]string, 0, len(params)-len(args))
			for _, param := range params[len(args):] {
				missing = append(missing, param.Name+":"+param.Type)
			}
			message += "; missing " + strings.Join(missing, ", ")
		}
		return nil, fmt.Errorf("%s", message)
	}
	inputs := make(map[string]Value, len(params))
	for i, param := range params {
		raw := args[i]
		var value Value
		var err error
		base := strings.TrimSuffix(param.Type, "?")
		nullable := strings.HasSuffix(param.Type, "?")
		if nullable && raw == "null" {
			if err := CheckType(nil, param.Type); err != nil {
				return nil, fmt.Errorf("argument %d (%s): %w", i+1, param.Name, err)
			}
			inputs[param.Name] = nil
			continue
		}
		switch base {
		case "string":
			value = raw
			if nullable && strings.HasPrefix(raw, "\"") {
				var text string
				err = json.Unmarshal([]byte(raw), &text)
				value = text
			}
		case "number":
			var n float64
			n, err = strconv.ParseFloat(raw, 64)
			if err != nil || !decimalNumber.MatchString(raw) || math.IsNaN(n) || math.IsInf(n, 0) {
				err = fmt.Errorf("expected a finite decimal number, got %q", raw)
			}
			value = n
		case "bool":
			if raw != "true" && raw != "false" {
				err = fmt.Errorf("expected true or false, got %q", raw)
			}
			value = raw == "true"
		case "any", "list", "object":
			err = json.Unmarshal([]byte(raw), &value)
			if err != nil {
				err = fmt.Errorf("%s input must be valid JSON: %w", param.Type, err)
			}
		default:
			err = fmt.Errorf("unknown input type %q", param.Type)
		}
		if err == nil {
			err = CheckType(value, param.Type)
		}
		if err != nil {
			return nil, fmt.Errorf("argument %d (%s): %w", i+1, param.Name, err)
		}
		inputs[param.Name] = value
	}
	return inputs, nil
}

// FormatValue keeps strings human-readable and other results valid JSON.
func FormatValue(value Value) (string, error) {
	if text, ok := scalarString(value); ok {
		return text, nil
	}
	if reference, ok := value.(map[string]any); ok {
		path, named := reference["$python_ref"].(string)
		kind, described := reference["kind"].(string)
		if named && described && path != "" {
			switch kind {
			case "function", "class":
				return fmt.Sprintf("<Python %s %s; call with (...)>", kind, path), nil
			case "module":
				return fmt.Sprintf("<Python module %s>", path), nil
			}
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("output cannot be encoded as JSON: %w", err)
	}
	return string(data), nil
}

// ExecutionHint supplies actionable guidance without exposing scheduler internals.
func ExecutionHint(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	// Explicit fail messages describe the programmer's chosen failure, not a
	// kernel diagnosis. Do not infer a repair from words inside that message.
	if strings.Contains(message, "fail: ") {
		return ""
	}
	switch {
	case strings.Contains(message, "unknown Python handle"):
		return "Python object handles belong to one Worker session. In the REPL, convert values with python.to_json while still in the same cell, or use a host program with a persistent Worker."
	case strings.Contains(message, "ModuleNotFoundError") || strings.Contains(message, "No module named"):
		return "Check the actual Python module name and install its distribution in the interpreter environment used by the Worker; import does not install packages."
	case strings.Contains(message, "AttributeError") && strings.Contains(message, "has no attribute"):
		return "Check the Python attribute spelling and module version. Read attributes with module.name; use module.name(...) to call a function."
	case strings.Contains(message, "division by zero"):
		return "Check the divisor before dividing, e.g. if divisor == 0 { null } else { value / divisor }."
	case strings.Contains(message, "index") && strings.Contains(message, "out of range"):
		return "Indices are integers starting at 0; require index >= 0 && index < len(value) before reading an element."
	case strings.Contains(message, "modulo by zero"):
		return "Use a nonzero divisor for %, e.g. if divisor == 0 { null } else { value % divisor }."
	case strings.Contains(message, "logarithm argument must be positive"):
		return "Use a positive left operand for x */ base; zero and negative values have no real logarithm."
	case strings.Contains(message, "logarithm base must be positive"):
		return "Use a positive right operand different from 1 for x */ base."
	case strings.Contains(message, "index") && (strings.Contains(message, "integer") || strings.Contains(message, "nonnegative")):
		return "Use a nonnegative integer index; negative and fractional indices are not supported."
	case strings.Contains(message, "missing field") || strings.Contains(message, "missing map key"):
		return "Check the requested key in the actual object; an absent key is different from a key containing null."
	case strings.Contains(message, "cannot read field") && strings.Contains(message, "null"):
		return "Check that the object is not null before reading its field."
	case strings.Contains(message, "range step cannot be zero"):
		return "Use a positive step for an increasing range or a negative step for a decreasing range."
	case strings.Contains(message, "range argument") && strings.Contains(message, "safe integer"):
		return "range bounds and step must be integers between -9007199254740991 and 9007199254740991."
	case strings.Contains(message, "sort key"):
		return "Use keys that are all numbers or all strings. For records, select one comparable field with list.sort_by(rows, fn(row) { row.field })."
	case strings.Contains(message, "needs a nonempty list") || strings.Contains(message, "first/last need a nonempty list"):
		return "Check len(values) == 0 before selecting first, last, min or max; choose an explicit fallback for empty input."
	case strings.Contains(message, "split separator must be nonempty"):
		return "Use a nonempty separator with string.split; use string.chars(text) to split into Unicode characters."
	case strings.Contains(message, "pattern must be nonempty"):
		return "Pass a nonempty search pattern."
	case strings.Contains(message, "expected number, got string"):
		return "Use string.parse_number(text) when the string contains numeric text; str converts in the other direction."
	case strings.Contains(message, "expected a finite decimal number"):
		return "Use finite decimal text such as 0.4 or 1.2e3; empty text, NaN and infinity are not numbers."
	case strings.Contains(message, "expected bool, got"):
		return "Use a bool value or an explicit comparison, e.g. value != 0; numbers and strings are not implicitly booleans."
	case strings.Contains(message, "expected number, got null"):
		return "Check why the input is null and handle the missing value before arithmetic."
	case strings.Contains(message, "expects two strings"):
		return "Use two numbers for arithmetic or two strings for concatenation; use str(value) when text conversion is intended."
	case strings.Contains(message, "call depth exceeds"):
		return "Add a terminating base case to the recursive function; use Map or fold for list traversal."
	case strings.Contains(message, "host operation") && strings.Contains(message, "not registered"):
		return "Register the operation in the Go host; an import declaration does not provide its implementation."
	default:
		return ""
	}
}

// ResolveValue is used for synchronous composition of pure local expressions.
// It observes the same cancellation and future semantics as a graph node.
func ResolveValue(ctx context.Context, result Result) (Value, error) {
	resolved, err := awaitResult(ctx, result)
	if err != nil {
		return nil, err
	}
	if resolved.Err != nil {
		return nil, resolved.Err
	}
	return resolved.Value, nil
}

func awaitNodeResult(ctx context.Context, node NodeSpec, result Result) (Result, error) {
	resolved, err := awaitResult(ctx, result)
	if err != nil || resolved.Err != nil {
		return resolved, err
	}
	if err := CheckType(resolved.Value, node.ValueType); err != nil {
		return Result{}, fmt.Errorf("value: %w", err)
	}
	return resolved, nil
}
