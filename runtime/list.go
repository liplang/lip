package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"

	"lipalpha/internal/listops"
)

const MaxListLength = 1_000_000

func listSize(size int) error {
	if size < 0 || size > MaxListLength {
		return fmt.Errorf("list result exceeds %d element slots", MaxListLength)
	}
	return nil
}

func listInput(value Value) ([]Value, error) {
	if items, ok := value.([]Value); ok {
		if err := listSize(len(items)); err != nil {
			return nil, err
		}
		return items, nil
	}
	if value != nil {
		v := reflect.ValueOf(value)
		if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
			if err := listSize(v.Len()); err != nil {
				return nil, err
			}
		}
	}
	items, err := sequenceValues(value)
	if err != nil {
		return nil, err
	}
	if err := listSize(len(items)); err != nil {
		return nil, err
	}
	return items, nil
}

// ListCall evaluates the fixed pure standard library. Callbacks are compiled
// local functions, never operations looked up in a caller-supplied Host.
func ListCall(ctx context.Context, name string, args []Value, callback Op) (result Value, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("%s: %w", name, err)
		}
	}()
	if err = checkContext(ctx); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	spec, ok := listops.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("unknown list operation")
	}
	provided := len(args)
	if spec.Callback >= 0 {
		if callback == nil {
			return nil, fmt.Errorf("needs a pure callback")
		}
		provided++
	}
	if provided < spec.MinArgs || spec.MaxArgs >= 0 && provided > spec.MaxArgs {
		return nil, argumentCountError(spec.MinArgs, spec.MaxArgs, provided)
	}
	for index, arg := range args {
		if err := CheckType(arg, spec.TypeAt(index)); err != nil {
			return nil, fmt.Errorf("argument %d: %w", index+1, err)
		}
	}
	switch name {
	case "list.concat", "list.zip", "list.cartesian":
		return listCombine(ctx, name, args)
	case "list.repeat":
		count, err := integer(args[1])
		if err != nil {
			return nil, err
		}
		if count < 0 {
			return nil, fmt.Errorf("repeat count must be nonnegative")
		}
		if err = listSize(count); err != nil {
			return nil, err
		}
		out := make([]Value, count)
		for index := range out {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			out[index] = args[0]
		}
		return out, nil
	}
	items, err := listInput(args[0])
	if err != nil {
		return nil, err
	}
	switch name {
	case "list.append", "list.prepend":
		if err = listSize(len(items) + 1); err != nil {
			return nil, err
		}
		out := make([]Value, 0, len(items)+1)
		if name == "list.prepend" {
			out = append(out, args[1])
		}
		out = append(out, items...)
		if name == "list.append" {
			out = append(out, args[1])
		}
		return out, nil
	case "list.reverse":
		out := make([]Value, len(items))
		for index, item := range items {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			out[len(items)-1-index] = item
		}
		return out, nil
	case "list.take", "list.drop", "list.slice":
		start, end := 0, len(items)
		n, err := integer(args[1])
		if err != nil {
			return nil, err
		}
		if name == "list.slice" {
			start = clipIndex(n, len(items))
			n, err = integer(args[2])
			if err != nil {
				return nil, err
			}
			end = clipIndex(n, len(items))
		} else if n >= 0 {
			n = min(n, len(items))
			if name == "list.take" {
				end = n
			} else {
				start = n
			}
		} else {
			n = max(n, -len(items))
			if name == "list.take" {
				start = len(items) + n
			} else {
				end = len(items) + n
			}
		}
		if end < start {
			end = start
		}
		return append([]Value{}, items[start:end]...), nil
	case "list.first", "list.last":
		if len(items) == 0 {
			return nil, fmt.Errorf("needs a nonempty list")
		}
		if name == "list.first" {
			return items[0], nil
		}
		return items[len(items)-1], nil
	case "list.contains", "list.count":
		count := 0
		for _, item := range items {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			if equalValues(item, args[1]) {
				count++
				if name == "list.contains" {
					return true, nil
				}
			}
		}
		if name == "list.contains" {
			return false, nil
		}
		return float64(count), nil
	case "list.enumerate":
		if err = listSize(len(items) * 3); err != nil {
			return nil, err
		}
		out := make([]Value, len(items))
		for index, item := range items {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			out[index] = []Value{float64(index), item}
		}
		return out, nil
	case "list.riffle":
		size := max(0, len(items)*2-1)
		if err = listSize(size); err != nil {
			return nil, err
		}
		out := make([]Value, 0, size)
		for index, item := range items {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			if index > 0 {
				out = append(out, args[1])
			}
			out = append(out, item)
		}
		return out, nil
	case "list.partition":
		return listPartition(ctx, items, args[1:])
	case "list.flatten":
		depth := int(^uint(0) >> 1)
		if len(args) == 2 {
			depth, err = integer(args[1])
			if err != nil {
				return nil, err
			}
			if depth < 0 {
				return nil, fmt.Errorf("flatten depth must be nonnegative")
			}
		}
		return listFlatten(ctx, items, depth)
	case "list.transpose":
		return listTranspose(ctx, items)
	case "list.unique", "list.group", "list.group_by", "list.split_by":
		return listGroup(ctx, name, items, callback)
	case "list.sort", "list.sort_by", "list.min", "list.max":
		return listSort(ctx, name, items, callback)
	case "list.sum", "list.product":
		value := float64(0)
		op := "+"
		if name == "list.product" {
			value = 1
			op = "*"
		}
		for index, item := range items {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			n, err := Number(item)
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", index, err)
			}
			next, err := Binary(op, value, n)
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", index, err)
			}
			value = next.(float64)
		}
		return value, nil
	case "list.map", "list.filter", "list.any", "list.all", "list.scan":
		return listApply(ctx, name, items, args, callback)
	}
	return nil, fmt.Errorf("unimplemented list operation")
}

