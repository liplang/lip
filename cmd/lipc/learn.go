package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"lip/script"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Lessons use ordinary LIP source and the same Compile/Run contract as files.
// They are bundled into lipc, so learn works outside a source checkout.
type lesson struct {
	id, title, section, explain, task, template, solution, hint string
	examples                                                    []lessonExample
	cases                                                       []lessonCase
	demo                                                        bool
	entry                                                       string
	entryInput                                                  bool
	sources                                                     map[string]script.Source
}
type lessonCase struct{ input, want string }
type lessonExample struct{ code, want, setup string }

const answerMark = "__ANSWER__"

var course = []lesson{
	{
		id:       "hello",
		title:    "Return a string",
		section:  "Getting started",
		explain:  "return gives the program its result. You only need to replace __ANSWER__.",
		task:     "Fill the blank with the text Hello, LIP!, including double quotes.",
		template: "flow Hello()->string { return __ANSWER__ }",
		solution: "\"Hello, LIP!\"",
		hint:     "A string is text between double quotes. Keep the comma, space, and exclamation mark.",
		examples: []lessonExample{
			{code: "\"Hello, Ada!\"", want: "\"Hello, Ada!\""},
			{code: "\"LIP\"", want: "\"LIP\""},
		},
		cases: []lessonCase{{"{}", "\"Hello, LIP!\""}},
	},
	{
		id:       "bindings",
		title:    "Give a value a name",
		section:  "Getting started",
		explain:  "A binding such as total = 12 * 3 gives one value a name. Names are not updated later.",
		task:     "Multiply price by count to get the total.",
		template: "flow Total(price:number, count:number)->number {\n    total = __ANSWER__\n    return total\n}",
		solution: "price * count",
		hint:     "Use the parameter names price and count, with * between them.",
		examples: []lessonExample{
			{code: "12 * 3", want: "36"},
			{code: "5 * 7", want: "35"},
		},
		cases: []lessonCase{{"{\"price\":12,\"count\":3}", "36"}, {"{\"price\":5,\"count\":7}", "35"}},
	},
	{
		id:       "arithmetic",
		title:    "Choose the calculation order",
		section:  "Getting started",
		explain:  "Parentheses make one part of a calculation happen first.",
		task:     "Add 2 to x first. Multiply that result by 3.",
		template: "flow Calculate(x:number)->number { return __ANSWER__ }",
		solution: "(x + 2) * 3",
		hint:     "Put x + 2 inside parentheses, then multiply by 3.",
		examples: []lessonExample{
			{code: "(4 + 2) * 3", want: "18"},
			{code: "4 + 2 * 3", want: "10"},
		},
		cases: []lessonCase{{"{\"x\":4}", "18"}, {"{\"x\":-2}", "0"}},
	},
	{
		id:       "strings",
		title:    "Join two pieces of text",
		section:  "Getting started",
		explain:  "+ joins text as well as adding numbers. Keep spaces inside the quotes.",
		task:     "Return Hello, followed by a space and name.",
		template: "flow Greet(name:string)->string { return __ANSWER__ }",
		solution: "\"Hello, \" + name",
		hint:     "Use + to join strings. The first string ends with a comma and a space.",
		examples: []lessonExample{
			{code: "\"Hello, \" + \"Ada\"", want: "\"Hello, Ada\""},
			{code: "\"L\" + \"IP\"", want: "\"LIP\""},
		},
		cases: []lessonCase{{"{\"name\":\"Ada\"}", "\"Hello, Ada\""}, {"{\"name\":\"Lin\"}", "\"Hello, Lin\""}},
	},
	{
		id:       "comparison",
		title:    "Ask a yes-or-no question",
		section:  "Getting started",
		explain:  "bool is a yes-or-no value: true or false. A comparison produces one directly.",
		task:     "Return true when score is at least 0.8; otherwise return false.",
		template: "flow Ready(score:number)->bool { return __ANSWER__ }",
		solution: "score >= 0.8",
		hint:     ">= means greater than or equal to. A comparison already returns true or false.",
		examples: []lessonExample{
			{code: "0.9 >= 0.8", want: "true"},
			{code: "0.7 >= 0.8", want: "false"},
		},
		cases: []lessonCase{{"{\"score\":0.8}", "true"}, {"{\"score\":0.7}", "false"}},
	},
	{
		id:       "first",
		title:    "Read one list item",
		section:  "Working with data",
		explain:  "A list keeps values in order. Indices start at 0; -1 reads the last item.",
		task:     "Return the first item of scores. The supplied lists are not empty.",
		template: "flow First(scores:list)->number { return __ANSWER__ }",
		solution: "scores[0]",
		hint:     "The first list position is 0, so use [0] after the list name.",
		examples: []lessonExample{
			{code: "[8, 9, 10][0]", want: "8"},
			{code: "[8, 9, 10][-1]", want: "10"},
		},
		cases: []lessonCase{{"{\"scores\":[8,9]}", "8"}, {"{\"scores\":[3]}", "3"}},
	},
	{
		id:       "fields",
		title:    "Read one object field",
		section:  "Working with data",
		explain:  "Objects give values field names. {name:\"Ada\"}.name reads the name field.",
		task:     "Return the name field from user.",
		template: "flow Name(user:object)->string { return __ANSWER__ }",
		solution: "user.name",
		hint:     "Write the object name, a dot, and the field name.",
		examples: []lessonExample{
			{code: "{name:\"Ada\", role:\"reader\"}.name", want: "\"Ada\""},
			{code: "{name:\"Lin\"}.name", want: "\"Lin\""},
		},
		cases: []lessonCase{{"{\"user\":{\"name\":\"Ada\"}}", "\"Ada\""}, {"{\"user\":{\"name\":\"Lin\"}}", "\"Lin\""}},
	},
	{
		id:       "containers",
		title:    "Build a small result object",
		section:  "Working with data",
		explain:  "Use an object to return related values together. A field uses name: value.",
		task:     "Return an object with name from user.name and first from scores[0].",
		template: "flow Read(user:object, scores:list)->object { return __ANSWER__ }",
		solution: "{name:user.name, first:scores[0]}",
		hint:     "Start with {name: ..., first: ...}. Replace each ... with the requested value.",
		examples: []lessonExample{
			{code: "{name:\"Ada\", first:8}", want: "{\"first\":8,\"name\":\"Ada\"}"},
			{code: "{name:{name:\"Lin\"}.name, first:[9,10][0]}", want: "{\"first\":9,\"name\":\"Lin\"}"},
		},
		cases: []lessonCase{{"{\"user\":{\"name\":\"Ada\"},\"scores\":[0.8,0.9]}", "{\"first\":0.8,\"name\":\"Ada\"}"}, {"{\"user\":{\"name\":\"Lin\"},\"scores\":[1]}", "{\"first\":1,\"name\":\"Lin\"}"}},
	},
	{
		id:       "slice",
		title:    "Take the beginning of a list",
		section:  "Working with data",
		explain:  "A slice [start:stop] keeps a range. Omitting start means start at the beginning.",
		task:     "Return the first two items of xs, or all items if there are fewer than two.",
		template: "flow FirstTwo(xs:list)->list { return __ANSWER__ }",
		solution: "xs[:2]",
		hint:     "Use [:2]. The stop position 2 is excluded.",
		examples: []lessonExample{
			{code: "[10,20,30][:2]", want: "[10,20]"},
			{code: "[10][:2]", want: "[10]"},
		},
		cases: []lessonCase{{"{\"xs\":[10,20,30]}", "[10,20]"}, {"{\"xs\":[]}", "[]"}},
	},
	{
		id:       "reverse",
		title:    "Reverse a list",
		section:  "Working with data",
		explain:  "A negative slice step moves backward. [::-1] visits the entire list from the end.",
		task:     "Return xs in reverse order.",
		template: "flow Reverse(xs:list)->list { return __ANSWER__ }",
		solution: "xs[::-1]",
		hint:     "Use [::-1]: both ends are omitted and the step is -1.",
		examples: []lessonExample{
			{code: "[1,2,3][::-1]", want: "[3,2,1]"},
			{code: "[][::-1]", want: "[]"},
		},
		cases: []lessonCase{{"{\"xs\":[1,2,3]}", "[3,2,1]"}, {"{\"xs\":[]}", "[]"}},
	},
	{
		id:       "conditions",
		title:    "Choose between two values",
		section:  "Deciding and transforming",
		explain:  "An if expression chooses one value. Only the selected branch is evaluated.",
		task:     "Return \"ready\" when score is at least 0.8, or \"retry\" otherwise.",
		template: "flow Decide(score:number)->string { return __ANSWER__ }",
		solution: "if score >= 0.8 { \"ready\" } else { \"retry\" }",
		hint:     "Use if score >= 0.8 { ... } else { ... }. Put a quoted string in each branch.",
		examples: []lessonExample{
			{code: "if true {\"yes\"} else {\"no\"}", want: "\"yes\""},
			{code: "if 3 > 5 {\"large\"} else {\"small\"}", want: "\"small\""},
		},
		cases: []lessonCase{{"{\"score\":0.8}", "\"ready\""}, {"{\"score\":0.7}", "\"retry\""}},
	},
	{
		id:       "match",
		title:    "Choose from several cases",
		section:  "Deciding and transforming",
		explain:  "match tries cases in order. => connects a pattern to its result; _ is the fallback.",
		task:     "Return \"zero\" for 0, \"one\" for 1, and \"other\" for any other n.",
		template: "flow Label(n:number)->string { return __ANSWER__ }",
		solution: "match n { 0 => \"zero\", 1 => \"one\", _ => \"other\" }",
		hint:     "Start with match n { 0 => \"zero\", ... }. Finish with _ => \"other\".",
		examples: []lessonExample{
			{code: "match 0 {0 => \"zero\", _ => \"other\"}", want: "\"zero\""},
			{code: "match 2 {0 => \"zero\", _ => \"other\"}", want: "\"other\""},
		},
		cases: []lessonCase{{"{\"n\":0}", "\"zero\""}, {"{\"n\":1}", "\"one\""}, {"{\"n\":5}", "\"other\""}},
	},
	{
		id:       "functions",
		title:    "Call a pure function",
		section:  "Deciding and transforming",
		explain:  "fn computes a value. Its last expression is the result; it cannot perform external actions.",
		task:     "Use the supplied square function to square n.",
		template: "fn square(x:number)->number { x * x }\nflow Square(n:number)->number { return __ANSWER__ }",
		solution: "square(n)",
		hint:     "A function call is its name followed by parentheses containing the argument.",
		examples: []lessonExample{
			{code: "square(3)", want: "9", setup: "fn square(x:number)->number {x*x}"},
			{code: "square(x=-4)", want: "16", setup: "fn square(x:number)->number {x*x}"},
		},
		cases: []lessonCase{{"{\"n\":3}", "9"}, {"{\"n\":-4}", "16"}},
	},
	{
		id:       "function-body",
		title:    "Write a tiny function",
		section:  "Deciding and transforming",
		explain:  "Defining a function gives a calculation a reusable name.",
		task:     "Fill the function body so double returns twice x.",
		template: "fn double(x:number)->number { __ANSWER__ }\nflow Main(n:number)->number { return double(n) }",
		solution: "x * 2",
		hint:     "The body uses the parameter x. Its final expression supplies the answer.",
		examples: []lessonExample{
			{code: "double(4)", want: "8", setup: "fn double(x:number)->number {x*2}"},
			{code: "double(-2)", want: "-4", setup: "fn double(x:number)->number {x*2}"},
		},
		cases: []lessonCase{{"{\"n\":4}", "8"}, {"{\"n\":-2}", "-4"}},
	},
	{
		id:       "closures",
		title:    "Remember a value in a function",
		section:  "Deciding and transforming",
		explain:  "A function can keep values from where it was defined. This is called a closure.",
		task:     "Fill add_bonus so it adds the surrounding bonus value to x.",
		template: "flow Main(n:number)->number {\n    bonus = 2\n    add_bonus = fn(x:number)->number { __ANSWER__ }\n    return add_bonus(n)\n}",
		solution: "x + bonus",
		hint:     "Use x + bonus. x is the function's parameter; bonus is kept from its surrounding scope.",
		examples: []lessonExample{
			{code: "AddBonus(3)", want: "5", setup: "flow AddBonus(n:number)->number {\n    bonus = 2\n    add = fn(x:number)->number {x+bonus}\n    return add(n)\n}"},
			{code: "AddBonus(0)", want: "2", setup: "flow AddBonus(n:number)->number {\n    bonus = 2\n    add = fn(x:number)->number {x+bonus}\n    return add(n)\n}"},
		},
		cases: []lessonCase{{"{\"n\":3}", "5"}, {"{\"n\":0}", "2"}},
	},
	{
		id:       "map",
		title:    "Apply a function to each item",
		section:  "Deciding and transforming",
		explain:  "map returns a new list. Its pure function runs once for each item, keeping list order.",
		task:     "Use map and double to double every item in xs.",
		template: "fn double(x:number)->number {x*2}\nflow Main(xs:list)->list {return __ANSWER__}",
		solution: "map(xs, double)",
		hint:     "map takes the list first and the function name second: map(xs, double).",
		examples: []lessonExample{
			{code: "map([1,2,3], double)", want: "[2,4,6]", setup: "fn double(x:number)->number {x*2}"},
			{code: "map([], double)", want: "[]", setup: "fn double(x:number)->number {x*2}"},
		},
		cases: []lessonCase{{"{\"xs\":[1,2,3]}", "[2,4,6]"}, {"{\"xs\":[]}", "[]"}},
	},
	{
		id:       "comprehension",
		title:    "Describe a new list",
		section:  "Deciding and transforming",
		explain:  "A list comprehension places the result expression before the loop.",
		task:     "Return a new list containing x + 1 for every x in xs.",
		template: "flow Main(xs:list)->list {return __ANSWER__}",
		solution: "[x + 1 for x in xs]",
		hint:     "Use [result for x in xs]. Here result is x + 1.",
		examples: []lessonExample{
			{code: "[x + 1 for x in [1,2]]", want: "[2,3]"},
			{code: "[x + 1 for x in []]", want: "[]"},
		},
		cases: []lessonCase{{"{\"xs\":[1,2]}", "[2,3]"}, {"{\"xs\":[]}", "[]"}},
	},
	{
		id:       "filter",
		title:    "Keep only matching items",
		section:  "Deciding and transforming",
		explain:  "An if at the end of a comprehension filters items before computing each result.",
		task:     "Keep positive items in xs and square them.",
		template: "flow Positive(xs:list)->list { return __ANSWER__ }",
		solution: "[x * x for x in xs if x > 0]",
		hint:     "Start with [x * x for x in xs], then add if x > 0 before the closing ].",
		examples: []lessonExample{
			{code: "[x for x in [-1,0,2] if x > 0]", want: "[2]"},
			{code: "[x*x for x in [-1,0,2] if x > 0]", want: "[4]"},
		},
		cases: []lessonCase{{"{\"xs\":[-1,0,2,3]}", "[4,9]"}, {"{\"xs\":[-2]}", "[]"}},
	},
	{
		id:       "fold",
		title:    "Add up a list",
		section:  "Deciding and transforming",
		explain:  "fold keeps a running result. For [1,2,3], the total goes from 0 to 1 to 3 to 6.",
		task:     "Use fold, starting at 0, with the supplied add function to sum xs.",
		template: "fn add(total:number, x:number)->number {total+x}\nflow Sum(xs:list)->number { return __ANSWER__ }",
		solution: "fold(xs, 0, add)",
		hint:     "Write fold(xs, 0, add): list, starting total, then combining function.",
		examples: []lessonExample{
			{code: "fold([1,2,3], 0, add)", want: "6", setup: "fn add(total:number,x:number)->number {total+x}"},
			{code: "fold([], 0, add)", want: "0", setup: "fn add(total:number,x:number)->number {total+x}"},
		},
		cases: []lessonCase{{"{\"xs\":[1,2,3]}", "6"}, {"{\"xs\":[]}", "0"}},
	},
	{
		id:       "diagnostics",
		title:    "Log while computing a value",
		section:  "Deciding and transforming",
		explain:  "fn may print or log diagnostics without changing its calculation. log returns null; the final expression is still the result. Diagnostics go to stderr.",
		task:     "The logging line is supplied. Fill the result with x * 2.",
		template: "fn doubled(x:number)->number {\n    log(\"learn\", x)\n    __ANSWER__\n}\nflow Main(x:number)->number {return doubled(x)}",
		solution: "x * 2",
		hint:     "Write x * 2. Do not return log(...): it returns null, not the calculation.",
		examples: []lessonExample{
			{code: "doubled(3)", want: "6", setup: "fn doubled(x:number)->number {log(\"learn\",x);x*2}"},
			{code: "doubled(-1)", want: "-2", setup: "fn doubled(x:number)->number {log(\"learn\",x);x*2}"},
		},
		cases: []lessonCase{{"{\"x\":3}", "6"}, {"{\"x\":-1}", "-2"}},
	},
	{
		id:       "carry",
		title:    "Update a loop result",
		section:  "Deciding and transforming",
		explain:  "for ... with starts with a value and carries a new one each round. An empty list returns the starting value.",
		task:     "Fill next with the old total plus the current x.",
		template: "flow Sum(xs:list)->number {\n    total = for x in xs with total = 0 {\n        next __ANSWER__\n    }\n    return total\n}",
		solution: "total + x",
		hint:     "Use total + x. next carries that value into the next iteration.",
		examples: []lessonExample{
			{code: "for x in [2,4] with total=0 {next total+x}", want: "6"},
			{code: "for x in [] with total=0 {next total+x}", want: "0"},
		},
		cases: []lessonCase{{"{\"xs\":[2,4,6]}", "12"}, {"{\"xs\":[]}", "0"}},
	},
	{
		id:       "flow-call",
		title:    "Call a child Flow",
		section:  "Organizing work",
		explain:  "A Flow groups work and dependencies. A child Flow finishes its selected work before returning its result.",
		task:     "Call Double with n to get twice n.",
		template: "flow Double(value:number)->number {return value*2}\nflow Main(n:number)->number {return __ANSWER__}",
		solution: "Double(n)",
		hint:     "Call it just like the supplied square function: Double(n).",
		examples: []lessonExample{
			{code: "Double(3)", want: "6", setup: "flow Double(value:number)->number {return value*2}"},
			{code: "Double(-1)", want: "-2", setup: "flow Double(value:number)->number {return value*2}"},
		},
		cases: []lessonCase{{"{\"n\":3}", "6"}, {"{\"n\":-1}", "-2"}},
	},
	{
		id:       "flow-values",
		title:    "Pass a function to a Flow",
		section:  "Organizing work",
		explain:  "A Flow can receive an ordinary value, including a pure function. Pass the function name; Apply calls it with n.",
		task:     "Pass doubled as the second argument to Apply.",
		template: "fn doubled(x:number)->number {x*2}\nflow Apply(value:number, transform:function)->number {return transform(value)}\nflow Main(n:number)->number {return Apply(n, __ANSWER__)}",
		solution: "doubled",
		hint:     "Fill in doubled without parentheses. doubled is a function value; doubled(n) is its numeric result.",
		examples: []lessonExample{
			{code: "Apply(3, doubled)", want: "6", setup: "fn doubled(x:number)->number {x*2}\nflow Apply(value:number, transform:function)->number {return transform(value)}"},
			{code: "Apply(-1, fn(x:number)->number{x+2})", want: "1", setup: "flow Apply(value:number, transform:function)->number {return transform(value)}"},
		},
		cases: []lessonCase{{"{\"n\":3}", "6"}, {"{\"n\":-1}", "-2"}},
	},
	{
		id:       "pipeline",
		title:    "Send a value to the next stage",
		section:  "Organizing work",
		explain:  "|> passes its left value as the first argument to the next stage.",
		task:     "Use |> to send n into Double.",
		template: "flow Double(value:number)->number {return value*2}\nflow Main(n:number)->number {return __ANSWER__}",
		solution: "n |> Double",
		hint:     "Put n on the left of |> and Double on the right.",
		examples: []lessonExample{
			{code: "3 |> Double", want: "6", setup: "flow Double(value:number)->number {return value*2}"},
			{code: "0 |> Double", want: "0", setup: "flow Double(value:number)->number {return value*2}"},
		},
		cases: []lessonCase{{"{\"n\":3}", "6"}, {"{\"n\":0}", "0"}},
	},
	{
		id:       "flows",
		title:    "Connect two stages",
		section:  "Organizing work",
		explain:  "Pipeline stages finish in order. The previous result fills the first parameter; named arguments fill the rest.",
		task:     "Double n, then add 3 with adjust. Join both stages using |> twice.",
		template: "fn adjust(value:number,delta:number)->number {value+delta}\nflow Double(value:number)->number {return value*2}\nflow Main(n:number)->number {return __ANSWER__}",
		solution: "n |> Double |> adjust(delta=3)",
		hint:     "Start with n |> Double, then append |> adjust(delta=3).",
		examples: []lessonExample{
			{code: "2 |> Double |> adjust(delta=3)", want: "7", setup: "fn adjust(value:number,delta:number)->number {value+delta}\nflow Double(value:number)->number {return value*2}"},
			{code: "(-3) |> Double |> adjust(delta=3)", want: "-3", setup: "fn adjust(value:number,delta:number)->number {value+delta}\nflow Double(value:number)->number {return value*2}"},
		},
		cases: []lessonCase{{"{\"n\":2}", "7"}, {"{\"n\":-3}", "-3"}},
	},
	{
		id:       "entries",
		title:    "Use a fixed value at an entry",
		section:  "Organizing work",
		explain:  "Top-level literal Data can be shared by functions and Flows. With several root Flows and no Main, select the one to run.",
		task:     "This lesson runs Add. Return n plus the fixed OFFSET of 2.",
		template: "OFFSET:number=2\nflow Add(n:number)->number {return __ANSWER__}\nflow Empty()->void {}",
		solution: "n + OFFSET",
		hint:     "Use n + OFFSET. The lesson chooses Add for you; you do not need to write any CLI flags.",
		examples: []lessonExample{
			{code: "3 + OFFSET", want: "5", setup: "OFFSET:number=2"},
			{code: "(-2) + OFFSET", want: "0", setup: "OFFSET:number=2"},
		},
		cases:      []lessonCase{{"{\"positional\":[3],\"named\":{}}", "5"}, {"{\"positional\":[],\"named\":{\"n\":-2}}", "0"}},
		entry:      "Add",
		entryInput: true,
	},
	{
		id:       "modules",
		title:    "Reuse an exported module",
		section:  "Organizing work",
		explain:  "import lip reuses source. Only export declarations are visible to callers. This lesson includes the dependency for you.",
		task:     "Return {total: ..., base: ...}, using pricing.Adjust for total and pricing.BASE for base.",
		template: "import lip \"./lib/pricing.lip\" as pricing\nflow Price(n:number)->object {return __ANSWER__}",
		solution: "{total:n |> pricing.Adjust, base:pricing.BASE}",
		hint:     "Use n |> pricing.Adjust and pricing.BASE. The helper named add is private.",
		examples: []lessonExample{
			{code: "3 |> pricing.Adjust", want: "5", setup: "import lip \"./lib/pricing.lip\" as pricing"},
			{code: "pricing.BASE", want: "2", setup: "import lip \"./lib/pricing.lip\" as pricing"},
		},
		cases:   []lessonCase{{"{\"n\":3}", "{\"base\":2,\"total\":5}"}, {"{\"n\":-2}", "{\"base\":2,\"total\":0}"}},
		sources: map[string]script.Source{"lib/pricing.lip": {Version: "1", Text: "export BASE:number=2\nfn add(x:number)->number {x+BASE}\nexport flow Adjust(x:number)->number {return add(x)}"}},
	},
	{
		id:       "claim-add",
		title:    "Add one fact",
		section:  "Collecting results",
		explain:  "A Claim collects facts. Reading it returns records, and each record has a value field.",
		task:     "Append n to facts. Fill only the value after <+.",
		template: "flow Main(n:number)->list {\n    facts = claim()\n    facts <+ __ANSWER__\n    return [row.value for row in facts]\n}",
		solution: "n",
		hint:     "Use n. <+ appends a contribution; it does not replace earlier facts.",
		examples: []lessonExample{
			{code: "5 |> Collect", want: "[5]", setup: "flow Collect(n:number)->list {\n    facts = claim()\n    facts <+ n\n    return [row.value for row in facts]\n}"},
			{code: "0 |> Collect", want: "[0]", setup: "flow Collect(n:number)->list {\n    facts = claim()\n    facts <+ n\n    return [row.value for row in facts]\n}"},
		},
		cases: []lessonCase{{"{\"n\":5}", "[5]"}, {"{\"n\":0}", "[0]"}},
	},
	{
		id:       "claim-read",
		title:    "Read the collected values",
		section:  "Collecting results",
		explain:  "A complete Claim read waits for its producers. Project .value to get ordinary business data.",
		task:     "Return just the value of each record in facts.",
		template: "flow Main(xs:list)->list {\n    facts = claim()\n    for x in xs {facts <+ x}\n    return __ANSWER__\n}",
		solution: "[row.value for row in facts]",
		hint:     "Use a list comprehension: [row.value for row in facts].",
		examples: []lessonExample{
			{code: "[3,1] |> Collect", want: "[3,1]", setup: "flow Collect(xs:list)->list {\n    facts = claim()\n    for x in xs {facts <+ x}\n    return [row.value for row in facts]\n}"},
			{code: "[] |> Collect", want: "[]", setup: "flow Collect(xs:list)->list {\n    facts = claim()\n    for x in xs {facts <+ x}\n    return [row.value for row in facts]\n}"},
		},
		cases: []lessonCase{{"{\"xs\":[3,1]}", "[3,1]"}, {"{\"xs\":[]}", "[]"}},
	},
	{
		id:       "claim",
		title:    "Collect parallel results",
		section:  "Collecting results",
		explain:  "parallel lets independent iterations overlap. Claim records retain input order even when completion order differs.",
		task:     "Inside the supplied parallel loop, append x * x to facts.",
		template: "flow Squares(xs:list)->list {\n    facts = claim()\n    for x in xs parallel {\n        __ANSWER__\n    }\n    return [row.value for row in facts]\n}",
		solution: "facts <+ x * x",
		hint:     "Write facts <+ x * x. The loop and complete read are already supplied.",
		examples: []lessonExample{
			{code: "[3,1,2] |> Squares", want: "[9,1,4]", setup: "flow Squares(xs:list)->list {\n    facts = claim()\n    for x in xs parallel {facts <+ x*x}\n    return [row.value for row in facts]\n}"},
			{code: "[] |> Squares", want: "[]", setup: "flow Squares(xs:list)->list {\n    facts = claim()\n    for x in xs parallel {facts <+ x*x}\n    return [row.value for row in facts]\n}"},
		},
		cases: []lessonCase{{"{\"xs\":[3,1,2]}", "[9,1,4]"}, {"{\"xs\":[]}", "[]"}},
	},
	{
		id:       "request",
		title:    "Build an external request",
		section:  "Making requests",
		explain:  "effect makes an immutable request value without calling the library. perform executes it later.",
		task:     "Fill the pure function with a request to text.ToUpper(name). Use effect, not perform.",
		template: "import go \"strings\" as text\nfn upper_request(name:string)->effect {__ANSWER__}\nflow Main(name:string)->string {return perform upper_request(name)}",
		solution: "effect text.ToUpper(name)",
		hint:     "Start with effect, followed by text.ToUpper(name). The supplied Flow executes the request.",
		examples: []lessonExample{
			{code: "perform effect text.ToUpper(\"ada\")", want: "\"ADA\"", setup: "import go \"strings\" as text"},
			{code: "perform effect text.ToUpper(\"lip\")", want: "\"LIP\"", setup: "import go \"strings\" as text"},
		},
		cases: []lessonCase{{"{\"name\":\"Ada\"}", "\"ADA\""}, {"{\"name\":\"lip\"}", "\"LIP\""}},
		demo:  true,
	},
	{
		id:       "effect",
		title:    "Execute a saved request",
		section:  "Making requests",
		explain:  "The Host supplies the operation and checks access. This lesson includes a registered Go strings.ToUpper operation.",
		task:     "Execute upper_request(name) with perform.",
		template: "import go \"strings\" as text\nfn upper_request(name:string)->effect {effect text.ToUpper(name)}\nflow Upper(name:string)->string {return __ANSWER__}",
		solution: "perform upper_request(name)",
		hint:     "Put perform before upper_request(name). Calling the pure builder alone only returns a request.",
		examples: []lessonExample{
			{code: "perform upper_request(\"ada\")", want: "\"ADA\"", setup: "import go \"strings\" as text\nfn upper_request(name:string)->effect {effect text.ToUpper(name)}"},
			{code: "perform upper_request(\"lip\")", want: "\"LIP\"", setup: "import go \"strings\" as text\nfn upper_request(name:string)->effect {effect text.ToUpper(name)}"},
		},
		cases: []lessonCase{{"{\"name\":\"Ada\"}", "\"ADA\""}, {"{\"name\":\"lip\"}", "\"LIP\""}},
		demo:  true,
	},
	{
		id:       "effect-values",
		title:    "Pass a request through a function",
		section:  "Making requests",
		explain:  "Requests can travel through fn and Flow parameters/results. Passing one never executes it; Host, entry JSON, and Claim boundaries still require Data.",
		task:     "Return request from forward unchanged. Do not execute it inside fn.",
		template: "import go \"strings\" as text\nfn forward(request:effect)->effect {__ANSWER__}\nflow Main(name:string)->string {\n    request = effect text.ToUpper(name)\n    return perform forward(request)\n}",
		solution: "request",
		hint:     "The final expression is simply request. fn can return the request value but cannot perform it.",
		examples: []lessonExample{
			{code: "perform forward(effect text.ToUpper(\"ada\"))", want: "\"ADA\"", setup: "import go \"strings\" as text\nfn forward(request:effect)->effect {request}"},
			{code: "perform forward(effect text.ToUpper(\"lip\"))", want: "\"LIP\"", setup: "import go \"strings\" as text\nfn forward(request:effect)->effect {request}"},
		},
		cases: []lessonCase{{"{\"name\":\"Ada\"}", "\"ADA\""}, {"{\"name\":\"lip\"}", "\"LIP\""}},
		demo:  true,
	},
	{
		id:       "effect-parallel",
		title:    "Run known read requests in parallel",
		section:  "Making requests",
		explain:  "This lesson's teaching Host registers ToUpper as a concurrent read. A known request builder keeps its operation summary; a generic forwarding parameter cannot prove parallel safety.",
		task:     "Fill value with perform upper_request(name). The rest of the collection is supplied.",
		template: "import go \"strings\" as text\nfn upper_request(name:string)->effect {effect text.ToUpper(name)}\nflow Main(names:list)->list {\n    facts = claim()\n    for name in names parallel {\n        value = __ANSWER__\n        facts <+ value\n    }\n    return [row.value for row in facts]\n}",
		solution: "perform upper_request(name)",
		hint:     "Use perform upper_request(name), just as in the effect lesson.",
		examples: []lessonExample{
			{code: "[\"ada\",\"lip\"] |> UpperAll", want: "[\"ADA\",\"LIP\"]", setup: "import go \"strings\" as text\nfn upper_request(name:string)->effect {effect text.ToUpper(name)}\nflow UpperAll(names:list)->list {\n    facts = claim()\n    for name in names parallel {\n        value = perform upper_request(name)\n        facts <+ value\n    }\n    return [row.value for row in facts]\n}"},
			{code: "[] |> UpperAll", want: "[]", setup: "import go \"strings\" as text\nfn upper_request(name:string)->effect {effect text.ToUpper(name)}\nflow UpperAll(names:list)->list {\n    facts = claim()\n    for name in names parallel {\n        value = perform upper_request(name)\n        facts <+ value\n    }\n    return [row.value for row in facts]\n}"},
		},
		cases: []lessonCase{{"{\"names\":[\"ada\",\"lip\"]}", "[\"ADA\",\"LIP\"]"}, {"{\"names\":[]}", "[]"}},
		demo:  true,
	},
	{
		id:       "react",
		title:    "Use one result in the next request",
		section:  "Making requests",
		explain:  "After a call finishes, its result is ordinary Data. Use it to prepare the next call: call, observe, then decide again.",
		task:     "Execute a second ToUpper request, passing draft + \"!\" as its argument.",
		template: "import go \"strings\" as text\nflow Refine(name:string)->string {\n    draft = perform effect text.ToUpper(name)\n    return __ANSWER__\n}",
		solution: "perform effect text.ToUpper(draft + \"!\")",
		hint:     "Use perform effect text.ToUpper(draft + \"!\"). draft is already the first call's string result.",
		examples: []lessonExample{
			{code: "perform effect text.ToUpper(\"ADA\" + \"!\")", want: "\"ADA!\"", setup: "import go \"strings\" as text"},
			{code: "perform effect text.ToUpper(\"LIP\" + \"!\")", want: "\"LIP!\"", setup: "import go \"strings\" as text"},
		},
		cases: []lessonCase{{"{\"name\":\"Ada\"}", "\"ADA!\""}, {"{\"name\":\"lip\"}", "\"LIP!\""}},
		demo:  true,
	},
	{
		id:       "power",
		title:    "Square a number",
		section:  "Extra practice",
		explain:  "A bound negative value can be the base of a power. Written -3 ** 2 means -(3 ** 2), so parenthesize a negative literal.",
		task:     "Return x squared, including when x is negative.",
		template: "flow Square(x:number)->number {return __ANSWER__}",
		solution: "x ** 2",
		hint:     "** means power. Use x ** 2; (-3) ** 2 is 9.",
		examples: []lessonExample{
			{code: "(-3) ** 2", want: "9"},
			{code: "-3 ** 2", want: "-9"},
		},
		cases: []lessonCase{{"{\"x\":-3}", "9"}, {"{\"x\":2}", "4"}},
	},
	{
		id:       "logarithm",
		title:    "Find a power from its result",
		section:  "Extra practice",
		explain:  "The */ operator answers: what power of the right-hand base produces the left-hand value?",
		task:     "Return the logarithm of value using base as the base.",
		template: "flow Log(value:number,base:number)->number {return __ANSWER__}",
		solution: "value */ base",
		hint:     "Use value */ base. For example, 8 */ 2 is 3 because 2 ** 3 is 8.",
		examples: []lessonExample{
			{code: "8 */ 2", want: "3"},
			{code: "0.25 */ 2", want: "-2"},
		},
		cases: []lessonCase{{"{\"value\":8,\"base\":2}", "3"}, {"{\"value\":0.25,\"base\":2}", "-2"}},
	},
	{
		id:       "slicing",
		title:    "Combine list selections",
		section:  "Extra practice",
		explain:  "This combines earlier list skills. Index lists keep order and duplicates; string slices count Unicode code points, not UTF-8 bytes.",
		task:     "Return {picked, reverse, stride, text}: select xs at indices, reverse xs, take xs[1::2], and reverse label.",
		template: "flow Select(xs:list,indices:list,label:string)->object {return __ANSWER__}",
		solution: "{picked:xs[indices], reverse:xs[::-1], stride:xs[1::2], text:label[::-1]}",
		hint:     "Use {picked:xs[indices], reverse:xs[::-1], stride:xs[1::2], text:label[::-1]}. Try slice and reverse first if needed.",
		examples: []lessonExample{
			{code: "[10,20,30][[2,0,2]]", want: "[30,10,30]"},
			{code: "\"a𐀀b\"[::-1]", want: "\"b𐀀a\""},
		},
		cases: []lessonCase{{"{\"xs\":[0,1,2,3,4],\"indices\":[-1,0,-1],\"label\":\"a𐀀b\"}", "{\"picked\":[4,0,4],\"reverse\":[4,3,2,1,0],\"stride\":[1,3],\"text\":\"b𐀀a\"}"}, {"{\"xs\":[],\"indices\":[],\"label\":\"\"}", "{\"picked\":[],\"reverse\":[],\"stride\":[],\"text\":\"\"}"}},
	},
}

