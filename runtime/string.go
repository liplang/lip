package runtime

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"lipalpha/internal/stringops"
)

// The library bounds input and output bytes before allocating expanded results.
// Character positions still count Unicode scalar values, never bytes.
const MaxStringBytes = 16 * 1024 * 1024

func stringInput(value Value) (string, error) {
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("expects string, got %s", TypeName(value))
	}
	if len(text) > MaxStringBytes {
		return "", fmt.Errorf("string exceeds %d bytes", MaxStringBytes)
	}
	if !utf8.ValidString(text) {
		return "", fmt.Errorf("string must be valid UTF-8")
	}
	return text, nil
}

// StringCall cannot be overridden through Host registration. It returns fresh
// containers, performs no IO, and preserves cancellation errors with %w.
func StringCall(ctx context.Context, name string, args []Value) (result Value, err error) {
	defer func() {
		if err == nil {
			if text, ok := result.(string); ok && len(text) > MaxStringBytes {
				err = fmt.Errorf("string result exceeds %d bytes", MaxStringBytes)
			}
			if err == nil && ctx != nil {
				err = ctx.Err()
			}
		}
		if err != nil {
			result = nil
			err = fmt.Errorf("%s: %w", name, err)
		}
	}()
	if err = checkContext(ctx); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	spec, ok := stringops.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("unknown string operation")
	}
	if len(args) < spec.MinArgs || len(args) > spec.MaxArgs {
		return nil, argumentCountError(spec.MinArgs, spec.MaxArgs, len(args))
	}
	for index, arg := range args {
		if err := CheckType(arg, spec.Types[index]); err != nil {
			return nil, fmt.Errorf("argument %d: %w", index+1, err)
		}
	}
	if name == "string.join" {
		return stringJoin(ctx, args[0], args[1].(string))
	}
	text := args[0].(string)
	switch name {
	case "string.trim":
		return strings.TrimSpace(text), nil
	case "string.trim_start":
		return strings.TrimLeftFunc(text, unicode.IsSpace), nil
	case "string.trim_end":
		return strings.TrimRightFunc(text, unicode.IsSpace), nil
	case "string.lower", "string.upper":
		convert := unicode.ToLower
		if name == "string.upper" {
			convert = unicode.ToUpper
		}
		var out strings.Builder
		for _, char := range text {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			char = convert(char)
			if out.Len() > MaxStringBytes-utf8.RuneLen(char) {
				return nil, fmt.Errorf("string result exceeds %d bytes", MaxStringBytes)
			}
			out.WriteRune(char)
		}
		return out.String(), nil
	case "string.contains":
		return strings.Contains(text, args[1].(string)), nil
	case "string.starts_with":
		return strings.HasPrefix(text, args[1].(string)), nil
	case "string.ends_with":
		return strings.HasSuffix(text, args[1].(string)), nil
	case "string.find":
		index := strings.Index(text, args[1].(string))
		if index < 0 {
			return float64(-1), nil
		}
		return float64(utf8.RuneCountInString(text[:index])), nil
	case "string.count":
		needle := args[1].(string)
		if needle == "" {
			return nil, fmt.Errorf("count pattern must be nonempty")
		}
		return float64(strings.Count(text, needle)), nil
	case "string.split":
		separator := args[1].(string)
		if separator == "" {
			return nil, fmt.Errorf("split separator must be nonempty; use string.chars")
		}
		count := strings.Count(text, separator) + 1
		if err := listSize(count); err != nil {
			return nil, err
		}
		return stringParts(ctx, strings.Split(text, separator))
	case "string.lines":
		if text == "" {
			return []Value{}, nil
		}
		count := strings.Count(text, "\n")
		if !strings.HasSuffix(text, "\n") {
			count++
		}
		if err := listSize(count); err != nil {
			return nil, err
		}
		parts := strings.Split(text, "\n")
		terminated := len(parts) - 1
		if strings.HasSuffix(text, "\n") {
			parts = parts[:terminated]
		}
		for index := range parts {
			if index < terminated {
				parts[index] = strings.TrimSuffix(parts[index], "\r")
			}
		}
		return stringParts(ctx, parts)
	case "string.split_whitespace":
		// Count before allocation, including pathological many-small-field inputs.
		count, inWord := 0, false
		for _, char := range text {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if unicode.IsSpace(char) {
				inWord = false
			} else if !inWord {
				count++
				inWord = true
			}
			if err := listSize(count); err != nil {
				return nil, err
			}
		}
		return stringParts(ctx, strings.Fields(text))
	case "string.chars":
		if err := listSize(utf8.RuneCountInString(text)); err != nil {
			return nil, err
		}
		out := make([]Value, 0, utf8.RuneCountInString(text))
		for _, char := range text {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			out = append(out, string(char))
		}
		return out, nil
	case "string.slice":
		start, err := integer(args[1])
		if err != nil {
			return nil, err
		}
		end, err := integer(args[2])
		if err != nil {
			return nil, err
		}
		length := utf8.RuneCountInString(text)
		start, end = clipIndex(start, length), clipIndex(end, length)
		if end <= start {
			return "", nil
		}
		first, last, position := 0, len(text), 0
		for offset := range text {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if position == start {
				first = offset
			}
			if position == end {
				last = offset
				break
			}
			position++
		}
		return text[first:last], nil
	case "string.repeat":
		count, err := integer(args[1])
		if err != nil {
			return nil, err
		}
		if count < 0 {
			return nil, fmt.Errorf("repeat count must be nonnegative")
		}
		if text == "" || count == 0 {
			return "", nil
		}
		if count > MaxStringBytes/len(text) {
			return nil, fmt.Errorf("string result exceeds %d bytes", MaxStringBytes)
		}
		return strings.Repeat(text, count), nil
	case "string.replace":
		old, replacement := args[1].(string), args[2].(string)
		if old == "" {
			return nil, fmt.Errorf("replace pattern must be nonempty")
		}
		count := strings.Count(text, old)
		if len(args) == 4 {
			limit, err := integer(args[3])
			if err != nil {
				return nil, err
			}
			if limit < 0 {
				return nil, fmt.Errorf("replace count must be nonnegative")
			}
			count = min(count, limit)
		}
		growth := len(replacement) - len(old)
		if growth > 0 && count > (MaxStringBytes-len(text))/growth {
			return nil, fmt.Errorf("string result exceeds %d bytes", MaxStringBytes)
		}
		return strings.Replace(text, old, replacement, count), nil
	case "string.parse_number":
		number, err := strconv.ParseFloat(text, 64)
		if err != nil || !decimalNumber.MatchString(text) || math.IsInf(number, 0) || math.IsNaN(number) {
			return nil, fmt.Errorf("expected a finite decimal number, got %q", text)
		}
		return number, nil
	}
	return nil, fmt.Errorf("unimplemented string operation")
}

func stringParts(ctx context.Context, parts []string) (Value, error) {
	out := make([]Value, len(parts))
	for index, part := range parts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out[index] = part
	}
	return out, nil
}

func stringJoin(ctx context.Context, value Value, separator string) (Value, error) {
	items, err := listInput(value)
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	for index, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		text, err := stringInput(item)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", index, err)
		}
		if index > 0 {
			if out.Len() > MaxStringBytes-len(separator) {
				return nil, fmt.Errorf("string result exceeds %d bytes", MaxStringBytes)
			}
			out.WriteString(separator)
		}
		if out.Len() > MaxStringBytes-len(text) {
			return nil, fmt.Errorf("string result exceeds %d bytes", MaxStringBytes)
		}
		out.WriteString(text)
	}
	return out.String(), nil
}
