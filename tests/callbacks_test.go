package tests

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lipalpha/compiler"
)

func TestInlineCallbackChecksAndDependencies(t *testing.T) {
	for _, source := range []string{
		`print(list.map(range(4), fn(x) { x * x }))`,
		`fn square(x:number) {x*x}; print(list.map(range(4),square))`,
		`fn square(x:number)->number {x*x;}; print(list.map(range(4),square))`,
		`print(list.map(range(4), fn(x: number) -> number { return x * x; }))`,
		`print(fold(range(4), 0, fn(total, x) { total + x }))`,
		`fn scaled(n:number)->list { return list.map(range(n),fn(x){x*n}) }; print(scaled(3))`,
		`print(list.map(range(3), fn(x) { list.map(range(x), fn(y) { x + y }) }))`,
		`print([list.map(range(x),fn(y){x+y}) for x in range(3)])`,
		`print(list.map(range(3),fn(x){[x+y for y in range(x)]}))`,
		`print(list.scan(range(3),0,fn(total:number,x:number)->number{return total+x}))`,
	} {
		if _, err := compiler.ParseAndBuild(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	for _, tc := range []struct{ source, code, want string }{
		{`print(list.map([],fn(){1}))`, "LIP_CALLBACK_ERROR", "1 parameter, got 0"},
		{`print(fold([],0,fn(x){x}))`, "LIP_CALLBACK_ERROR", "2 parameters, got 1"},
		{`print(list.filter([],fn(x){1}))`, "LIP_CALLBACK_ERROR", "must return bool, got number"},
		{`print(list.sort_by([],fn(x){true}))`, "LIP_CALLBACK_ERROR", "must return number or string, got bool"},
		{`print(list.map([],fn(x)->number{"bad"}))`, "LIP_CALLBACK_ERROR", "returns string, declared number"},
		{`print(list.map([],missing,1))`, "LIP_ARGUMENT_ERROR", "expects 2 arguments, got 3"},
		{`print(list.map([],missing()))`, "LIP_CALLBACK_ERROR", "local pure function"},
		{`print(list.scan([],"bad",fn(total:number,x){total+x}))`, "LIP_CALLBACK_ERROR", "seed has type string, accumulator type is number"},
		{`print(fold([],0,fn(total:number,x){"bad"}))`, "LIP_CALLBACK_ERROR", "accumulator requires number"},
		{`print(list.map([],fn(x){missing}))`, "LIP_NAME_ERROR", `reference "missing"`},
		{`print(list.map([],fn(x){print(x)}))`, "LIP_EFFECT_ERROR", "callback must be pure"},
		{`import python "math" as m; print(list.map([],fn(x){m.sqrt(x)}))`, "LIP_EFFECT_ERROR", "callback must be pure"},
		{`import python "math" as m; fn f()->list {return list.map([],fn(x){m.sqrt(x)})}; print(f())`, "LIP_EFFECT_ERROR", "must be pure"},
		{`a=fn(x){x}`, "LIP_CALLBACK_ERROR", "only supported as a collection callback"},
	} {
		report := compiler.CheckSource("callbacks.lip", tc.source)
		if report.OK || report.Diagnostics[0].Code != tc.code || !strings.Contains(report.Diagnostics[0].Message, tc.want) || len(report.Diagnostics[0].Hints) == 0 {
			t.Fatalf("%s: %+v", tc.source, report)
		}
	}
	for _, tc := range []struct{ source, want string }{
		{`print(list.map([],fn(x,x){x}))`, "duplicate callback parameter"},
		{`import python "math" as m; print(list.map([],fn(m){m}))`, "module alias"},
		{`print(list.map([],fn(x){x; x}))`, "one expression"},
		{`fn recurse(n:number){return list.map(range(n),fn(x){recurse(x)})}; print(recurse(2))`, "explicit return type"},
	} {
		if _, err := compiler.ParseAndBuild(tc.source); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v", tc.source, err)
		}
	}
	graph, err := compiler.ParseAndBuild(`flow Capture(scale:number, values:list)->list { return list.map(values,fn(x){x*scale}) }`)
	if err != nil || !reflect.DeepEqual(graph.Nodes[0].Deps, []string{"values", "scale"}) {
		t.Fatalf("captures are not dependencies: %+v %v", graph, err)
	}
}

func TestGeneratedInlineCallbackComposition(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	source := `fn scale(n:number)->list { list.map(range(3),fn(x){x*n}) }
fn tree(node:object)->number { return node.value + list.sum(list.map(node.children,fn(child:object)->number{tree(child)})) }
flow Aggregations(n:number, factor:number, rows:any, node:object, mode:number)->object {
    mapped = list.map(range(n),fn(x){x*factor})
    return if mode==1 { {typed:list.map(rows,fn(x:number)->number{x*2})} }
    else { if mode==2 { {predicate:list.filter(rows,fn(x){x})} }
    else { if mode==3 { {failed:list.map(range(3),fn(x){1/(x-1)})} }
    else {
        {
            mapped:mapped,
            nested:list.map(range(3),fn(x){list.map(range(x),fn(x){x+factor})}),
            captured:list.map(range(3),fn(x){[x+y for y in range(x)]}),
            mixed:[list.map(range(x),fn(y){x+y}) for x in range(3)],
            transformed:list.sum(list.map(list.filter(range(n),fn(x){x>1}),fn(x){x*x})),
            total:fold(list.map(range(n),fn(x){x*x}),0,fn(total,x){total+x}),
            scan:list.scan(range(n),0,fn(total,x){total+x}),
            groups:list.group_by(range(3),fn(x){x>0}),
            runs:list.split_by([1,2,0,3],fn(x){x>0}),
            sorted:list.sort_by(range(3),fn(x){0-x}),
            any:list.any(range(3),fn(x){if x==0 {true} else {fail("any did not stop")}}),
            all:list.all(range(3),fn(x){if x==0 {false} else {fail("all did not stop")}}),
            empty_map:list.map([],fn(x){fail("empty map")}),
            empty_scan:list.scan([],7,fn(total,x){fail("empty scan")}),
            lazy:if true {[]} else {list.map(range(3),fn(x){fail("unused branch")})},
            shadow:scale(4), tree:tree(node), texts:list.map([" a "," b "],fn(x){string.upper(string.trim(x))})
        }
    } } }
}`
	writeTestFile(t, filepath.Join(dir, "flow.go"), generatedSource(t, source, false))
	writeTestFile(t, filepath.Join(dir, "flow_test.go"), `package main
import("context";"reflect";"strings";"testing";"lipalpha/runtime")
func TestCallbacks(t *testing.T) {
    node:=map[string]runtime.Value{"value":1,"children":[]runtime.Value{map[string]runtime.Value{"value":2,"children":[]runtime.Value{}}}}
    inputs:=map[string]runtime.Value{"n":4,"factor":3,"rows":[]runtime.Value{},"node":node,"mode":0}
    want:="{\"all\":false,\"any\":true,\"captured\":[[],[1],[2,3]],\"empty_map\":[],\"empty_scan\":[7],\"groups\":[{\"key\":false,\"values\":[0]},{\"key\":true,\"values\":[1,2]}],\"lazy\":[],\"mapped\":[0,3,6,9],\"mixed\":[[],[1],[2,3]],\"nested\":[[],[3],[3,4]],\"runs\":[{\"key\":true,\"values\":[1,2]},{\"key\":false,\"values\":[0]},{\"key\":true,\"values\":[3]}],\"scan\":[0,0,1,3,6],\"shadow\":[0,4,8],\"sorted\":[2,1,0],\"texts\":[\"A\",\"B\"],\"total\":14,\"transformed\":13,\"tree\":3}"
    for _,run:=range []func(context.Context,runtime.Host,map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){Run,RunSequential,func(c context.Context,h runtime.Host,v map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){return RunParallel(c,h,v,2)}} {
        got,_,err:=run(context.Background(),runtime.DefaultHost(),inputs)
        if err!=nil {t.Fatal(err)}
        text,err:=runtime.FormatValue(got);if err!=nil||text!=want {t.Fatalf("%s %v",text,err)}
        for _,tc:=range []struct{mode int; rows runtime.Value; want string}{
            {1,[]runtime.Value{1,"bad"},"element 1: callback argument x: expected number, got string"},
            {2,[]runtime.Value{true,1},"callback element 1: expected bool, got number"},
            {3,[]runtime.Value{},"element 1: division by zero"},
        } {
            other:=map[string]runtime.Value{"n":4,"factor":3,"rows":tc.rows,"node":node,"mode":tc.mode}
            _,_,err:=run(context.Background(),runtime.DefaultHost(),other)
            if err==nil||!strings.Contains(err.Error(),tc.want){t.Fatalf("mode %d: %v",tc.mode,err)}
        }
    }
    instance,err:=NewInstance(runtime.DefaultHost(),inputs);if err!=nil{t.Fatal(err)}
    if _,_,err:=instance.Tick(context.Background(),nil);err!=nil{t.Fatal(err)}
    _,trace,err:=instance.Tick(context.Background(),nil);if err!=nil{t.Fatal(err)}
    reused:=false;for _,event:=range trace{if event.Node=="mapped"&&strings.Contains(event.Reason,"reuse"){reused=true}};if !reused{t.Fatal(trace)}
    next,_,err:=instance.Tick(context.Background(),map[string]runtime.Value{"factor":2});if err!=nil{t.Fatal(err)}
    if !reflect.DeepEqual(next.(map[string]runtime.Value)["mapped"],[]runtime.Value{float64(0),float64(2),float64(4),float64(6)}){t.Fatal("capture was omitted from cache dependencies",next)}
    cycle:=map[string]runtime.Value{"value":0};cycle["children"]=[]runtime.Value{cycle}
    inputs["node"]=cycle
    _,_,err=Run(context.Background(),runtime.DefaultHost(),inputs)
    if err==nil||!strings.Contains(err.Error(),"call depth exceeds"){t.Fatalf("recursive inline callback: %v",err)}
}
`)
	command := exec.Command("go", "test", "-race", filepath.Join(dir, "flow.go"), filepath.Join(dir, "flow_test.go"))
	command.Dir = root
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("inline callbacks: %v\n%s", err, out)
	}
}