type progress struct {
	Version   int             `json:"version"`
	Current   string          `json:"current"`
	Completed map[string]bool `json:"completed"`
}

func lessonIndex(id string) int {
	if number, err := strconv.Atoi(id); err == nil && number > 0 && number <= len(course) {
		return number - 1
	}
	for i, lesson := range course {
		if lesson.id == id {
			return i
		}
	}
	return -1
}

type lessonAttempt struct {
	text string
	full bool
}

const learnCommands = ":show :examples :tests :hint :demo :edit :submit :retry :draft :cancel :next :prev :lesson :list :status :help :quit :exit"

func lessonSource(l lesson, answer string) string {
	return strings.ReplaceAll(l.template, answerMark, strings.TrimSpace(answer))
}

func showLesson(l lesson, index int, completed bool) {
	status := ""
	if completed {
		status = " · completed"
	}
	fmt.Printf("\nLesson %d/%d · %s (%s)%s\n%s\n\n%s\n", index+1, len(course), l.title, l.id, status, l.section, l.explain)
	if l.demo {
		fmt.Println("This lesson uses the teaching Host. To run its code in a file: lipc run -host demo FILE.")
	}
	if len(l.examples) > 0 {
		example := l.examples[0]
		fmt.Printf("Example: %s  =>  %s\n", example.code, example.want)
		if example.setup != "" {
			fmt.Println("Use :examples to see and run the helper code for this example.")
		}
	}
	fmt.Printf("\nYour task: %s\n", l.task)
	ids := make([]string, 0, len(l.sources))
	for id := range l.sources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Printf("\n# %s (included, read-only example)\n%s\n", id, l.sources[id].Text)
	}
	fmt.Printf("\n# main.lip\n%s\n", l.template)
	if len(l.cases) > 0 {
		fmt.Printf("\nInput: %s\nExpected result: %s\n", l.cases[0].input, l.cases[0].want)
	}
	fmt.Println("Type only the code for __ANSWER__, then press Enter. :hint helps; :examples shows more.")
}

