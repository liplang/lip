package tests

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lipalpha/compiler"
)

func TestStringStaticChecks(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{`fn f(flag:bool){return if flag {fail("stop")} else {"wrong"}} flow Bad(flag:bool)->number{x=f(flag); return x}`, "return has type string"},
		{`flow Bad()->number{return fail(1)}`, "expects string"},
		{`flow Bad()->number{return fail()}`, "expects 1 argument"},
		{`flow Bad()->string{return string.nope("x")}`, "unknown string operation"},
		{`flow Bad()->string{return string.trim(1)}`, "expects string"},
		{`flow Bad()->string{return string.join("x",",")}`, "expects list"},
		{`flow Bad()->string{return string.replace("x","x")}`, "expects 3 or 4 arguments"},
		{`flow Bad()->number{return string.slice("x",0,1)}`, "return has type string"},
		{`flow Bad()->list{return string.split("x",1)}`, "expects string"},
	} {
		_, err := compiler.ParseAndBuild(tc.source)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v", tc.source, err)
		}
	}
}

func TestGeneratedStringComposition(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	source := `fn clean(word:string)->string{return string.lower(string.trim(word))}
fn validate(word:string)->string{return if len(word)>0 {word} else {fail("empty word")}}
fn nonempty(word:string)->bool{return len(word)>0}
fn parse(word:string)->number{return string.parse_number(word)}
flow Text(text:string, numbers:string)->object{
 words=list.map(list.filter(list.map(string.split(text,","),clean),nonempty),validate)
 values=list.map(string.split_whitespace(numbers),parse)
 mapped=[string.upper(word) for word in words]
 return {words:words,mapped:mapped,total:list.sum(values),text:string.join(words," / "),index:string.find("你好🌱","🌱"),slice:string.slice("你好🌱",-2,3)}
}`
	writeTestFile(t, filepath.Join(dir, "flow.go"), generatedSource(t, source, false))
	writeTestFile(t, filepath.Join(dir, "flow_test.go"), `package main
import("context";"errors";"reflect";"strings";"testing";"lipalpha/runtime")
func TestText(t *testing.T){
 host:=runtime.DefaultHost();for _,name:=range []string{"string.trim","fail"}{host.Register(name,func(context.Context,[]runtime.Value)runtime.Result{return runtime.Failed(errors.New("override"))})}
 inputs:=map[string]runtime.Value{"text":" A, ,你好 ","numbers":"1 -2 3.5"}
 want:=map[string]runtime.Value{"words":[]runtime.Value{"a","你好"},"mapped":[]runtime.Value{"A","你好"},"total":float64(2.5),"text":"a / 你好","index":float64(2),"slice":"好🌱"}
 for _,run:=range []func(context.Context,runtime.Host,map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){Run,RunSequential,func(c context.Context,h runtime.Host,v map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){return RunParallel(c,h,v,2)}}{
  got,_,err:=run(context.Background(),host,inputs);if err!=nil||!reflect.DeepEqual(got,want){t.Fatalf("%v %v",got,err)}
 }
 instance,err:=NewInstance(host,inputs);if err!=nil{t.Fatal(err)};if _,_,err:=instance.Tick(context.Background(),nil);err!=nil{t.Fatal(err)}
 _,trace,err:=instance.Tick(context.Background(),nil);if err!=nil{t.Fatal(err)};reused:=false;for _,event:=range trace{if event.Node=="words"&&strings.Contains(event.Reason,"reuse"){reused=true}};if !reused{t.Fatal(trace)}
 if _,_,err:=Run(context.Background(),host,map[string]runtime.Value{"text":"a","numbers":"oops"});err==nil||!strings.Contains(err.Error(),"element 0"){t.Fatal(err)}
 if _,err:=runtime.ResolveValue(context.Background(),__lip_fn_validate(host)(context.Background(),[]runtime.Value{""}));err==nil||!strings.Contains(err.Error(),"fail: empty word"){t.Fatal(err)}
}`)
	command := exec.Command("go", "test", "-race", filepath.Join(dir, "flow.go"), filepath.Join(dir, "flow_test.go"))
	command.Dir = root
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("string conformance: %v\n%s", err, out)
	}
}
