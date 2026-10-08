package compiler

import (
	"strings"
	"testing"
)

func TestNumericOperatorChecksAndComments(t *testing.T) {
	for _, source := range []string{
		"# // and ** in a comment {\nprint(7//2) # inline comment [\nprint(2**3)",
		`print("# // **")`,
		`fn power(x: number) -> number { x ** 2 }; print(power(3))`,
		`print(list.map(range(3), fn(x) { x ** 2 // 2 }))`,
		`print(match true { true => 2 ** -2, false => 7 // 2 })`,
		`print(7 % 2 + 8 */ 2)`,
	} {
		if _, err := ParseAndBuild(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	for _, source := range []string{`print("2" ** 3)`, `print(2 ** false)`, `print([] // 2)`, `print(2 // null)`, `print(7 % true)`, `print("8" */ 2)`} {
		if _, err := ParseAndBuild(source); err == nil || !strings.Contains(err.Error(), "expects numbers") {
			t.Fatalf("%s: %v", source, err)
		}
	}
}
