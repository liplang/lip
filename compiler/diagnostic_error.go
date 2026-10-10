package compiler

import (
	"fmt"
	"sort"
	"strings"

	"lipalpha/internal/listops"
	"lipalpha/internal/stringops"
)

// Repair advice attached where the cause is known takes precedence over the
// fallback classifier. Wrapping an error must retain this information.
type diagnosticError struct {
	message, code string
	hints         []string
}

func (e *diagnosticError) Error() string { return e.message }
func repairError(code, message string, hints ...string) error {
	return &diagnosticError{message: message, code: code, hints: hints}
}

func argumentCountError(name string, minimum, maximum, got int) error {
	expected := fmt.Sprint(minimum)
	if maximum < 0 {
		expected = fmt.Sprintf("at least %d", minimum)
	} else if minimum != maximum {
		expected = fmt.Sprintf("%d to %d", minimum, maximum)
		if maximum == minimum+1 {
			expected = fmt.Sprintf("%d or %d", minimum, maximum)
		}
	}
	signature := operationSignature(name)
	hint := "Match the number of arguments in the function declaration."
	if signature != "" {
		hint = "Use " + signature + "."
	}
	noun := "arguments"
	if minimum == 1 && maximum == 1 {
		noun = "argument"
	}
	return repairError("LIP_ARGUMENT_ERROR", fmt.Sprintf("%s expects %s %s, got %d", name, expected, noun, got), hint)
}

func operationSignature(name string) string {
	if spec, ok := listops.Lookup(name); ok {
		types := append([]string(nil), spec.Types...)
		if spec.Callback >= 0 {
			noun := "parameters"
			if spec.CallbackArity == 1 {
				noun = "parameter"
			}
			types[spec.Callback] = fmt.Sprintf("fn with %d %s returning %s", spec.CallbackArity, noun, spec.CallbackResult)
		}
		return formatSignature(name, types, spec.MinArgs, spec.MaxArgs)
	}
	if spec, ok := stringops.Lookup(name); ok {
		return formatSignature(name, spec.Types, spec.MinArgs, spec.MaxArgs)
	}
	switch name {
	case "str":
		return "str(value)"
	case "len":
		return "len(string_or_list_or_object)"
	case "isEmpty", "is_empty":
		return "isEmpty(string_or_list_or_object)"
	case "isNotEmpty", "is_not_empty":
		return "isNotEmpty(string_or_list_or_object)"
	case "random":
		return "random()"
	case "random_list":
		return "random_list(count)"
	case "random_int":
		return "random_int(end) or random_int(start, end)"
	case "random_choice":
		return "random_choice(list)"
	case "random_shuffle":
		return "random_shuffle(list)"
	case "range":
		return "range(end), range(start, end) or range(start, end, step)"
	case "fold", "list.fold":
		return name + "(list, seed, reducer), where reducer is a local pure function or fn(accumulator, item) { ... }"
	case "fail":
		return "fail(message: string)"
	case "state":
		return "state(initial_value) as a Flow binding"
	}
	return ""
}

func formatSignature(name string, types []string, minimum, maximum int) string {
	if maximum < 0 {
		typ := "value"
		if len(types) > 0 {
			typ = types[0]
		}
		return name + "(" + typ + ", ...)"
	}
	arguments := make([]string, maximum)
	for i := range arguments {
		arguments[i] = "value"
		if i < len(types) {
			arguments[i] = types[i]
		}
		if i >= minimum {
			arguments[i] = "[" + arguments[i] + "]"
		}
	}
	return name + "(" + strings.Join(arguments, ", ") + ")"
}

// Only suggest a close, unique spelling. Ambiguous matches should not steer
// the programmer toward an arbitrary operation or module.
func spellingSuggestion(name string, candidates []string) string {
	sort.Strings(candidates)
	best, distance := "", 3
	ambiguous := false
	for _, candidate := range candidates {
		if candidate == name {
			continue
		}
		d := spellingDistance([]rune(name), []rune(candidate))
		limit := 1
		if len([]rune(name)) >= 5 {
			limit = 2
		}
		if d > limit {
			continue
		}
		if d < distance {
			best, distance, ambiguous = candidate, d, false
		} else if d == distance && candidate != best {
			ambiguous = true
		}
	}
	if ambiguous {
		return ""
	}
	return best
}

func spellingDistance(a, b []rune) int {
	if len(a)-len(b) > 2 || len(b)-len(a) > 2 {
		return 3
	}
	previousPrevious := make([]int, len(b)+1)
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i, r := range a {
		current := make([]int, len(b)+1)
		current[0] = i + 1
		for j, s := range b {
			cost := 0
			if r != s {
				cost = 1
			}
			current[j+1] = min(previous[j+1]+1, current[j]+1, previous[j]+cost)
			if i > 0 && j > 0 && a[i] == b[j-1] && a[i-1] == b[j] {
				current[j+1] = min(current[j+1], previousPrevious[j-1]+1)
			}
		}
		previousPrevious, previous = previous, current
	}
	return previous[len(b)]
}

func operationCandidates(functions map[string]bool) []string {
	candidates := strings.Fields("str len range fold fail print state retry feedback isEmpty isNotEmpty is_empty is_not_empty random random_list random_int random_choice random_shuffle")
	candidates = append(candidates, listops.Aliases()...)
	for name := range functions {
		candidates = append(candidates, name)
	}
	for _, op := range listops.All() {
		candidates = append(candidates, op.Name)
	}
	for _, op := range stringops.All() {
		candidates = append(candidates, op.Name)
	}
	return candidates
}
