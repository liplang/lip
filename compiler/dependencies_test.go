package compiler

import (
	"reflect"
	"testing"
)

func TestOnlyUsedImportsRequireAdapters(t *testing.T) {
	for _, tc := range []struct {
		source       string
		host, python bool
	}{
		{`import host "unused"; import go "math"; import python "not_installed"; print(7)`, false, false},
		{`import host "service.*"; for x in [] { service.emit(x) }`, true, false},
		{`import go "math" as m; print(m.Abs(-1))`, true, false},
		{`import python "math" as m; print(m.pi)`, false, true},
		{`import python "math" as m; for x in [] { print(m.sqrt(x)) }`, false, true},
		{`import python "math" as m; value = feedback(len([]), m.sqrt, m.isfinite, 2)`, false, true},
		{`import python "math"; print(python.module_available("math"))`, false, true},
		{`import python "math"; fn same(x:string){x}; value = feedback(str("math"), same, python.module_available, 2)`, false, true},
		{`import host "python.module_available"; print(python.module_available("math"))`, true, false},
	} {
		source := tc.source
		graph, err := ParseAndBuild(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if graph.RequiresHost() != tc.host || graph.RequiresPython() != tc.python {
			t.Fatalf("%s: host=%v python=%v", source, graph.RequiresHost(), graph.RequiresPython())
		}
	}
	graph, err := ParseAndBuild(`import host "unused"; import host "service.*"; import go "math"; service.emit(1)`)
	if err != nil {
		t.Fatal(err)
	}
	if got := requiredHostOperations(graph); !reflect.DeepEqual(got, []string{"service.emit"}) {
		t.Fatal(got)
	}
	if len(graph.Dependencies) != 3 {
		t.Fatal("unused declarations disappeared")
	}
}