func showLessonTests(l lesson) {
	for i, test := range l.cases {
		fmt.Printf("Test %d: input %s  =>  %s\n", i+1, test.input, test.want)
	}
}

func showLessonExamples(ctx context.Context, l lesson, trace bool) error {
	for i, example := range l.examples {
		fmt.Printf("\nExample %d\n", i+1)
		if example.setup != "" {
			fmt.Println(example.setup)
		}
		fmt.Printf("%s  =>  %s\n", example.code, example.want)
		sample := l
		sample.entry, sample.entryInput = "", false
		sample.cases = []lessonCase{{"{}", example.want}}
		source := example.setup + "\nflow Main()->any {return " + example.code + "}"
		if err := checkLesson(ctx, sample, source, trace); err != nil {
			return fmt.Errorf("example %d: %w", i+1, err)
		}
	}
	fmt.Println("Examples do not mark a lesson complete. Try your own answer when ready.")
	return nil
}

// Extract visible code names without evaluating code or suggesting words from
// comments/string literals. This includes lesson parameters and request builders.
func lessonNames(source string) []string {
	var names []string
	for i := 0; i < len(source); {
		if source[i] == '#' {
			for i < len(source) && source[i] != '\n' {
				i++
			}
			continue
		}
		if source[i] == '"' {
			quote := "\""
			if strings.HasPrefix(source[i:], "\"\"\"") {
				quote = "\"\"\""
			}
			i += len(quote)
			for i < len(source) {
				if source[i] == '\\' {
					i += 2
				} else if strings.HasPrefix(source[i:], quote) {
					i += len(quote)
					break
				} else {
					i++
				}
			}
			continue
		}
		start := i
		for i < len(source) && (source[i] >= 'a' && source[i] <= 'z' || source[i] >= 'A' && source[i] <= 'Z' || source[i] == '_' || i > start && (source[i] >= '0' && source[i] <= '9' || source[i] == '.')) {
			i++
		}
		if i > start {
			name := source[start:i]
			if name != answerMark {
				names = append(names, name)
			}
		} else {
			i++
		}
	}
	return names
}

