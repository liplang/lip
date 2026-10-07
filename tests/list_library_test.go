package tests

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lipalpha/compiler"
)

func TestListLibraryDiagnosticsAndDependencies(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{`flow Bad()->list{return list.unknown([])}`, "unknown list operation"},
		{`flow Bad()->list{return list.concat([],1)}`, "expects list"},
		{`flow Bad()->list{return list.transpose()}`, "argument count"},
		{`flow Bad()->list{return list.map([],missing)}`, "local pure function"},
		{`fn f(x:number,y:number)->number{return x+y} flow Bad()->list{return list.map([],f)}`, "1 parameters"},
		{`fn f(x:number)->number{return x} flow Bad()->list{return list.filter([],f)}`, "return bool"},
		{`fn f(x:number)->number{return x} flow Bad()->list{return list.map([],f(1))}`, "local pure function"},
		{`fn add(x:number,y:number)->number{return x+y} flow Bad()->list{return list.scan([],"bad",add)}`, "accumulator type"},
		{`require host "fetch" fn f(x:number)->number{return fetch(x)} flow Bad()->list{return list.map([],f)}`, "must be pure"},
		{`fn tree(node:object){return list.sum(list.map(node.children,tree))} flow Bad(node:object)->number{return tree(node)}`, "explicit return type"},
	} {
		_, err := compiler.ParseAndBuild(tc.source)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v, want %s", tc.source, err, tc.want)
		}
	}
	graph, err := compiler.ParseAndBuild(`fn double(x:number)->number{return x*2} flow Pure(values:list)->list{return list.map(values,double)}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Dependencies) != 0 || len(graph.Nodes[0].Deps) != 1 || graph.Nodes[0].Deps[0] != "values" {
		t.Fatal(graph)
	}
}

func TestListLibraryGeneratedCompositionAndRecursion(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	source := `fn key(row:object)->string{return row.kind}
fn amount(row:object)->number{return row.amount}
fn summarize(group:object)->object{return {kind:group.key,total:list.sum(list.map(group.values,amount))}}
fn tree(node:object)->number{return node.value+list.sum(list.map(node.children,tree))}
flow Lists(rows:list, node:object)->object{
 groups=list.group_by(rows,key)
 return {groups:list.map(groups,summarize), ranked:list.sort_by(rows,amount), tree:tree(node), columns:list.transpose([[1,2],[3,4]])}
}`
	writeTestFile(t, filepath.Join(dir, "flow.go"), generatedSource(t, source, false))
	writeTestFile(t, filepath.Join(dir, "flow_test.go"), `package main
import("context";"errors";"reflect";"strings";"testing";"lipalpha/runtime")
func TestLists(t *testing.T){
 rows:=[]runtime.Value{map[string]runtime.Value{"kind":"A","amount":3},map[string]runtime.Value{"kind":"B","amount":2},map[string]runtime.Value{"kind":"A","amount":-1}}
 node:=map[string]runtime.Value{"value":1,"children":[]runtime.Value{map[string]runtime.Value{"value":2,"children":[]runtime.Value{}}}}
 host:=runtime.DefaultHost();for _,name:=range []string{"list.group_by","list.map","key","amount"}{host.Register(name,func(context.Context,[]runtime.Value)runtime.Result{return runtime.Failed(errors.New("override"))})}
 want:=map[string]runtime.Value{"groups":[]runtime.Value{map[string]runtime.Value{"kind":"A","total":float64(2)},map[string]runtime.Value{"kind":"B","total":float64(2)}},"ranked":[]runtime.Value{rows[2],rows[1],rows[0]},"tree":float64(3),"columns":[]runtime.Value{[]runtime.Value{float64(1),float64(3)},[]runtime.Value{float64(2),float64(4)}}}
 for _,run:=range []func(context.Context,runtime.Host,map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){Run,RunSequential,func(c context.Context,h runtime.Host,v map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){return RunParallel(c,h,v,2)}}{
  got,_,err:=run(context.Background(),host,map[string]runtime.Value{"rows":rows,"node":node});if err!=nil||!reflect.DeepEqual(got,want){t.Fatalf("%v %v",got,err)}
 }
 instance,err:=NewInstance(host,map[string]runtime.Value{"rows":rows,"node":node});if err!=nil{t.Fatal(err)}
 if _,_,err:=instance.Tick(context.Background(),nil);err!=nil{t.Fatal(err)}
 _,trace,err:=instance.Tick(context.Background(),nil);if err!=nil{t.Fatal(err)};reused:=false;for _,event:=range trace{if event.Node=="groups"&&strings.Contains(event.Reason,"reuse"){reused=true}};if !reused{t.Fatal(trace)}
 cycle:=map[string]runtime.Value{"value":0};cycle["children"]=[]runtime.Value{cycle}
 if _,_,err:=Run(context.Background(),host,map[string]runtime.Value{"rows":rows,"node":cycle});err==nil||!strings.Contains(err.Error(),"call depth exceeds"){t.Fatalf("recursive callback: %v",err)}
}
`)
	command := exec.Command("go", "test", "-race", filepath.Join(dir, "flow.go"), filepath.Join(dir, "flow_test.go"))
	command.Dir = root
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("list conformance: %v\n%s", err, out)
	}
}