func clipIndex(index, length int) int {
	if index < 0 {
		index += length
	}
	return min(max(index, 0), length)
}

func listCombine(ctx context.Context, name string, args []Value) (Value, error) {
	lists := make([][]Value, len(args))
	total := 0
	for index, arg := range args {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items, err := listInput(arg)
		if err != nil {
			return nil, err
		}
		lists[index] = items
		total += len(items)
		if name == "list.concat" {
			if err := listSize(total); err != nil {
				return nil, err
			}
		}
	}
	if name == "list.concat" {
		if err := listSize(total); err != nil {
			return nil, err
		}
		out := make([]Value, 0, total)
		for _, items := range lists {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			out = append(out, items...)
		}
		return out, nil
	}
	if name == "list.zip" {
		rows := 0
		if len(lists) > 0 {
			rows = len(lists[0])
			for _, items := range lists {
				rows = min(rows, len(items))
			}
		}
		if len(lists) > 0 && rows > MaxListLength/(len(lists)+1) {
			return nil, fmt.Errorf("zip result exceeds %d element slots", MaxListLength)
		}
		out := make([]Value, rows)
		for row := range out {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			tuple := make([]Value, len(lists))
			for col, items := range lists {
				tuple[col] = items[row]
			}
			out[row] = tuple
		}
		return out, nil
	}
	count := 1
	for _, items := range lists {
		if len(items) == 0 {
			return []Value{}, nil
		}
		if count > MaxListLength/len(items) {
			return nil, fmt.Errorf("cartesian result exceeds %d element slots", MaxListLength)
		}
		count *= len(items)
	}
	if count > MaxListLength/(len(lists)+1) {
		return nil, fmt.Errorf("cartesian result exceeds %d element slots", MaxListLength)
	}
	out := make([]Value, count)
	indices := make([]int, len(lists))
	for row := range out {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tuple := make([]Value, len(lists))
		for col, items := range lists {
			tuple[col] = items[indices[col]]
		}
		out[row] = tuple
		for col := len(lists) - 1; col >= 0; col-- {
			indices[col]++
			if indices[col] < len(lists[col]) {
				break
			}
			indices[col] = 0
		}
	}
	return out, nil
}

func listPartition(ctx context.Context, items []Value, args []Value) (Value, error) {
	size, err := integer(args[0])
	if err != nil {
		return nil, err
	}
	step := size
	if len(args) == 2 {
		step, err = integer(args[1])
		if err != nil {
			return nil, err
		}
	}
	if size <= 0 || step <= 0 {
		return nil, fmt.Errorf("partition size and step must be positive")
	}
	out := []Value{}
	slots := 0
	for start := 0; start < len(items); {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		width := min(size, len(items)-start)
		slots += 1 + width
		if err = listSize(slots); err != nil {
			return nil, err
		}
		out = append(out, append([]Value{}, items[start:start+width]...))
		if step >= len(items)-start {
			break
		}
		start += step
	}
	return out, nil
}

func listFlatten(ctx context.Context, items []Value, depth int) (Value, error) {
	out := []Value{}
	var visit func([]Value, int, int) error
	visit = func(values []Value, remaining, nesting int) error {
		if nesting > MaxCallDepth {
			return fmt.Errorf("flatten nesting exceeds %d", MaxCallDepth)
		}
		for _, item := range values {
			if err := ctx.Err(); err != nil {
				return err
			}
			if remaining > 0 && CheckType(item, "list") == nil {
				nested, err := listInput(item)
				if err != nil {
					return err
				}
				if err = visit(nested, remaining-1, nesting+1); err != nil {
					return err
				}
			} else {
				if err := listSize(len(out) + 1); err != nil {
					return err
				}
				out = append(out, item)
			}
		}
		return nil
	}
	if err := visit(items, depth, 0); err != nil {
		return nil, err
	}
	return out, nil
}

func listTranspose(ctx context.Context, items []Value) (Value, error) {
	rows := make([][]Value, len(items))
	cols := 0
	for index, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, err := listInput(item)
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", index, err)
		}
		if index == 0 {
			cols = len(row)
		} else if len(row) != cols {
			return nil, fmt.Errorf("row %d has %d columns, expected %d", index, len(row), cols)
		}
		rows[index] = row
	}
	if cols > 0 && len(rows)+1 > MaxListLength/cols {
		return nil, fmt.Errorf("transpose result exceeds %d element slots", MaxListLength)
	}
	out := make([]Value, cols)
	for col := range out {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		column := make([]Value, len(rows))
		for row, values := range rows {
			column[row] = values[col]
		}
		out[col] = column
	}
	return out, nil
}