func lessonProgram(source string) bool {
	// Recognize declaration starts, not arbitrary keyword prefixes. In
	// particular, an anonymous fn can be a valid expression for the blank.
	rest := source
	skip := func() {
		for {
			rest = strings.TrimLeft(rest, " \t\r\n;")
			if !strings.HasPrefix(rest, "#") {
				return
			}
			_, rest, _ = strings.Cut(rest, "\n")
		}
	}
	identifier := func() string {
		skip()
		n := 0
		for n < len(rest) {
			c := rest[n]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || n > 0 && c >= '0' && c <= '9') {
				break
			}
			n++
		}
		name := rest[:n]
		rest = rest[n:]
		return name
	}
	name := identifier()
	switch name {
	case "flow", "import", "export":
		return true
	case "fn":
		return identifier() != ""
	}
	skip()
	return name != "" && strings.HasPrefix(rest, ":")
}

func showLessonError(err error, source string, l lesson) {
	fmt.Fprintln(os.Stderr, err)
	var diagnostic *script.Diagnostic
	if errors.As(err, &diagnostic) {
		text := source
		if dependency, ok := l.sources[diagnostic.SourceID]; ok {
			text = dependency.Text
		}
		lines := strings.Split(text, "\n")
		if diagnostic.Line > 0 && diagnostic.Line <= len(lines) {
			fmt.Fprintf(os.Stderr, "  %d | %s\n", diagnostic.Line, lines[diagnostic.Line-1])
		}
		switch diagnostic.Code {
		case "E-LEX", "E-PARSE":
			fmt.Fprintln(os.Stderr, "Check quotes, parentheses, and braces. Fill only __ANSWER__, or paste a complete program.")
		case "E-NAME":
			fmt.Fprintln(os.Stderr, "Check the spelling and capitalization of names in the template. Commands start with a colon, as in :help.")
		case "E-TYPE", "E-SCHEMA":
			fmt.Fprintln(os.Stderr, "Check the requested result type. Text needs quotes; numbers and true/false do not.")
		case "E-PURITY":
			fmt.Fprintln(os.Stderr, "fn computes values, builds requests, and may use print/log/telemetry for diagnostics. Execute requests with perform inside a Flow.")
		case "E-PARALLEL":
			fmt.Fprintln(os.Stderr, "Each possible request operation must have a Host contract allowing concurrent reads.")
		}
	}
	fmt.Fprintln(os.Stderr, "Your answer is saved. :retry edits it; :draft shows it; :hint gives a smaller step.")
}

