package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// Input declares one positional CLI parameter. Libraries pass native Go values
// and validate the same types through CheckType.
type Input struct{ Name, Type string }

// StringValue is the fixed, explicit language conversion. Host adapters
// cannot redefine it or change the effect classification of a local fn.
func StringValue(value Value) string { return fmt.Sprint(value) }

var decimalNumber = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

func ParseCLIInputs(args []string, params []Input) (map[string]Value, error) {
	if len(args) != len(params) {
		return nil, fmt.Errorf("expected %d arguments, got %d", len(params), len(args))
	}
	inputs := make(map[string]Value, len(params))
	for i, param := range params {
		raw := args[i]
		var value Value
		var err error
		switch param.Type {
		case "string":
			value = raw
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
		case "any":
			err = json.Unmarshal([]byte(raw), &value)
			if err != nil {
				err = fmt.Errorf("any input must be valid JSON: %w", err)
			}
		default:
			err = fmt.Errorf("unknown input type %q", param.Type)
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
	if text, ok := value.(string); ok {
		return text, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("output cannot be encoded as JSON: %w", err)
	}
	return string(data), nil
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
