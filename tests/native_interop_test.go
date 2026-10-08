package tests

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGeneratedNativeScalarsAndAliasCallForms(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "flow.go"), generatedSource(t, `;;;import host "source" as state;;;
import host "emit" as print
import host "unused"
import go "math" as twice
import go "math" as len
import host "label" as string
fn twice(x: number) -> number { ;; x * 2;; }
flow Native(value: number, enabled: bool, label: string) -> object {
 ;; scalar = state(value);;
 match enabled { true => { print(label) }, _ => {} }
 return {
  calculated: twice(scalar) + twice.Abs(-value),
  length: len(label), absolute: len.Abs(-value), upper: string.upper(label),
  repeated: label * 2, matched: match label { "hi" => true, _ => false },
  boolean: enabled == true, mapped: list.map([scalar], fn(x: number) -> number { x + 1 }), labeled: string(label)
 };;
};;;`, false))
	writeTestFile(t, filepath.Join(dir, "flow_test.go"), `package main
import("context";"reflect";"strings";"sync";"testing";"lipalpha/runtime")
type nativeNumber float64
type nativeBool bool
type nativeString string
func TestNative(t *testing.T) {
 var mu sync.Mutex; var emitted []runtime.Value
 host:=runtime.DefaultHost()
 host.Register("source",func(_ context.Context,args []runtime.Value)runtime.Result{n,err:=runtime.Number(args[0]);if err!=nil{return runtime.Failed(err)};return runtime.Ready(nativeNumber(n+1))})
 host.RegisterPure("math.Abs",func(_ context.Context,args []runtime.Value)runtime.Result{n,_:=runtime.Number(args[0]);if n<0{n=-n};return runtime.Ready(nativeNumber(n))})
 host.Register("emit",func(_ context.Context,args []runtime.Value)runtime.Result{mu.Lock();emitted=append(emitted,args[0]);mu.Unlock();return runtime.Ready(nil)})
 host.RegisterPure("label",func(_ context.Context,args []runtime.Value)runtime.Result{return runtime.Ready(args[0])})
 inputs:=map[string]runtime.Value{"value":nativeNumber(2),"enabled":nativeBool(true),"label":nativeString("hi")}
 want:=map[string]runtime.Value{"calculated":float64(8),"length":float64(2),"absolute":nativeNumber(2),"upper":"HI","repeated":"hihi","matched":true,"boolean":true,"mapped":[]runtime.Value{float64(4)},"labeled":nativeString("hi")}
 for _,run:=range []func(context.Context,runtime.Host,map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){Run,RunSequential,func(c context.Context,h runtime.Host,v map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){return RunParallel(c,h,v,4)}}{
  value,_,err:=run(context.Background(),host,inputs);if err!=nil||!reflect.DeepEqual(value,want){t.Fatalf("value=%v err=%v",value,err)}
 }
 instance,err:=NewInstance(host,inputs);if err!=nil{t.Fatal(err)}
 value,_,err:=instance.Tick(context.Background(),nil);if err!=nil||!reflect.DeepEqual(value,want){t.Fatalf("tick=%v err=%v",value,err)}
 value,_,err=instance.Tick(context.Background(),map[string]runtime.Value{"enabled":nativeBool(false),"label":nativeString("ok"),"value":nativeNumber(3)})
 updated:=map[string]runtime.Value{"calculated":float64(11),"length":float64(2),"absolute":nativeNumber(3),"upper":"OK","repeated":"okok","matched":false,"boolean":false,"mapped":[]runtime.Value{float64(5)},"labeled":nativeString("ok")}
 if err!=nil||!reflect.DeepEqual(value,updated){t.Fatalf("updated=%v err=%v",value,err)}
 if !reflect.DeepEqual(emitted,[]runtime.Value{nativeString("hi"),nativeString("hi"),nativeString("hi"),nativeString("hi")}){t.Fatal(emitted)}
 if _,_,err:=Run(context.Background(),runtime.DefaultHost(),inputs);err==nil||!strings.Contains(err.Error(),"source"){t.Fatalf("missing adapter: %v",err)}
 if got:=RequiredDependencies();len(got)!=6||got[2].Spec!="unused"{t.Fatal(got)}
}
`)
	command := exec.Command("go", "test", filepath.Join(dir, "flow.go"), filepath.Join(dir, "flow_test.go"))
	command.Dir = root
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native scalars and aliases: %v\n%s", err, out)
	}
}