func listCallback(ctx context.Context, callback Op, index int, args ...Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, err := ResolveValue(ctx, callback(ctx, args))
	if err != nil {
		return nil, fmt.Errorf("element %d: %w", index, err)
	}
	return value, nil
}

func listApply(ctx context.Context, name string, items, args []Value, callback Op) (Value, error) {
	out := []Value{}
	if name == "list.scan" {
		if err := listSize(len(items) + 1); err != nil {
			return nil, err
		}
		out = append(out, args[1])
	}
	for index, item := range items {
		params := []Value{item}
		if name == "list.scan" {
			params = []Value{out[len(out)-1], item}
		}
		value, err := listCallback(ctx, callback, index, params...)
		if err != nil {
			return nil, err
		}
		if name == "list.filter" || name == "list.any" || name == "list.all" {
			match, err := Bool(value)
			if err != nil {
				return nil, fmt.Errorf("callback element %d: %w", index, err)
			}
			if name == "list.any" && match {
				return true, nil
			}
			if name == "list.all" && !match {
				return false, nil
			}
			if name == "list.filter" && match {
				out = append(out, item)
			}
		} else {
			out = append(out, value)
		}
	}
	if name == "list.any" {
		return false, nil
	}
	if name == "list.all" {
		return true, nil
	}
	return out, nil
}

// Canonical JSON fingerprints keep common grouping/uniqueness linear. Buckets
// still check equality, so different Go values with the same JSON do not merge.
func listFingerprint(value Value) (string, error) {
	if number, err := Number(value); err == nil {
		if number == 0 {
			number = 0
		}
		return "number:" + strconv.FormatFloat(number, 'g', -1, 64), nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("group/unique keys must be finite JSON data: %w", err)
	}
	return string(data), nil
}

func listGroup(ctx context.Context, name string, items []Value, callback Op) (Value, error) {
	keys := []Value{}
	groups := [][]Value{}
	buckets := map[string][]int{}
	out := []Value{}
	slots := 0
	for index, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := item
		if callback != nil {
			value, err := listCallback(ctx, callback, index, item)
			if err != nil {
				return nil, err
			}
			key = value
		}
		fingerprint, err := listFingerprint(key)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", index, err)
		}
		group := -1
		if name == "list.split_by" {
			if len(keys) > 0 && equalValues(keys[len(keys)-1], key) {
				group = len(keys) - 1
			}
		} else {
			for _, candidate := range buckets[fingerprint] {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if equalValues(keys[candidate], key) {
					group = candidate
					break
				}
			}
		}
		if group < 0 {
			group = len(keys)
			keys = append(keys, key)
			groups = append(groups, []Value{})
			buckets[fingerprint] = append(buckets[fingerprint], group)
			slots++
			if name == "list.unique" {
				out = append(out, item)
			}
		}
		if name != "list.unique" {
			groups[group] = append(groups[group], item)
			slots++
		}
		if err := listSize(slots); err != nil {
			return nil, err
		}
	}
	if name == "list.unique" {
		return out, nil
	}
	out = make([]Value, len(keys))
	for index, key := range keys {
		out[index] = map[string]Value{"key": key, "values": groups[index]}
	}
	return out, nil
}

func listSort(ctx context.Context, name string, items []Value, callback Op) (Value, error) {
	type keyed struct {
		item, key Value
		number    float64
		text      string
	}
	values := make([]keyed, len(items))
	category := ""
	for index, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := item
		if callback != nil {
			value, err := listCallback(ctx, callback, index, item)
			if err != nil {
				return nil, err
			}
			key = value
		}
		entry := keyed{item: item, key: key}
		kind := "number"
		if text, ok := scalarString(key); ok {
			kind = "string"
			entry.text = text
		} else {
			number, err := Number(key)
			if err != nil {
				return nil, fmt.Errorf("sort key at element %d must be number or string, got %s: %w", index, TypeName(key), err)
			}
			entry.number = number
		}
		if category != "" && category != kind {
			return nil, fmt.Errorf("sort key at element %d has type %s, expected %s; sort keys must have one type", index, kind, category)
		}
		category = kind
		values[index] = entry
	}
	sort.SliceStable(values, func(i, j int) bool {
		if ctx.Err() != nil {
			return false
		}
		if category == "string" {
			return values[i].text < values[j].text
		}
		return values[i].number < values[j].number
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if name == "list.min" || name == "list.max" {
		if len(values) == 0 {
			return nil, fmt.Errorf("needs a nonempty list")
		}
		if name == "list.min" {
			return values[0].item, nil
		}
		return values[len(values)-1].item, nil
	}
	out := make([]Value, len(values))
	for index, entry := range values {
		out[index] = entry.item
	}
	return out, nil
}