func learn(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("learn", flag.ContinueOnError)
	list := flags.Bool("list", false, "list lessons in learning order")
	start := flags.String("lesson", "", "start at a lesson ID or number (see -list)")
	path := flags.String("progress", "", "save/resume progress at this JSON path (default: no files)")
	trace := flags.Bool("trace", true, "show best-effort print/log/telemetry diagnostics on stderr")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("use lipc learn -lesson ID to choose a lesson, then enter code at the prompt")
	}
	if *list {
		for i, l := range course {
			fmt.Printf("%2d  %-16s %s [%s]\n", i+1, l.id, l.title, l.section)
		}
		return nil
	}
	state := progress{Version: 1, Completed: map[string]bool{}}
	if *path != "" {
		data, err := os.ReadFile(*path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			if err := json.Unmarshal(data, &state); err != nil {
				return fmt.Errorf("invalid learning progress: %w", err)
			}
			if state.Version != 1 {
				return fmt.Errorf("unsupported learning progress version")
			}
			if state.Completed == nil {
				state.Completed = map[string]bool{}
			}
		}
	}
	index := 0
	selection := *start
	if selection == "" {
		selection = state.Current
	}
	if selection != "" {
		index = lessonIndex(selection)
		if index < 0 {
			return fmt.Errorf("unknown lesson %q; lipc learn -list shows lesson IDs", selection)
		}
	}
	fmt.Println("LIP 1.0 · Learn by small steps")
	fmt.Println("Each lesson has one blank. Type the missing code and press Enter; no whole program needed.")
	fmt.Println(":hint gives a small step; :examples runs worked examples; :demo shows one possible answer.")
	if *path == "" {
		fmt.Println("Progress stays in this session. Add -progress FILE next time to save it; :quit or Ctrl-D exits.")
	} else {
		fmt.Printf("Progress file: %s. Completed lessons are kept by ID. :quit or Ctrl-D exits.\n", *path)
	}
	save := func() error {
		if *path == "" {
			return nil
		}
		state.Current = course[index].id
		data, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return err
		}
		dir := filepath.Dir(*path)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		file, err := os.CreateTemp(dir, ".lip-learn-*")
		if err != nil {
			return err
		}
		name := file.Name()
		defer os.Remove(name)
		if _, err = file.Write(append(data, '\n')); err != nil {
			file.Close()
			return err
		}
		if err = file.Close(); err != nil {
			return err
		}
		return os.Rename(name, *path)
	}
	show := func() { showLesson(course[index], index, state.Completed[course[index].id]) }
	show()
	completionSession, err := script.NewSession(script.Host{})
	if err != nil {
		return err
	}
	defer completionSession.Close()
	var draft string
	editing, revising := false, false
	attempts := map[string]lessonAttempt{}
	hints := map[string]int{}
	input := newInteractiveInput(ctx, func(prefix string) []string {
		names := completionSession.Complete(prefix)
		l := course[index]
		for _, name := range lessonNames(l.template + "\n" + draft) {
			if strings.HasPrefix(name, prefix) {
				names = append(names, name)
			}
		}
		return commandCompletions(prefix, names, learnCommands)
	})
	for ctx.Err() == nil {
		prompt := ""
		if input.editor != nil {
			prompt = "learn> "
			if editing {
				prompt = "program> "
			} else if draft != "" {
				prompt = "  ...> "
			}
		}
		line, readErr := input.read(ctx, prompt)
		if errors.Is(readErr, errInputCanceled) {
			draft, editing, revising = "", false, false
			fmt.Println("Input cleared. Your last submitted answer is still available with :retry.")
			continue
		}
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		text := strings.TrimSpace(line)
		l := course[index]
		var source, answer string
		submit, demo, full := false, false, editing
		finishDraft := func() {
			answer = strings.TrimSpace(draft)
			full = editing || lessonProgram(answer)
			source = answer
			if !full {
				source = lessonSource(l, answer)
			}
			submit = true
		}
		ready := func() bool {
			return readErr == io.EOF || !script.CellIncomplete(draft) && (text == "" || !script.CellAwaitingElse(draft))
		}
		switch {
		case script.CellInMultilineString(draft):
			draft += line
			if !editing && ready() {
				finishDraft()
			}
		case text == ":quit" || text == ":exit":
			return save()
		case text == ":help":
			fmt.Println("Fill __ANSWER__ and press Enter. Open brackets or triple-quoted strings continue on the next line.")
			fmt.Println("An if without else waits for the next line; enter else, or :submit to check the current code.")
			fmt.Println(":show review this lesson; :examples run two small examples; :tests show inputs and expected results.")
			fmt.Println(":hint get increasing help (idea, example, answer); :demo run a complete answer without marking it complete.")
			fmt.Println(":draft show your current/last answer; :retry edit your last answer in place (terminal), or type a replacement (pipe).")
			fmt.Println(":edit start a whole program; :submit check it; :cancel leave program mode/discard current input.")
			fmt.Println(":next / :prev move freely; :lesson ID or number jump; :list / :status show progress; :quit exit.")
			fmt.Println(editorHelp)
		case text == ":show":
			show()
		case text == ":tests":
			showLessonTests(l)
		case text == ":examples":
			if err := showLessonExamples(ctx, l, *trace); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		case text == ":hint":
			hints[l.id]++
			switch hints[l.id] {
			case 1:
				fmt.Printf("Hint 1: %s\nEnter :hint again for a worked example.\n", l.hint)
			case 2:
				example := l.examples[0]
				fmt.Println("Hint 2:")
				if example.setup != "" {
					fmt.Println(example.setup)
				}
				fmt.Printf("%s  =>  %s\nEnter :hint again for one possible answer.\n", example.code, example.want)
			default:
				fmt.Printf("One possible answer for __ANSWER__:\n%s\nYou can type it to try, or use :demo to see the complete program.\n", l.solution)
			}
		case text == ":demo":
			source = lessonSource(l, l.solution)
			fmt.Println(source)
			submit, demo = true, true
		case text == ":draft":
			text := draft
			if text == "" {
				text = attempts[l.id].text
			}
			if text == "" {
				fmt.Println("No answer yet. Type the missing code, or enter :hint.")
			} else {
				for i, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
					fmt.Printf("%3d | %s\n", i+1, line)
				}
			}
		case text == ":retry":
			attempt, ok := attempts[l.id]
			if !ok {
				fmt.Println("No submitted answer to edit yet. Type an answer, or use :hint.")
				break
			}
			if draft != "" || editing {
				fmt.Fprintln(os.Stderr, "Current input is still open. Use :submit to check it or :cancel before :retry.")
				break
			}
			editing = attempt.full
			if input.editor != nil {
				input.initial = attempt.text
				revising = attempt.full
				fmt.Println("Your last answer is loaded. Edit it, then press Enter to check it. Alt-Enter adds a line.")
			} else {
				fmt.Printf("Last answer:\n%s\n", attempt.text)
				if editing {
					fmt.Println("Enter a replacement program, then :submit.")
				} else {
					fmt.Println("Enter a replacement answer, then press Enter.")
				}
			}
		case text == ":cancel":
			draft, editing, revising = "", false, false
			fmt.Println("Current input cleared. Type a fresh answer; :retry restores the last submitted one.")
		case text == ":edit":
			if draft != "" || editing {
				fmt.Fprintln(os.Stderr, "Current input is still open. Use :submit or :cancel first.")
				break
			}
			editing, revising = true, false
			fmt.Println("Whole-program mode: enter or paste a complete program, then :submit. :cancel returns to the blank.")
		case text == ":submit":
			if strings.TrimSpace(draft) == "" {
				fmt.Println("Nothing to check yet. Type the missing code, or use :edit to write a whole program.")
			} else {
				finishDraft()
			}
		case text == ":status" || text == ":list":
			done := 0
			for i, item := range course {
				mark := " "
				if state.Completed[item.id] {
					mark, done = "✓", done+1
				}
				current := ""
				if i == index {
					current = " <- current"
				}
				fmt.Printf("%s %2d %-16s %s%s\n", mark, i+1, item.id, item.title, current)
			}
			fmt.Printf("Completed %d/%d. :lesson ID or number opens a lesson.\n", done, len(course))
		case text == ":next" || text == ":prev" || text == ":lesson" || strings.HasPrefix(text, ":lesson "):
			if editing || draft != "" {
				fmt.Fprintln(os.Stderr, "Current input is still open. Use :submit to check it or :cancel before changing lessons.")
				break
			}
			newIndex := index
			if text == ":next" {
				if index == len(course)-1 {
					fmt.Println("This is the last lesson. :status shows what you have completed; :lesson ID reviews any lesson.")
					break
				}
				newIndex++
			} else if text == ":prev" {
				if index == 0 {
					fmt.Println("This is the first lesson. Try an answer, :hint, or :next.")
					break
				}
				newIndex--
			} else {
				newIndex = lessonIndex(strings.TrimSpace(strings.TrimPrefix(text, ":lesson")))
			}
			if newIndex < 0 || newIndex >= len(course) {
				fmt.Fprintln(os.Stderr, "Choose a lesson ID or number, such as :lesson hello or :lesson 1. :list shows all lessons.")
			} else {
				index = newIndex
				show()
				if err := save(); err != nil {
					return err
				}
			}
		case strings.HasPrefix(text, ":"):
			fmt.Fprintln(os.Stderr, "Unknown command. Try :hint for help with the exercise, or :help for commands.")
		case text != "" || draft != "":
			draft += line
			if revising || !editing && ready() {
				finishDraft()
			}
		}
		if submit {
			if !demo {
				attempts[l.id] = lessonAttempt{text: answer, full: full}
			}
			if err := checkLesson(ctx, l, source, *trace); err != nil {
				if demo {
					fmt.Fprintln(os.Stderr, err)
				} else {
					showLessonError(err, source, l)
				}
			} else if demo {
				fmt.Println("Example only; progress is unchanged. Type your answer to complete this lesson, or use :next.")
			} else {
				state.Completed[l.id] = true
				fmt.Println("Passed. :next opens the next lesson; :retry edits your answer; :show reviews this one.")
				if err := save(); err != nil {
					return err
				}
			}
			if !demo {
				draft, editing, revising = "", false, false
			}
		}
		if readErr == io.EOF {
			return save()
		}
	}
	return ctx.Err()
}

