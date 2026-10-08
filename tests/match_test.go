package tests

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGeneratedMatchExecution(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "flow.go"), generatedSource(t, `import host "emit"
fn choice(flag: bool) -> any { match flag { true => 7, false => "nil" } }
flow Choice(mode: any, flag: bool, values: list) -> any {
 match mode {
  123 if fail("selected guard") => { return null },
  null => { return choice(flag) },
  true if flag => {
   local = [match x { 0 => 0, _ => x * 2 } for x in values]
   emit(local)
   return local
  },
  true => {
   local = "disabled"
   emit(local)
   return local
  },
  false => {
   local = {kind: "false"}
   emit(local)
   return local
  },
  _ => {
   match flag {
    true => { return match mode { -1 => "negative", _ => mode } },
    false => { return null }
   }
  }
 }
}`, false))
	writeTestFile(t, filepath.Join(dir, "flow_test.go"), `package main
import("context";"errors";"reflect";"strings";"testing";"lipalpha/runtime")
func TestMatch(t *testing.T) {
 for _,run:=range []func(context.Context,runtime.Host,map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){Run,RunSequential,func(c context.Context,h runtime.Host,v map[string]runtime.Value)(runtime.Value,[]runtime.TraceEvent,error){return RunParallel(c,h,v,3)}} {
  for _,tc:=range []struct{mode runtime.Value;flag bool;want runtime.Value;emits int}{
   {nil,true,float64(7),0},{nil,false,"nil",0},
   {true,true,[]runtime.Value{float64(0),float64(2),float64(4)},1},
   {true,false,"disabled",1},{false,true,map[string]runtime.Value{"kind":"false"},1},
   {-1,true,"negative",0},{99,true,99,0},{99,false,nil,0},
  } {
   emitted:=[]runtime.Value{}
   host:=runtime.DefaultHost();host.Register("emit",func(_ context.Context,args []runtime.Value)runtime.Result{emitted=append(emitted,args[0]);return runtime.Ready(nil)})
   value,trace,err:=run(context.Background(),host,map[string]runtime.Value{"mode":tc.mode,"flag":tc.flag,"values":[]int{0,1,2}})
   if err!=nil||!reflect.DeepEqual(value,tc.want)||len(emitted)!=tc.emits {t.Fatalf("mode=%v flag=%v value=%v emitted=%v err=%v",tc.mode,tc.flag,value,emitted,err)}
   completed,skipped:=0,0
   for _,event:=range trace{if strings.HasPrefix(event.Node,"__return_"){if event.Status==runtime.Completed{completed++};if event.Status==runtime.Skipped{skipped++}}}
   if completed!=1||skipped==0{t.Fatalf("return selection: %v",trace)}
  }
  guardHost:=runtime.DefaultHost();guardHost.Register("emit",func(context.Context,[]runtime.Value)runtime.Result{return runtime.Failed(errors.New("unused emit executed"))})
  if _,_,err:=run(context.Background(),guardHost,map[string]runtime.Value{"mode":123,"flag":true,"values":[]int{}});err==nil||!strings.Contains(err.Error(),"selected guard"){t.Fatalf("guard failure: %v",err)}
  ctx,cancel:=context.WithCancel(context.Background());cancel()
  if _,_,err:=run(ctx,guardHost,map[string]runtime.Value{"mode":nil,"flag":true,"values":[]int{}});!errors.Is(err,context.Canceled){t.Fatal(err)}
 }
 emitted:=[]runtime.Value{}
 host:=runtime.DefaultHost();host.Register("emit",func(_ context.Context,args []runtime.Value)runtime.Result{emitted=append(emitted,args[0]);return runtime.Ready(nil)})
 instance,err:=NewInstance(host,map[string]runtime.Value{"mode":true,"flag":true,"values":[]int{0,1,2}});if err!=nil{t.Fatal(err)}
 for _,flag:=range []bool{true,false,true,false}{
  value,_,err:=instance.Tick(context.Background(),map[string]runtime.Value{"flag":flag})
  var want runtime.Value="disabled";if flag{want=[]runtime.Value{float64(0),float64(2),float64(4)}}
  if err!=nil||!reflect.DeepEqual(value,want){t.Fatalf("tick flag=%v value=%v err=%v",flag,value,err)}
 }
 if len(emitted)!=4{t.Fatal(emitted)}
}
`)
	command := exec.Command("go", "test", filepath.Join(dir, "flow.go"), filepath.Join(dir, "flow_test.go"))
	command.Dir = root
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("match execution: %v\n%s", err, out)
	}
}