func checkLesson(ctx context.Context, l lesson, source string, trace bool) error {
	host := script.Host{}
	if l.demo {
		var err error
		host, err = script.DemoHost()
		if err != nil {
			return err
		}
	}
	defer host.Close()
	sources := map[string]script.Source{"main.lip": {Version: "local", Text: source}}
	for id, dependency := range l.sources {
		sources[id] = dependency
	}
	program, err := script.CompileSourceSet(script.SourceSet{Root: "main.lip", Sources: sources}, host, script.CompileOptions{Entry: l.entry})
	if err != nil {
		return err
	}
	options := interactiveOptions()
	if trace {
		var stop func()
		options.Trace, stop = script.StartTraceOutput(os.Stderr)
		defer stop()
	}
	for i, test := range l.cases {
		value, err := script.DecodeData([]byte(test.input))
		if err != nil {
			return err
		}
		var report script.Report
		if l.entryInput {
			report, err = program.RunEntry(ctx, value, options)
		} else {
			inputs, _ := value.Fields()
			report, err = program.Run(ctx, inputs, options)
		}
		if err != nil {
			return fmt.Errorf("Case %d input %s: %w", i+1, test.input, err)
		}
		want, err := script.DecodeData([]byte(test.want))
		if err != nil {
			return err
		}
		equal, err := script.Equal(report.Output, want)
		if err != nil {
			return err
		}
		if !equal {
			return fmt.Errorf("Case %d input %s: got %s, expected %s", i+1, test.input, showValue(report.Output), test.want)
		}
		fmt.Printf("Case %d ✓ %s\n", i+1, showValue(report.Output))
	}
	return nil
}
